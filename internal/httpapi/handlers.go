package httpapi

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/JoseLArantes/tucano-indices/internal/domain"
	"github.com/JoseLArantes/tucano-indices/internal/ingest"
	"github.com/JoseLArantes/tucano-indices/internal/store"
)

type database interface {
	Ping(context.Context) error
	ListSeries(context.Context, int, []string) ([]domain.Series, error)
	GetSeries(context.Context, string) (domain.Series, error)
	Latest(context.Context, string, string) ([]domain.Observation, error)
	LatestAsOf(context.Context, string, string, string) ([]domain.Observation, error)
	Series(context.Context, string, store.SeriesQuery) ([]domain.ChartPoint, error)
	ObservationSeries(context.Context, string, store.SeriesQuery) ([]domain.Observation, error)
	CountCurrent(context.Context, string, string, string) (int64, error)
	Upsert(context.Context, string, []domain.ObservationInput, string) (int, error)
	IdempotentUpsert(context.Context, string, string, string, string, []domain.ObservationInput, string) (int, []byte, bool, error)
	RecentRuns(context.Context, string, int) ([]domain.SyncRun, error)
}

type Handler struct {
	db      database
	syncer  *ingest.Syncer
	token   string
	name    string
	openAPI []byte
}

//go:embed swagger.html
var swaggerHTML []byte

func New(db database, syncer *ingest.Syncer, token, name string, openAPI []byte) *Handler {
	return &Handler{db: db, syncer: syncer, token: token, name: name, openAPI: openAPI}
}

func (h *Handler) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", h.health)
	r.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(h.openAPI)
	})
	r.Get("/swagger", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(swaggerHTML)
	})
	r.Group(func(api chi.Router) {
		api.Use(tokenMiddleware(h.token))
		api.Get("/v1/indices", h.listSeries)
		api.Get("/v1/indices/{code}/final", h.final)
		api.Get("/v1/indices/{code}/series", h.series)
		api.Put("/v1/indices/{code}/observations", h.upsert)
		api.Post("/v1/indices/sync/{source}", h.runSync)
		api.Get("/v1/selic", h.selicList)
		api.Get("/v1/selic/latest", h.selicLatest)
		api.Get("/v1/selic/{date}", h.selicDate)
		api.Post("/v1/selic/import", h.importSelic)
		api.Get("/v1/operations/sync-runs", h.syncRuns)
		api.Post("/v1/operations/sync/{source}", h.runSync)
		api.Get("/v1/system/workers", h.workerStatus)
		api.Get("/v1/system/workers/runs", h.syncRuns)
		api.Post("/v1/system/workers/{source}/run", h.runSync)
	})
	return r
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": h.name})
}

func (h *Handler) listSeries(w http.ResponseWriter, r *http.Request) {
	group, err := parseIntDefault(r.URL.Query().Get("group"), 0)
	if err != nil || group < 0 || group > 6 {
		writeError(w, http.StatusBadRequest, "invalid_group", "group must be between 1 and 6")
		return
	}
	codes := splitCodes(r.URL.Query().Get("codes"))
	items, err := h.db.ListSeries(r.Context(), group, codes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog_failed", err.Error())
		return
	}
	if strings.EqualFold(r.URL.Query().Get("latest"), "true") {
		type item struct {
			domain.Series
			Latest []domain.ChartPoint `json:"latest"`
		}
		response := make([]item, 0, len(items))
		for _, series := range items {
			latest, err := h.db.Latest(r.Context(), series.Code, "")
			if err != nil {
				writeError(w, http.StatusInternalServerError, "latest_failed", err.Error())
				return
			}
			entry := item{Series: series, Latest: make([]domain.ChartPoint, 0, len(latest))}
			for _, point := range latest {
				entry.Latest = append(entry.Latest, toChartPoint(point))
			}
			response = append(response, entry)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": response})
		return
	}
	writeJSON(w, http.StatusOK, domain.SeriesList{Items: items})
}

func (h *Handler) final(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	series, err := h.db.GetSeries(r.Context(), code)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "series_not_found", "Series was not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "series_failed", err.Error())
		return
	}
	dimension := r.URL.Query().Get("dim")
	if dimension == "*" {
		dimension = ""
	}
	var latest []domain.Observation
	if asOf := r.URL.Query().Get("as_of"); asOf != "" {
		if err := validateDate(asOf); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", err.Error())
			return
		}
		latest, err = h.db.LatestAsOf(r.Context(), series.Code, dimension, asOf)
	} else {
		latest, err = h.db.Latest(r.Context(), series.Code, dimension)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "final_value_failed", err.Error())
		return
	}
	if len(latest) == 0 {
		writeError(w, http.StatusNotFound, "value_not_found", "No observation is available for this series")
		return
	}
	points := make([]domain.ChartPoint, 0, len(latest))
	for _, observation := range latest {
		points = append(points, toChartPoint(observation))
	}
	writeJSON(w, http.StatusOK, domain.FinalResponse{Series: series, Data: points, Warnings: freshnessWarnings(series, latest)})
}

