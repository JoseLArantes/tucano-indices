package ingest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/JoseLArantes/tucano-indices/internal/config"
	"github.com/JoseLArantes/tucano-indices/internal/domain"
)

func TestGetBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "tucano-indices/1.0" {
			t.Errorf("request headers missing: %#v", r.Header)
		}
		_, _ = io.WriteString(w, "payload")
	}))
	defer server.Close()
	body, err := getBytes(context.Background(), server.Client(), server.URL, "application/json", 100)
	if err != nil || string(body) != "payload" {
		t.Fatalf("getBytes() = %q, %v", body, err)
	}

	tooLong := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, strings.Repeat("x", 400))
	}))
	defer tooLong.Close()
	_, err = getBytes(context.Background(), tooLong.Client(), tooLong.URL, "", 1024)
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || len(err.Error()) > len("GET ")+len(tooLong.URL)+len(" returned HTTP 502: ")+300 {
		t.Fatalf("expected bounded HTTP status error, got %v", err)
	}
	if _, err := getBytes(context.Background(), server.Client(), ": invalid", "", 0); err == nil {
		t.Fatal("getBytes accepted an invalid URL")
	}
}

func TestParseSelicCSV(t *testing.T) {
	fixture := "\ufeffRelatório BCB\r\nData;Taxa (% a.a.);Fator diário;Financeiro (R$);Operações;Taxa média;Taxa mediana;Taxa modal;Desvio padrão;Índice de curtose\r\n02/01/2024;11,25;1,0004;1.234,50;1200;11,20;11,25;11,25;0,10;2,00\r\ninvalid;12,00;;;;;;;;\r\n"
	items, err := ParseSelicCSV([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	item := items[0]
	if item.Date != "2024-01-02" || item.Value != "11.2500000000" || item.Source != "csv_import" {
		t.Fatalf("unexpected parsed point: %#v", item)
	}
	var attributes map[string]any
	if err := json.Unmarshal(item.Attributes, &attributes); err != nil {
		t.Fatal(err)
	}
	if attributes["annual_rate"] != "11.2500000000" || attributes["operations_count"] != float64(1200) || attributes["financial_volume"] != "1234.5000000000" {
		t.Fatalf("unexpected parsed attributes: %#v", attributes)
	}
}

func TestParseSelicCSVRejectsMissingOrEmptyData(t *testing.T) {
	for _, raw := range []string{"", "title;description\n", "Data;Taxa (% a.a.)\ninvalid;12,5\n"} {
		if _, err := ParseSelicCSV([]byte(raw)); err == nil {
			t.Errorf("ParseSelicCSV(%q) succeeded, want error", raw)
		}
	}
}

func TestBrazilParsers(t *testing.T) {
	if got := normalizeHeader("Taxa (% a.a.)"); got != "taxaaa" {
		t.Fatalf("normalizeHeader() = %q", got)
	}
	for value, want := range map[string]string{"1.234,56": "1234.56", "12,5": "12.5", "0.75": "0.75", "-2,00": "-2.00"} {
		got, err := normalizeBrazilNumber(value)
		if err != nil || got != want {
			t.Errorf("normalizeBrazilNumber(%q) = %q, %v, want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"", "1,2,3", "abc"} {
		if _, err := normalizeBrazilNumber(value); err == nil {
			t.Errorf("normalizeBrazilNumber(%q) succeeded", value)
		}
	}
	for _, value := range []string{"02/01/2024", "2024-01-02"} {
		if _, err := parseBrazilDate(value); err != nil {
			t.Errorf("parseBrazilDate(%q): %v", value, err)
		}
	}
	if _, err := parseBrazilDate("31-02-2024"); err == nil {
		t.Fatal("parseBrazilDate accepted invalid date")
	}
	if got, err := numberFromAny("12"); err != nil || got != 12 {
		t.Fatalf("numberFromAny(string) = %d, %v", got, err)
	}
	if got, err := numberFromAny(float64(9)); err != nil || got != 9 {
		t.Fatalf("numberFromAny(number) = %d, %v", got, err)
	}
	if _, err := numberFromAny(true); err == nil {
		t.Fatal("numberFromAny accepted a boolean")
	}
}

func TestFetchSGS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bcdata.sgs.433/dados" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("dataInicial") != "02/01/2024" || r.URL.Query().Get("dataFinal") != "02/01/2024" {
			t.Errorf("unexpected query: %v", r.URL.Query())
		}
		_, _ = io.WriteString(w, `[{"data":"02/01/2024","valor":"4,50"}]`)
	}))
	defer server.Close()
	syncer := &Syncer{cfg: config.Config{SGSBaseURL: server.URL}, client: server.Client()}
	from := date("2024-01-02")
	items, err := syncer.fetchSGS(context.Background(), domain.Series{Source: "bcb_sgs:433"}, from, from)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Date != "2024-01-02" || items[0].Value != "4.50" || items[0].Source != "bcb_sgs" {
		t.Fatalf("unexpected SGS result: %#v", items)
	}
	for _, source := range []string{"bcb_sgs", "bcb_sgs:nope", "bcb_sgs:0"} {
		if _, err := syncer.fetchSGS(context.Background(), domain.Series{Source: source}, from, from); err == nil {
			t.Errorf("fetchSGS(source=%q) succeeded", source)
		}
	}
}

