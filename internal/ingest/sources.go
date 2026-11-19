package ingest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/unicode/norm"

	"github.com/JoseLArantes/tucano-indices/internal/config"
	"github.com/JoseLArantes/tucano-indices/internal/domain"
)

const (
	defaultTesouroCKAN = "https://www.tesourotransparente.gov.br/ckan/api/3/action/package_show?id=taxas-dos-titulos-ofertados-pelo-tesouro-direto"
	defaultTesouroCSV  = "https://www.tesourotransparente.gov.br/ckan/dataset/df56aa42-484a-4a59-8184-7676580c81e3/resource/796d2059-14e9-44e3-80c9-2d9e30b405c1/download/precotaxatesourodireto.csv"
	defaultSelicURL    = "https://www3.bcb.gov.br/novoselic/rest/taxaSelicApurada/pub/search"
	defaultB3Base      = "https://sistemaswebb3-listados.b3.com.br/indexStatisticsProxy/IndexCall"
)

type SeriesStore interface {
	ListSeries(context.Context, int, []string) ([]domain.Series, error)
	Latest(context.Context, string, string) ([]domain.Observation, error)
	MaxDate(context.Context, string, string) (time.Time, error)
	Upsert(context.Context, string, []domain.ObservationInput, string) (int, error)
	StartRun(context.Context, string, time.Time, time.Time) (string, error)
	FinishRun(context.Context, string, string, int, int, error) error
	AcquireSourceLock(context.Context, int64) (func(), bool, error)
}

type Syncer struct {
	pool   *pgxpool.Pool
	store  SeriesStore
	cfg    config.Config
	client doer
}

type SyncOptions struct {
	From *time.Time
	To   *time.Time
}

type SyncResult struct {
	Source       string   `json:"source"`
	Status       string   `json:"status"`
	Metrics      []string `json:"series"`
	PointsSeen   int      `json:"points_seen"`
	PointsStored int      `json:"points_stored"`
	Error        string   `json:"error,omitempty"`
}

func NewSyncer(pool *pgxpool.Pool, dbstore SeriesStore, cfg config.Config) *Syncer {
	return &Syncer{pool: pool, store: dbstore, cfg: cfg, client: newClient(cfg.HTTPTimeout)}
}

var sourceLocks = map[string]int64{"sgs": 74220001, "focus": 74220002, "tesouro": 74220003, "b3": 74220004, "selic": 74220005}

func (s *Syncer) Run(ctx context.Context, source string, opts SyncOptions) (SyncResult, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "all" {
		result := SyncResult{Source: "all", Status: "succeeded"}
		for _, name := range []string{"sgs", "focus", "tesouro", "b3", "selic"} {
			one, err := s.Run(ctx, name, opts)
			result.Metrics = append(result.Metrics, one.Metrics...)
			result.PointsSeen += one.PointsSeen
			result.PointsStored += one.PointsStored
			if err != nil {
				result.Status = "failed"
				result.Error = strings.TrimSpace(result.Error + "; " + name + ": " + err.Error())
			}
		}
		if result.Status == "failed" {
			return result, errors.New(result.Error)
		}
		return result, nil
	}
	lockID, ok := sourceLocks[source]
	if !ok {
		return SyncResult{}, fmt.Errorf("unknown source %q", source)
	}
	from, to, err := s.window(opts)
	if err != nil {
		return SyncResult{}, err
	}
	unlock, locked, err := s.store.AcquireSourceLock(ctx, lockID)
	if err != nil {
		return SyncResult{}, err
	}
	if !locked {
		return SyncResult{Source: source, Status: "skipped", Error: "another sync is already running"}, nil
	}
	defer unlock()
	runID, err := s.store.StartRun(ctx, source, from, to)
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{Source: source, Status: "running"}
	series, points, stored, runErr := s.syncSource(ctx, source, from, to, opts.From != nil)
	result.Metrics, result.PointsSeen, result.PointsStored = series, points, stored
	if runErr != nil {
		result.Status = "failed"
		result.Error = runErr.Error()
		_ = s.store.FinishRun(context.Background(), runID, result.Status, points, stored, runErr)
		return result, runErr
	}
	result.Status = "succeeded"
	if err := s.store.FinishRun(ctx, runID, result.Status, points, stored, nil); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Syncer) window(opts SyncOptions) (time.Time, time.Time, error) {
	loc, _ := time.LoadLocation("America/Sao_Paulo")
	to := time.Now().In(loc)
	to = time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	if opts.To != nil {
		to = dateOnly(*opts.To)
	}
	from := to.AddDate(0, 0, -30)
	if opts.From != nil {
		from = dateOnly(*opts.From)
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("from date cannot be after to date")
	}
	return from, to, nil
}