func (h *Handler) series(w http.ResponseWriter, r *http.Request) {
	h.seriesWithNames(w, r, "from", "to", "limit")
}

func (h *Handler) seriesWithNames(w http.ResponseWriter, r *http.Request, fromName, toName, limitName string) {
	code := chi.URLParam(r, "code")
	series, err := h.db.GetSeries(r.Context(), code)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "series_not_found", "Series was not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "series_failed", err.Error())
		return
	}
	query := store.SeriesQuery{Dimension: r.URL.Query().Get("dim"), Resolution: r.URL.Query().Get("resolution")}
	if query.Dimension == "*" {
		query.Dimension = ""
	}
	query.From = firstNonEmpty(r.URL.Query().Get(fromName), r.URL.Query().Get("start"))
	query.To = firstNonEmpty(r.URL.Query().Get(toName), r.URL.Query().Get("end"))
	query.AsOf = r.URL.Query().Get("as_of")
	for _, date := range []string{query.From, query.To, query.AsOf} {
		if date != "" {
			if err := validateDate(date); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_date", err.Error())
				return
			}
		}
	}
	if query.From != "" && query.To != "" && query.From > query.To {
		writeError(w, http.StatusBadRequest, "invalid_range", "from must be on or before to")
		return
	}
	query.Limit, err = parseIntDefault(r.URL.Query().Get(limitName), 2000)
	if err != nil || query.Limit < 1 || query.Limit > 20000 {
		writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 20000")
		return
	}
	if raw := r.URL.Query().Get("last"); raw != "" {
		query.Limit, err = strconv.Atoi(raw)
		if err != nil || query.Limit < 1 || query.Limit > 20000 {
			writeError(w, http.StatusBadRequest, "invalid_last", "last must be between 1 and 20000")
			return
		}
	}
	points, err := h.db.Series(r.Context(), code, query)
	if err != nil {
		writeError(w, http.StatusBadRequest, "series_query_failed", err.Error())
		return
	}
	warnings := []string{}
	if len(points) == 0 {
		warnings = append(warnings, "no_observations")
	} else if isStale(series, latestPointDate(points)) {
		warnings = append(warnings, "latest_observation_may_be_stale")
	}
	writeJSON(w, http.StatusOK, domain.SeriesResponse{Series: series, Data: points, Warnings: warnings})
}

func (h *Handler) upsert(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if _, err := h.db.GetSeries(r.Context(), code); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "series_not_found", "Series was not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "series_failed", err.Error())
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	var input struct {
		Items []domain.ObservationInput `json:"items"`
	}
	if err := json.Unmarshal(body, &input); err != nil || len(input.Items) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_body", "Body must contain a non-empty items array")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	hash := sha256.Sum256(body)
	_, response, duplicate, err := h.db.IdempotentUpsert(r.Context(), key, "observation.upsert."+strings.ToLower(code), hex.EncodeToString(hash[:]), code, input.Items, "api")
	if err != nil {
		if strings.Contains(err.Error(), "different request") {
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
		} else {
			writeError(w, http.StatusBadRequest, "upsert_failed", err.Error())
		}
		return
	}
	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	w.Header().Set("Idempotent-Replay", strconv.FormatBool(duplicate))
	writeRawJSON(w, status, response)
}

func (h *Handler) importSelic(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 200 {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}
	var raw []byte
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		if err := r.ParseMultipartForm(20 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_multipart", err.Error())
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file_required", "A file field is required")
			return
		}
		defer file.Close()
		raw, err = io.ReadAll(io.LimitReader(file, 20<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "file_read_failed", err.Error())
			return
		}
	} else {
		var err error
		raw, err = io.ReadAll(io.LimitReader(r.Body, 20<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "file_read_failed", err.Error())
			return
		}
	}
	items, err := ingest.ParseSelicCSV(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_selic_csv", err.Error())
		return
	}
	hash := sha256.Sum256(raw)
	_, response, duplicate, err := h.db.IdempotentUpsert(r.Context(), key, "selic.csv.import", hex.EncodeToString(hash[:]), "selic_apurada", items, "csv_import")
	if err != nil {
		if strings.Contains(err.Error(), "different request") {
			writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
		} else {
			writeError(w, http.StatusBadRequest, "import_failed", err.Error())
		}
		return
	}
	w.Header().Set("Idempotent-Replay", strconv.FormatBool(duplicate))
	writeRawJSON(w, http.StatusOK, response)
}