func TestFetchFocus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ExpectativasMercadoAnuais" || !strings.Contains(r.URL.Query().Get("$filter"), "Indicador eq 'IPCA'") {
			t.Errorf("unexpected Focus URL: %s", r.URL.String())
		}
		_, _ = io.WriteString(w, `{"value":[{"Data":"2024-01-02","DataReferencia":"2025","Mediana":4.25},{"Data":"2024-01-03","DataReferencia":"","Mediana":4.5}]}`)
	}))
	defer server.Close()
	syncer := &Syncer{cfg: config.Config{FocusBaseURL: server.URL}, client: server.Client()}
	items, err := syncer.fetchFocus(context.Background(), domain.Series{Code: "focus_ipca"}, date("2024-01-01"), date("2024-01-31"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Date != "2024-01-02" || items[0].Value != "4.2500000000" || items[0].Dimension != "2025" {
		t.Fatalf("unexpected Focus result: %#v", items)
	}
	if _, err := syncer.fetchFocus(context.Background(), domain.Series{Code: "unknown"}, time.Time{}, time.Time{}); err == nil {
		t.Fatal("fetchFocus accepted unsupported series")
	}
}

func TestFetchTesouro(t *testing.T) {
	csv := "Tipo Título;Data Vencimento;Data Base;Taxa Compra Manhã;Taxa Venda Manhã\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2035;02/01/2024;5,10;5,30\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2040;03/01/2024;5,00;\n" +
		"Tesouro Prefixado;01/01/2030;02/01/2024;10,00;11,00\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2045;01/01/2024;5,00;5,20\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rates.csv" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, csv)
	}))
	defer server.Close()
	syncer := &Syncer{cfg: config.Config{TesouroCSVURL: server.URL + "/rates.csv"}, client: server.Client()}
	items, err := syncer.fetchTesouro(context.Background(), domain.Series{}, date("2024-01-02"), date("2024-01-03"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Value != "5.2000000000" || items[0].Dimension != "2035" || items[1].Value != "5.0000000000" {
		t.Fatalf("unexpected Tesouro result: %#v", items)
	}
}

func TestFetchB3(t *testing.T) {
	var years []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		prefix := "/GetPortfolioDay/"
		if !strings.Contains(r.URL.Path, prefix) {
			t.Errorf("unexpected B3 path: %q", r.URL.Path)
		}
		encoded := strings.TrimPrefix(r.URL.EscapedPath(), prefix)
		decoded, err := url.PathUnescape(encoded)
		if err != nil {
			t.Errorf("decode request payload: %v", err)
		}
		payload, err := base64.StdEncoding.DecodeString(decoded)
		if err != nil {
			t.Errorf("decode base64 payload: %v", err)
		}
		var request b3Request
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("decode request JSON: %v", err)
		}
		years = append(years, request.Year)
		if request.Index != "IBOVESPA" || request.Language != "pt-br" {
			t.Errorf("unexpected B3 request: %#v", request)
		}
		_, _ = io.WriteString(w, `{"results":[{"day":"2","rateValue1":"123.456,78","rateValue2":"1.234,50"},{"day":31,"rateValue2":"9,90"}]}`)
	}))
	defer server.Close()
	syncer := &Syncer{cfg: config.Config{B3BaseURL: server.URL}, client: server.Client()}
	items, err := syncer.fetchB3(context.Background(), domain.Series{Source: "b3_statistics:IBOVESPA"}, date("2024-01-01"), date("2025-02-28"))
	if err != nil {
		t.Fatal(err)
	}
	if len(years) != 2 || years[0] != "2024" || years[1] != "2025" {
		t.Fatalf("years requested = %v", years)
	}
	if len(items) != 4 || items[0].Date != "2024-01-02" || items[0].Value != "123456.78" || items[1].Date != "2024-02-02" || items[2].Date != "2025-01-02" || items[3].Date != "2025-02-02" {
		t.Fatalf("unexpected B3 result: %#v", items)
	}
	for _, source := range []string{"b3_statistics", "bad:mapping:extra"} {
		if _, err := syncer.fetchB3(context.Background(), domain.Series{Source: source}, date("2024-01-01"), date("2024-01-01")); err == nil {
			t.Errorf("fetchB3(source=%q) succeeded", source)
		}
	}
}