func dateOnly(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Syncer) syncSource(ctx context.Context, source string, from, to time.Time, explicitFrom bool) ([]string, int, int, error) {
	catalog, err := s.store.ListSeries(ctx, 0, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	selected := make([]domain.Series, 0)
	for _, item := range catalog {
		if sourceMatches(source, item.Source) {
			selected = append(selected, item)
		}
	}
	touched := make([]string, 0, len(selected))
	points, stored := 0, 0
	for _, series := range selected {
		start := from
		if !explicitFrom {
			latest, err := s.store.MaxDate(ctx, series.Code, "")
			if err != nil {
				return touched, points, stored, err
			}
			if latest.IsZero() {
				if strings.HasPrefix(series.Source, "b3_statistics:") {
					start = time.Date(1998, 1, 1, 0, 0, 0, 0, time.UTC)
				} else {
					start = time.Date(s.cfg.InitialYear, 1, 1, 0, 0, 0, 0, time.UTC)
				}
			} else {
				start = latest.AddDate(0, 0, -5)
			}
		}
		var items []domain.ObservationInput
		switch source {
		case "sgs":
			items, err = s.fetchSGS(ctx, series, start, to)
		case "focus":
			items, err = s.fetchFocus(ctx, series, start, to)
		case "tesouro":
			items, err = s.fetchTesouro(ctx, series, start, to)
		case "b3":
			items, err = s.fetchB3(ctx, series, start, to)
		case "selic":
			items, err = s.fetchSelic(ctx, start, to)
			if series.Code != "selic_apurada" {
				continue
			}
		default:
			return touched, points, stored, fmt.Errorf("unknown source %q", source)
		}
		if err != nil {
			return touched, points, stored, fmt.Errorf("%s: %w", series.Code, err)
		}
		touched = append(touched, series.Code)
		points += len(items)
		added, err := s.store.Upsert(ctx, series.Code, items, source)
		if err != nil {
			return touched, points, stored, fmt.Errorf("persist %s: %w", series.Code, err)
		}
		stored += added
	}
	return touched, points, stored, nil
}

func sourceMatches(source, metricSource string) bool {
	switch source {
	case "sgs":
		return strings.HasPrefix(metricSource, "bcb_sgs:")
	case "focus":
		return metricSource == "bcb_focus"
	case "tesouro":
		return metricSource == "tesouro"
	case "b3":
		return strings.HasPrefix(metricSource, "b3_statistics:")
	case "selic":
		return metricSource == "bcb_novoselic"
	default:
		return false
	}
}

type sgsValue struct {
	Date  string `json:"data"`
	Value string `json:"valor"`
}

func (s *Syncer) fetchSGS(ctx context.Context, series domain.Series, from, to time.Time) ([]domain.ObservationInput, error) {
	parts := strings.Split(series.Source, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid SGS mapping %q", series.Source)
	}
	var code int
	if _, err := fmt.Sscan(parts[1], &code); err != nil || code <= 0 {
		return nil, fmt.Errorf("invalid SGS code %q", parts[1])
	}
	var out []domain.ObservationInput
	cursor := from
	for !cursor.After(to) {
		chunkEnd := cursor.AddDate(10, 0, -1)
		if chunkEnd.After(to) {
			chunkEnd = to
		}
		u := fmt.Sprintf("%s/bcdata.sgs.%d/dados?formato=json&dataInicial=%s&dataFinal=%s", strings.TrimRight(s.cfg.SGSBaseURL, "/"), code, cursor.Format("02/01/2006"), chunkEnd.Format("02/01/2006"))
		body, err := getBytes(ctx, s.client, u, "application/json", 16<<20)
		if err != nil {
			return nil, err
		}
		var rows []sgsValue
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("decode SGS %d: %w", code, err)
		}
		for _, row := range rows {
			date, err := time.Parse("02/01/2006", strings.TrimSpace(row.Date))
			if err != nil {
				return nil, fmt.Errorf("invalid SGS date %q", row.Date)
			}
			value := strings.ReplaceAll(strings.TrimSpace(row.Value), ",", ".")
			if value == "" {
				continue
			}
			out = append(out, domain.ObservationInput{Date: date.Format("2006-01-02"), Value: value, Source: "bcb_sgs"})
		}
		cursor = chunkEnd.AddDate(0, 0, 1)
	}
	return out, nil
}