func (h *Handler) selicList(w http.ResponseWriter, r *http.Request) {
	query := store.SeriesQuery{From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"), Limit: 100, Offset: 0}
	for _, date := range []string{query.From, query.To} {
		if date != "" {
			if err := validateDate(date); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_date", err.Error())
				return
			}
		}
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		query.Limit, _ = strconv.Atoi(raw)
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		query.Offset, _ = strconv.Atoi(raw)
	}
	if query.Limit < 1 || query.Limit > 1000 || query.Offset < 0 {
		writeError(w, http.StatusBadRequest, "invalid_pagination", "limit must be 1 to 1000 and offset must be non-negative")
		return
	}
	points, err := h.db.ObservationSeries(r.Context(), "selic_apurada", query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "selic_query_failed", err.Error())
		return
	}
	total, err := h.db.CountCurrent(r.Context(), "selic_apurada", query.From, query.To)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "selic_count_failed", err.Error())
		return
	}
	items := make([]map[string]any, 0, len(points))
	for i := len(points) - 1; i >= 0; i-- {
		items = append(items, selicPoint(points[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": query.Limit, "offset": query.Offset})
}

func (h *Handler) selicLatest(w http.ResponseWriter, r *http.Request) {
	latest, err := h.db.Latest(r.Context(), "selic_apurada", "")
	if err != nil || len(latest) == 0 {
		writeError(w, http.StatusNotFound, "selic_not_found", "No Selic observation is available")
		return
	}
	writeJSON(w, http.StatusOK, selicPoint(latest[0]))
}

func (h *Handler) selicDate(w http.ResponseWriter, r *http.Request) {
	date := chi.URLParam(r, "date")
	if err := validateDate(date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", err.Error())
		return
	}
	points, err := h.db.ObservationSeries(r.Context(), "selic_apurada", store.SeriesQuery{From: date, To: date, Limit: 1})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "selic_query_failed", err.Error())
		return
	}
	if len(points) == 0 {
		writeError(w, http.StatusNotFound, "selic_not_found", "No Selic observation is available for this date")
		return
	}
	writeJSON(w, http.StatusOK, selicPoint(points[0]))
}

func (h *Handler) syncRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := parseIntDefault(r.URL.Query().Get("limit"), 50)
	items, err := h.db.RecentRuns(r.Context(), r.URL.Query().Get("source"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sync_runs_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) workerStatus(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.RecentRuns(r.Context(), "", 20)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "worker_status_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workers": []string{"sgs", "focus", "tesouro", "b3", "selic"}, "runs": items})
}

func (h *Handler) runSync(w http.ResponseWriter, r *http.Request) {
	source := chi.URLParam(r, "source")
	var from, to *time.Time
	for name, target := range map[string]**time.Time{"from": &from, "to": &to} {
		raw := r.URL.Query().Get(name)
		if raw == "" {
			continue
		}
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", fmt.Sprintf("%s must be YYYY-MM-DD", name))
			return
		}
		*target = &parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	result, err := h.syncer.Run(ctx, source, ingest.SyncOptions{From: from, To: to})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func selicPoint(point domain.Observation) map[string]any {
	result := map[string]any{"reference_date": point.Date, "annual_rate": point.Value, "source": point.Source, "updated_at": point.RecordedAt}
	var attributes map[string]any
	if len(point.Attributes) > 0 {
		_ = json.Unmarshal(point.Attributes, &attributes)
	}
	for key, value := range attributes {
		result[key] = value
	}
	return result
}

func toChartPoint(item domain.Observation) domain.ChartPoint {
	return domain.ChartPoint{X: item.Date, Y: item.Value, Dimension: item.Dimension, Source: item.Source, Attributes: item.Attributes}
}

func freshnessWarnings(series domain.Series, observations []domain.Observation) []string {
	if len(observations) == 0 {
		return []string{"no_observations"}
	}
	if isStale(series, latestObservationDate(observations)) {
		return []string{"latest_observation_may_be_stale"}
	}
	return []string{}
}

func latestObservationDate(items []domain.Observation) string {
	latest := ""
	for _, item := range items {
		if item.Date > latest {
			latest = item.Date
		}
	}
	return latest
}

func latestPointDate(items []domain.ChartPoint) string {
	latest := ""
	for _, item := range items {
		if item.X > latest {
			latest = item.X
		}
	}
	return latest
}

func isStale(series domain.Series, latestDate string) bool {
	date, err := time.Parse("2006-01-02", latestDate)
	if err != nil {
		return false
	}
	threshold := 48 * time.Hour
	switch series.Frequency {
	case "weekly":
		threshold = 14 * 24 * time.Hour
	case "monthly":
		threshold = 60 * 24 * time.Hour
	case "quarterly":
		threshold = 180 * 24 * time.Hour
	}
	return time.Since(date) > threshold
}

func splitCodes(raw string) []string {
	values := strings.Split(raw, ",")
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			items = append(items, value)
		}
	}
	return items
}

func parseIntDefault(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validateDate(value string) error {
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("date %q must use YYYY-MM-DD", value)
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeRawJSON(w http.ResponseWriter, status int, value []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(value)
}