func TestFetchSelic(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected NovoSelic request: %s %#v", r.Method, r.Header)
		}
		var request selicRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.From != "02/01/2024" || request.To != "02/01/2024" {
			t.Errorf("unexpected NovoSelic range: %#v", request)
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "1" {
			_, _ = io.WriteString(w, `{"totalItems":1,"registros":[{"dataCotacao":"02/01/2024","taxaAnual":11.25,"fatorDiario":1.0004,"qtdOperacoes":25}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"totalItems":1,"registros":[]}`)
	}))
	defer server.Close()
	syncer := &Syncer{cfg: config.Config{SelicSearchURL: server.URL}, client: server.Client()}
	items, err := syncer.fetchSelic(context.Background(), date("2024-01-02"), date("2024-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Date != "2024-01-02" || items[0].Value != "11.2500000000" || len(pages) != 1 || pages[0] != "1" {
		t.Fatalf("unexpected NovoSelic result: %#v pages=%v", items, pages)
	}
	var attrs map[string]any
	if err := json.Unmarshal(items[0].Attributes, &attrs); err != nil || attrs["operations_count"] != float64(25) {
		t.Fatalf("unexpected NovoSelic attributes: %#v, %v", attrs, err)
	}
}

func TestSourceMatches(t *testing.T) {
	for _, test := range []struct {
		source string
		metric string
		want   bool
	}{
		{source: "sgs", metric: "bcb_sgs:433", want: true},
		{source: "sgs", metric: "bcb_focus", want: false},
		{source: "focus", metric: "bcb_focus", want: true},
		{source: "tesouro", metric: "tesouro", want: true},
		{source: "b3", metric: "b3_statistics:IBOVESPA", want: true},
		{source: "selic", metric: "bcb_novoselic", want: true},
		{source: "unknown", metric: "bcb_sgs:1", want: false},
	} {
		if got := sourceMatches(test.source, test.metric); got != test.want {
			t.Errorf("sourceMatches(%q, %q) = %v, want %v", test.source, test.metric, got, test.want)
		}
	}
}

type fakeSeriesStore struct {
	series       []domain.Series
	locked       bool
	acquireErr   error
	startErr     error
	finishErr    error
	upsertErr    error
	maxDate      time.Time
	acquireCalls int
	starts       int
	finishes     int
	upserts      int
	lastStatus   string
	lastRunError error
	lastInputs   []domain.ObservationInput
}

func (s *fakeSeriesStore) ListSeries(context.Context, int, []string) ([]domain.Series, error) {
	return s.series, nil
}
func (s *fakeSeriesStore) Latest(context.Context, string, string) ([]domain.Observation, error) {
	return nil, nil
}
func (s *fakeSeriesStore) MaxDate(context.Context, string, string) (time.Time, error) {
	return s.maxDate, nil
}
func (s *fakeSeriesStore) Upsert(_ context.Context, _ string, inputs []domain.ObservationInput, _ string) (int, error) {
	s.upserts++
	s.lastInputs = inputs
	if s.upsertErr != nil {
		return 0, s.upsertErr
	}
	return len(inputs), nil
}
func (s *fakeSeriesStore) StartRun(context.Context, string, time.Time, time.Time) (string, error) {
	s.starts++
	if s.startErr != nil {
		return "", s.startErr
	}
	return "run-id", nil
}
func (s *fakeSeriesStore) FinishRun(_ context.Context, _ string, status string, _, _ int, runErr error) error {
	s.finishes++
	s.lastStatus = status
	s.lastRunError = runErr
	return s.finishErr
}
func (s *fakeSeriesStore) AcquireSourceLock(context.Context, int64) (func(), bool, error) {
	s.acquireCalls++
	return func() {}, !s.locked, s.acquireErr
}