var focusIndicators = map[string]string{"focus_ipca": "IPCA", "focus_selic": "Selic", "focus_usd": "Câmbio"}

type focusReply struct {
	Value []struct {
		Date      string  `json:"Data"`
		Target    string  `json:"DataReferencia"`
		Median    float64 `json:"Mediana"`
		Indicator string  `json:"Indicador"`
	} `json:"value"`
}

func (s *Syncer) fetchFocus(ctx context.Context, series domain.Series, from, to time.Time) ([]domain.ObservationInput, error) {
	indicator, ok := focusIndicators[series.Code]
	if !ok {
		return nil, fmt.Errorf("unsupported Focus series %s", series.Code)
	}
	filter := fmt.Sprintf("Indicador eq '%s' and baseCalculo eq 0 and Data ge '%s' and Data le '%s'", indicator, from.Format("2006-01-02"), to.Format("2006-01-02"))
	query := url.Values{}
	query.Set("$filter", filter)
	query.Set("$select", "Indicador,Data,DataReferencia,Mediana,baseCalculo")
	query.Set("$orderby", "Data asc")
	query.Set("$format", "json")
	query.Set("$top", "100000")
	u := strings.TrimRight(s.cfg.FocusBaseURL, "/") + "/ExpectativasMercadoAnuais?" + strings.ReplaceAll(query.Encode(), "+", "%20")
	body, err := getBytes(ctx, s.client, u, "application/json", 32<<20)
	if err != nil {
		return nil, err
	}
	var response focusReply
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	out := make([]domain.ObservationInput, 0, len(response.Value))
	for _, row := range response.Value {
		if row.Date == "" || row.Target == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", row.Date); err != nil {
			return nil, fmt.Errorf("invalid Focus date %q", row.Date)
		}
		out = append(out, domain.ObservationInput{Date: row.Date, Value: strconv.FormatFloat(row.Median, 'f', 10, 64), Dimension: row.Target, Source: "bcb_focus"})
	}
	return out, nil
}

type ckanPackage struct {
	Success bool `json:"success"`
	Result  struct {
		Resources []struct {
			Format string `json:"format"`
			URL    string `json:"url"`
		} `json:"resources"`
	} `json:"result"`
}

func (s *Syncer) fetchTesouro(ctx context.Context, series domain.Series, from, to time.Time) ([]domain.ObservationInput, error) {
	ckan := s.cfg.TesouroCKANURL
	if ckan == "" {
		ckan = defaultTesouroCKAN
	}
	csvURL := s.cfg.TesouroCSVURL
	if csvURL == "" {
		body, err := getBytes(ctx, s.client, ckan, "application/json", 8<<20)
		if err == nil {
			var packageInfo ckanPackage
			if json.Unmarshal(body, &packageInfo) == nil {
				for _, resource := range packageInfo.Result.Resources {
					if strings.EqualFold(resource.Format, "CSV") && resource.URL != "" {
						csvURL = resource.URL
						break
					}
				}
			}
		}
		if csvURL == "" {
			csvURL = defaultTesouroCSV
		}
	}
	raw, err := getBytes(ctx, s.client, csvURL, "text/csv,text/plain,*/*", 48<<20)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.Comma, reader.FieldsPerRecord, reader.LazyQuotes = ';', -1, true
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 2 {
		return nil, fmt.Errorf("invalid Tesouro CSV: %w", err)
	}
	columns := make(map[string]int)
	for i, name := range rows[0] {
		columns[normalizeHeader(name)] = i
	}
	index := func(names ...string) int {
		for _, name := range names {
			if i, ok := columns[name]; ok {
				return i
			}
		}
		return -1
	}
	iType, iMaturity, iDate := index("tipotitulo"), index("datavencimento"), index("database")
	iBuy, iSell := index("taxacompramanha"), index("taxavendamanha")
	if iType < 0 || iMaturity < 0 || iDate < 0 || (iBuy < 0 && iSell < 0) {
		return nil, fmt.Errorf("Tesouro CSV is missing required columns")
	}
	out := make([]domain.ObservationInput, 0)
	for _, row := range rows[1:] {
		if maxIndex(iType, iMaturity, iDate, iBuy, iSell) >= len(row) || strings.TrimSpace(row[iType]) != "Tesouro IPCA+ com Juros Semestrais" {
			continue
		}
		date, err := parseBrazilDate(row[iDate])
		if err != nil || date.Before(from) || date.After(to) {
			continue
		}
		maturity, err := parseBrazilDate(row[iMaturity])
		if err != nil {
			continue
		}
		buy, buyOK := parseBrazilNumber(row, iBuy)
		sell, sellOK := parseBrazilNumber(row, iSell)
		var rate float64
		switch {
		case buyOK && sellOK:
			rate = (buy + sell) / 2
		case buyOK:
			rate = buy
		case sellOK:
			rate = sell
		default:
			continue
		}
		out = append(out, domain.ObservationInput{Date: date.Format("2006-01-02"), Value: strconv.FormatFloat(rate, 'f', 10, 64), Dimension: strconv.Itoa(maturity.Year()), Source: "tesouro_transparente"})
	}
	return out, nil
}