func TestSyncerRunValidationAndLock(t *testing.T) {
	store := &fakeSeriesStore{}
	syncer := &Syncer{store: store}
	if _, err := syncer.Run(context.Background(), "invalid", SyncOptions{}); err == nil {
		t.Fatal("Run accepted unknown source")
	}
	from, to := date("2024-02-01"), date("2024-01-01")
	if _, err := syncer.Run(context.Background(), "sgs", SyncOptions{From: &from, To: &to}); err == nil {
		t.Fatal("Run accepted inverted window")
	}
	if store.acquireCalls != 0 {
		t.Fatalf("invalid source/window acquired lock %d times", store.acquireCalls)
	}
	store.locked = true
	result, err := syncer.Run(context.Background(), "sgs", SyncOptions{})
	if err != nil || result.Status != "skipped" || store.starts != 0 {
		t.Fatalf("locked run = %#v, %v; starts=%d", result, err, store.starts)
	}
}

func TestSyncerRunPersistsStatusAndAggregatesAll(t *testing.T) {
	store := &fakeSeriesStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"data":"02/01/2024","valor":"4,50"}]`)
	}))
	defer server.Close()
	syncer := &Syncer{store: store, cfg: config.Config{SGSBaseURL: server.URL, InitialYear: 2024}, client: server.Client()}
	from, to := date("2024-01-02"), date("2024-01-02")
	result, err := syncer.Run(context.Background(), "sgs", SyncOptions{From: &from, To: &to})
	if err != nil || result.Status != "succeeded" || result.PointsSeen != 1 || result.PointsStored != 1 || len(result.Metrics) != 1 || result.Metrics[0] != "ipca" {
		t.Fatalf("successful run = %#v, %v", result, err)
	}
	if store.starts != 1 || store.finishes != 1 || store.lastStatus != "succeeded" || store.upserts != 1 {
		t.Fatalf("run lifecycle counters: %#v", store)
	}

	empty := &fakeSeriesStore{}
	allSyncer := &Syncer{store: empty, cfg: config.Config{InitialYear: 2024}}
	all, err := allSyncer.Run(context.Background(), "all", SyncOptions{From: &from, To: &to})
	if err != nil || all.Source != "all" || all.Status != "succeeded" || empty.starts != 5 || empty.finishes != 5 {
		t.Fatalf("aggregate run = %#v, %v; starts=%d finishes=%d", all, err, empty.starts, empty.finishes)
	}
}

func TestSyncerRunRecordsFetchFailure(t *testing.T) {
	store := &fakeSeriesStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "temporarily unavailable")
	}))
	defer server.Close()
	syncer := &Syncer{store: store, cfg: config.Config{SGSBaseURL: server.URL}, client: server.Client()}
	from, to := date("2024-01-02"), date("2024-01-02")
	result, err := syncer.Run(context.Background(), "sgs", SyncOptions{From: &from, To: &to})
	if err == nil || result.Status != "failed" || !strings.Contains(result.Error, "HTTP 503") {
		t.Fatalf("failed run = %#v, %v", result, err)
	}
	if store.finishes != 1 || store.lastStatus != "failed" || store.lastRunError == nil {
		t.Fatalf("failed run was not recorded: %#v", store)
	}
}

func TestSyncerRunHandlesStoreFailures(t *testing.T) {
	store := &fakeSeriesStore{startErr: errors.New("cannot start")}
	syncer := &Syncer{store: store}
	if _, err := syncer.Run(context.Background(), "sgs", SyncOptions{}); err == nil || !strings.Contains(err.Error(), "cannot start") {
		t.Fatalf("start failure = %v", err)
	}
	store = &fakeSeriesStore{finishErr: errors.New("cannot finish")}
	syncer = &Syncer{store: store}
	if _, err := syncer.Run(context.Background(), "sgs", SyncOptions{}); err == nil || !strings.Contains(err.Error(), "cannot finish") {
		t.Fatalf("finish failure = %v", err)
	}
}

func TestSyncerAllReportsPartialFailure(t *testing.T) {
	store := &fakeSeriesStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	syncer := &Syncer{store: store, cfg: config.Config{SGSBaseURL: server.URL}, client: server.Client()}
	from, to := date("2024-01-02"), date("2024-01-02")
	result, err := syncer.Run(context.Background(), "all", SyncOptions{From: &from, To: &to})
	if err == nil || result.Status != "failed" || !strings.Contains(result.Error, "sgs") || store.starts != 5 {
		t.Fatalf("partial aggregate = %#v, %v; starts=%d", result, err, store.starts)
	}
}

func date(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(fmt.Sprintf("invalid test date %s: %v", value, err))
	}
	return parsed
}