type b3Request struct {
	Index    string `json:"index"`
	Language string `json:"language"`
	Year     string `json:"year"`
}

type b3YearResponse struct {
	Results []map[string]any `json:"results"`
}

func (s *Syncer) fetchB3(ctx context.Context, series domain.Series, from, to time.Time) ([]domain.ObservationInput, error) {
	parts := strings.Split(series.Source, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid B3 mapping %q", series.Source)
	}
	base := strings.TrimRight(s.cfg.B3BaseURL, "/")
	if base == "" {
		base = defaultB3Base
	}
	out := make([]domain.ObservationInput, 0)
	for year := from.Year(); year <= to.Year(); year++ {
		payload, _ := json.Marshal(b3Request{Index: parts[1], Language: "pt-br", Year: strconv.Itoa(year)})
		encoded := url.PathEscape(base64.StdEncoding.EncodeToString(payload))
		body, err := getBytes(ctx, s.client, base+"/GetPortfolioDay/"+encoded, "application/json", 16<<20)
		if err != nil {
			return nil, err
		}
		var response b3YearResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("decode B3 statistics %s %d: %w", parts[1], year, err)
		}
		for _, row := range response.Results {
			day, err := numberFromAny(row["day"])
			if err != nil || day < 1 || day > 31 {
				continue
			}
			for month := 1; month <= 12; month++ {
				field := fmt.Sprintf("rateValue%d", month)
				value, ok := row[field]
				if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
					continue
				}
				ref := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
				if ref.Month() != time.Month(month) || ref.Before(from) || ref.After(to) {
					continue
				}
				normalized, err := normalizeBrazilNumber(fmt.Sprint(value))
				if err != nil {
					return nil, fmt.Errorf("invalid B3 value %q for %s", value, ref.Format("2006-01-02"))
				}
				out = append(out, domain.ObservationInput{Date: ref.Format("2006-01-02"), Value: normalized, Source: "b3_statistics"})
			}
		}
	}
	return out, nil
}

func numberFromAny(value any) (int, error) {
	switch typed := value.(type) {
	case float64:
		return int(typed), nil
	case string:
		return strconv.Atoi(strings.TrimSpace(typed))
	default:
		return 0, fmt.Errorf("unsupported day value %T", value)
	}
}

type selicRequest struct {
	From string `json:"dataInicial"`
	To   string `json:"dataFinal"`
}

type selicPage struct {
	Total   int `json:"totalItems"`
	Records []struct {
		Date      string  `json:"dataCotacao"`
		Daily     float64 `json:"fatorDiario"`
		Mean      float64 `json:"media"`
		Median    float64 `json:"mediana"`
		Mode      float64 `json:"moda"`
		StdDev    float64 `json:"desvioPadrao"`
		Kurtosis  float64 `json:"indiceCurtose"`
		Financial float64 `json:"financeiro"`
		Count     int     `json:"qtdOperacoes"`
		Annual    float64 `json:"taxaAnual"`
	} `json:"registros"`
	Errors []struct {
		Message string `json:"mensagem"`
	} `json:"erros"`
}

func (s *Syncer) fetchSelic(ctx context.Context, from, to time.Time) ([]domain.ObservationInput, error) {
	endpoint := s.cfg.SelicSearchURL
	if endpoint == "" {
		endpoint = defaultSelicURL
	}
	payload, _ := json.Marshal(selicRequest{From: from.Format("02/01/2006"), To: to.Format("02/01/2006")})
	out := make([]domain.ObservationInput, 0)
	for page := 1; page <= 10000; page++ {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s%spage=%d&pageSize=100", endpoint, sep, page), bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "tucano-indices/selic-sync")
		resp, err := s.client.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("NovoSelic returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var response selicPage
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, err
		}
		if len(response.Errors) > 0 {
			return nil, fmt.Errorf("NovoSelic error: %s", response.Errors[0].Message)
		}
		for _, item := range response.Records {
			date, err := time.Parse("02/01/2006", strings.TrimSpace(item.Date))
			if err != nil {
				return nil, err
			}
			attributes, _ := json.Marshal(map[string]any{
				"annual_rate": formatFloat(item.Annual), "daily_factor": formatFloat(item.Daily),
				"financial_volume": formatFloat(item.Financial), "operations_count": item.Count,
				"average_rate": formatFloat(item.Mean), "median_rate": formatFloat(item.Median),
				"modal_rate": formatFloat(item.Mode), "std_deviation": formatFloat(item.StdDev),
				"kurtosis_index": formatFloat(item.Kurtosis),
			})
			out = append(out, domain.ObservationInput{Date: date.Format("2006-01-02"), Value: formatFloat(item.Annual), Attributes: attributes, Source: "bcb_novoselic"})
		}
		if len(response.Records) == 0 || len(response.Records) < 100 || len(out) >= response.Total {
			break
		}
	}
	return out, nil
}

func formatFloat(value float64) string { return strconv.FormatFloat(value, 'f', 10, 64) }

func normalizeHeader(value string) string {
	var out strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(value)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func maxIndex(values ...int) int {
	max := -1
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func parseBrazilDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, format := range []string{"02/01/2006", "2006-01-02"} {
		if date, err := time.Parse(format, value); err == nil {
			return date, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid Brazilian date %q", value)
}

func parseBrazilNumber(row []string, column int) (float64, bool) {
	if column < 0 || column >= len(row) {
		return 0, false
	}
	value := strings.TrimSpace(row[column])
	if value == "" {
		return 0, false
	}
	normalized, err := normalizeBrazilNumber(value)
	if err != nil {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(normalized, 64)
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func normalizeBrazilNumber(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, ",") {
		value = strings.ReplaceAll(value, ".", "")
		value = strings.ReplaceAll(value, ",", ".")
	}
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return "", err
	}
	return value, nil
}

// ParseSelicCSV accepts the official semicolon-delimited BCB export.
func ParseSelicCSV(raw []byte) ([]domain.ObservationInput, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.Comma, reader.LazyQuotes, reader.FieldsPerRecord = ';', true, -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	headerAt := -1
	for i, row := range rows {
		if len(row) > 0 && normalizeHeader(row[0]) == "data" {
			headerAt = i
			break
		}
	}
	if headerAt < 0 {
		return nil, fmt.Errorf("Selic CSV header is missing")
	}
	columns := map[string]int{}
	for i, value := range rows[headerAt] {
		columns[normalizeHeader(value)] = i
	}
	find := func(key string) int { return columns[normalizeHeader(key)] }
	dateCol, annualCol := find("Data"), find("Taxa (% a.a.)")
	if dateCol < 0 || annualCol < 0 {
		return nil, fmt.Errorf("Selic CSV is missing date or annual rate")
	}
	fields := []string{"Fator diário", "Financeiro (R$)", "Operações", "Taxa média", "Taxa mediana", "Taxa modal", "Desvio padrão", "Índice de curtose"}
	keys := []string{"daily_factor", "financial_volume", "operations_count", "average_rate", "median_rate", "modal_rate", "std_deviation", "kurtosis_index"}
	cols := make([]int, len(fields))
	for i, name := range fields {
		cols[i] = find(name)
	}
	out := make([]domain.ObservationInput, 0)
	for _, row := range rows[headerAt+1:] {
		if maxIndex(dateCol, annualCol) >= len(row) {
			continue
		}
		date, err := parseBrazilDate(row[dateCol])
		if err != nil {
			continue
		}
		annual, ok := parseBrazilNumber(row, annualCol)
		if !ok {
			continue
		}
		attributes := map[string]any{"annual_rate": formatFloat(annual)}
		for i, column := range cols {
			if column < 0 || column >= len(row) || strings.TrimSpace(row[column]) == "" {
				continue
			}
			if keys[i] == "operations_count" {
				count, err := strconv.Atoi(strings.TrimSpace(row[column]))
				if err == nil {
					attributes[keys[i]] = count
				}
				continue
			}
			value, ok := parseBrazilNumber(row, column)
			if ok {
				attributes[keys[i]] = formatFloat(value)
			}
		}
		extra, _ := json.Marshal(attributes)
		out = append(out, domain.ObservationInput{Date: date.Format("2006-01-02"), Value: formatFloat(annual), Attributes: extra, Source: "csv_import"})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Selic CSV contains no usable observations")
	}
	return out, nil
}
