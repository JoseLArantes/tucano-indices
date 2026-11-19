package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/JoseLArantes/tucano-indices/internal/domain"
	"github.com/JoseLArantes/tucano-indices/internal/store"
)

type fakeDatabase struct {
	series            []domain.Series
	observations      []domain.Observation
	chartPoints       []domain.ChartPoint
	runs              []domain.SyncRun
	pingErr           error
	listErr           error
	getErr            error
	latestErr         error
	latestAsOfErr     error
	seriesErr         error
	observationErr    error
	revisionsErr      error
	countErr          error
	upsertErr         error
	idempotentErr     error
	runsErr           error
	count             int64
	duplicate         bool
	idempotentPayload []byte
	lastGroup         int
	lastCodes         []string
	lastCode          string
	lastDimension     string
	lastAsOf          string
	lastQuery         store.SeriesQuery
	lastOperation     string
	lastPayloadHash   string
	lastInputs        []domain.ObservationInput
	lastSource        string
	lastRunSource     string
	lastRunLimit      int
	upsertCalls       int
	idempotentCalls   int
}

func newFakeDatabase() *fakeDatabase {
	now := time.Now().UTC()
	return &fakeDatabase{
		series:            []domain.Series{{Code: "ibov", Group: 4, Name: "Ibovespa", Unit: "pts", Frequency: "daily", Source: "b3_statistics:IBOVESPA", CreatedAt: now, UpdatedAt: now}},
		observations:      []domain.Observation{{SeriesCode: "ibov", Date: now.Format("2006-01-02"), Value: "130000.25", Revision: 1, IsCurrent: true, Source: "b3_statistics", RecordedAt: now}},
		chartPoints:       []domain.ChartPoint{{X: now.AddDate(0, 0, -1).Format("2006-01-02"), Y: "129000", Source: "b3"}, {X: now.Format("2006-01-02"), Y: "130000.25", Source: "b3"}},
		idempotentPayload: []byte(`{"observations_received":1,"observations_revised":1}`),
	}
}

func (db *fakeDatabase) Ping(context.Context) error { return db.pingErr }
func (db *fakeDatabase) ListSeries(_ context.Context, group int, codes []string) ([]domain.Series, error) {
	db.lastGroup, db.lastCodes = group, append([]string(nil), codes...)
	if db.listErr != nil {
		return nil, db.listErr
	}
	return db.series, nil
}
func (db *fakeDatabase) GetSeries(_ context.Context, code string) (domain.Series, error) {
	db.lastCode = code
	if db.getErr != nil {
		return domain.Series{}, db.getErr
	}
	for _, item := range db.series {
		if item.Code == strings.ToLower(strings.TrimSpace(code)) {
			return item, nil
		}
	}
	return domain.Series{}, pgx.ErrNoRows
}
func (db *fakeDatabase) Latest(_ context.Context, code, dimension string) ([]domain.Observation, error) {
	db.lastCode, db.lastDimension = code, dimension
	return db.observations, db.latestErr
}
func (db *fakeDatabase) LatestAsOf(_ context.Context, code, dimension, asOf string) ([]domain.Observation, error) {
	db.lastCode, db.lastDimension, db.lastAsOf = code, dimension, asOf
	return db.observations, db.latestAsOfErr
}
func (db *fakeDatabase) Series(_ context.Context, code string, query store.SeriesQuery) ([]domain.ChartPoint, error) {
	db.lastCode, db.lastQuery = code, query
	if db.seriesErr != nil {
		return nil, db.seriesErr
	}
	return db.chartPoints, nil
}
func (db *fakeDatabase) ObservationSeries(_ context.Context, code string, query store.SeriesQuery) ([]domain.Observation, error) {
	db.lastCode, db.lastQuery = code, query
	return db.observations, db.observationErr
}
func (db *fakeDatabase) Revisions(_ context.Context, code string, query store.SeriesQuery) ([]domain.Observation, error) {
	db.lastCode, db.lastQuery = code, query
	return db.observations, db.revisionsErr
}
func (db *fakeDatabase) CountCurrent(_ context.Context, code, from, to string) (int64, error) {
	db.lastCode, db.lastQuery.From, db.lastQuery.To = code, from, to
	return db.count, db.countErr
}
func (db *fakeDatabase) Upsert(_ context.Context, code string, inputs []domain.ObservationInput, source string) (int, error) {
	db.lastCode, db.lastInputs, db.lastSource = code, inputs, source
	db.upsertCalls++
	return len(inputs), db.upsertErr
}
func (db *fakeDatabase) IdempotentUpsert(_ context.Context, key, operation, payloadHash, code string, inputs []domain.ObservationInput, source string) (int, []byte, bool, error) {
	db.idempotentCalls++
	db.lastCode, db.lastOperation, db.lastPayloadHash = code, operation, payloadHash
	db.lastInputs, db.lastSource = inputs, source
	return len(inputs), db.idempotentPayload, db.duplicate, db.idempotentErr
}
func (db *fakeDatabase) RecentRuns(_ context.Context, source string, limit int) ([]domain.SyncRun, error) {
	db.lastRunSource, db.lastRunLimit = source, limit
	return db.runs, db.runsErr
}

func newHandler(db *fakeDatabase) http.Handler {
	return New(db, nil, "test-token", "test-service", []byte("openapi: 3.1.0\n")).Router()
}

func request(handler http.Handler, method, path, body string, authorized bool) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authorized {
		req.Header.Set("Authorization", "Bearer test-token")
	}
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestPublicHealthAndOpenAPIEndpoints(t *testing.T) {
	db := newFakeDatabase()
	handler := newHandler(db)
	response := request(handler, http.MethodGet, "/healthz", "", false)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("health response = %d %s", response.Code, response.Body.String())
	}
	db.pingErr = errors.New("database unavailable")
	response = request(handler, http.MethodGet, "/healthz", "", false)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable health status = %d", response.Code)
	}
	response = request(handler, http.MethodGet, "/openapi.yaml", "", false)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/yaml; charset=utf-8" || response.Body.String() != "openapi: 3.1.0\n" {
		t.Fatalf("OpenAPI response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	}
}

func TestListSeriesAuthenticationFilteringAndLatest(t *testing.T) {
	db := newFakeDatabase()
	handler := newHandler(db)
	response := request(handler, http.MethodGet, "/v1/indices", "", false)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.Code)
	}
	response = request(handler, http.MethodGet, "/v1/indices?group=4&codes=ibov,%20idiv", "", true)
	if response.Code != http.StatusOK || db.lastGroup != 4 || len(db.lastCodes) != 2 || db.lastCodes[1] != "idiv" {
		t.Fatalf("catalog response = %d; group=%d codes=%v", response.Code, db.lastGroup, db.lastCodes)
	}
	response = request(handler, http.MethodGet, "/v1/indices?latest=true", "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"latest"`) || !strings.Contains(response.Body.String(), `"x"`) {
		t.Fatalf("catalog latest response = %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/v1/indices?group=nope", "/v1/indices?group=7"} {
		if response := request(handler, http.MethodGet, path, "", true); response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, response.Code)
		}
	}
	db.listErr = errors.New("query failed")
	if response := request(handler, http.MethodGet, "/v1/indices", "", true); response.Code != http.StatusInternalServerError {
		t.Fatalf("list database error status = %d", response.Code)
	}
}

func TestFinalValueSupportsAsOfAndReportsErrors(t *testing.T) {
	db := newFakeDatabase()
	handler := newHandler(db)
	response := request(handler, http.MethodGet, "/v1/indices/ibov/final?as_of=2026-01-31&dim=*", "", true)
	if response.Code != http.StatusOK || db.lastAsOf != "2026-01-31" || db.lastDimension != "" {
		t.Fatalf("final response = %d; as_of=%q dim=%q", response.Code, db.lastAsOf, db.lastDimension)
	}
	var result domain.FinalResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || result.Data[0].Y != "130000.25" {
		t.Fatalf("unexpected final payload: %#v", result)
	}
	for _, path := range []string{"/v1/indices/ibov/final?as_of=2026-02-30", "/v1/indices/missing/final"} {
		want := http.StatusBadRequest
		if strings.Contains(path, "missing") {
			want = http.StatusNotFound
		}
		if response := request(handler, http.MethodGet, path, "", true); response.Code != want {
			t.Errorf("%s status = %d, want %d", path, response.Code, want)
		}
	}
	db.observations = nil
	if response := request(handler, http.MethodGet, "/v1/indices/ibov/final", "", true); response.Code != http.StatusNotFound {
		t.Fatalf("missing final value status = %d", response.Code)
	}
	db.observations = newFakeDatabase().observations
	db.latestAsOfErr = errors.New("read failed")
	if response := request(handler, http.MethodGet, "/v1/indices/ibov/final?as_of=2026-01-31", "", true); response.Code != http.StatusInternalServerError {
		t.Fatalf("latest-as-of error status = %d", response.Code)
	}
	db.getErr = errors.New("database error")
	if response := request(handler, http.MethodGet, "/v1/indices/ibov/final", "", true); response.Code != http.StatusInternalServerError {
		t.Fatalf("series lookup error status = %d", response.Code)
	}
}

func TestSeriesValidationPaginationAndWarnings(t *testing.T) {
	db := newFakeDatabase()
	handler := newHandler(db)
	response := request(handler, http.MethodGet, "/v1/indices/ibov/series?from=2026-01-01&to=2026-02-01&as_of=2026-02-01&last=10&dim=*&resolution=monthly", "", true)
	if response.Code != http.StatusOK || db.lastQuery.From != "2026-01-01" || db.lastQuery.To != "2026-02-01" || db.lastQuery.Limit != 10 || db.lastQuery.Dimension != "" || db.lastQuery.Resolution != "monthly" {
		t.Fatalf("series query/response = %d %#v", response.Code, db.lastQuery)
	}
	var result domain.SeriesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 2 || len(result.Warnings) != 0 {
		t.Fatalf("unexpected chart response: %#v", result)
	}
	for _, path := range []string{
		"/v1/indices/ibov/series?from=bad",
		"/v1/indices/ibov/series?from=2026-02-01&to=2026-01-01",
		"/v1/indices/ibov/series?limit=0",
		"/v1/indices/ibov/series?last=20001",
	} {
		if response := request(handler, http.MethodGet, path, "", true); response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, response.Code)
		}
	}
	db.chartPoints = []domain.ChartPoint{{X: "2020-01-01", Y: "1"}}
	response = request(handler, http.MethodGet, "/v1/indices/ibov/series", "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "latest_observation_may_be_stale") {
		t.Fatalf("stale warning response = %d %s", response.Code, response.Body.String())
	}
	db.chartPoints = nil
	response = request(handler, http.MethodGet, "/v1/indices/ibov/series", "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "no_observations") {
		t.Fatalf("empty series warning response = %d %s", response.Code, response.Body.String())
	}
	db.seriesErr = errors.New("query failed")
	if response := request(handler, http.MethodGet, "/v1/indices/ibov/series", "", true); response.Code != http.StatusBadRequest {
		t.Fatalf("series query error status = %d", response.Code)
	}
}

func TestLegacyRoutesAndRevisionPayload(t *testing.T) {
	db := newFakeDatabase()
	db.observations[0].IsCurrent = false
	db.observations = append(db.observations, domain.Observation{SeriesCode: "ibov", Date: db.observations[0].Date, Value: "130001", Revision: 2, IsCurrent: true, RecordedAt: time.Now()})
	handler := newHandler(db)
	response := request(handler, http.MethodGet, "/v1/market-metrics?latest=true", "", true)
	if response.Code != http.StatusOK || response.Header().Get("Deprecation") != "true" || !strings.Contains(response.Body.String(), `"code":"ibov"`) {
		t.Fatalf("legacy catalog response = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
	response = request(handler, http.MethodGet, "/v1/market-metrics/ibov?include_revisions=true&last=10", "", true)
	if response.Code != http.StatusOK || response.Header().Get("Deprecation") != "true" || !strings.Contains(response.Body.String(), `"is_current":false`) || !strings.Contains(response.Body.String(), `"is_current":true`) {
		t.Fatalf("legacy revisions response = %d %s", response.Code, response.Body.String())
	}
	if response := request(handler, http.MethodGet, "/v1/market-metrics/ibov?last=0", "", true); response.Code != http.StatusBadRequest {
		t.Fatalf("legacy invalid limit status = %d", response.Code)
	}
	db.revisionsErr = errors.New("revision query failed")
	if response := request(handler, http.MethodGet, "/v1/market-metrics/ibov?include_revisions=true", "", true); response.Code != http.StatusInternalServerError {
		t.Fatalf("legacy query error status = %d", response.Code)
	}
}

func TestObservationUpsertAuthorizationIdempotencyAndValidation(t *testing.T) {
	db := newFakeDatabase()
	handler := newHandler(db)
	body := `{"items":[{"date":"2026-01-01","value":"1.25"}]}`
	if response := request(handler, http.MethodPut, "/v1/indices/ibov/observations", body, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized upsert status = %d", response.Code)
	}
	for _, test := range []struct {
		name string
		path string
		body string
		key  string
		want int
	}{
		{name: "unknown series", path: "/v1/indices/missing/observations", body: body, key: "id", want: http.StatusNotFound},
		{name: "malformed body", path: "/v1/indices/ibov/observations", body: "{", key: "id", want: http.StatusBadRequest},
		{name: "empty items", path: "/v1/indices/ibov/observations", body: `{"items":[]}`, key: "id", want: http.StatusBadRequest},
		{name: "missing idempotency key", path: "/v1/indices/ibov/observations", body: body, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, test.path, strings.NewReader(test.body))
			req.Header.Set("Authorization", "Bearer test-token")
			if test.key != "" {
				req.Header.Set("Idempotency-Key", test.key)
			}
			handler.ServeHTTP(recorder, req)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/indices/ibov/observations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Idempotency-Key", "write-1")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusCreated || recorder.Header().Get("Idempotent-Replay") != "false" || db.lastOperation != "observation.upsert.ibov" || db.lastSource != "api" || len(db.lastInputs) != 1 {
		t.Fatalf("upsert response = %d %#v %s; operation=%s", recorder.Code, recorder.Header(), recorder.Body.String(), db.lastOperation)
	}
	db.duplicate = true
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/v1/indices/ibov/observations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Idempotency-Key", "write-1")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("replay response = %d %#v", recorder.Code, recorder.Header())
	}
	db.duplicate, db.idempotentErr = false, errors.New("idempotency key was already used with a different request")
	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/v1/indices/ibov/observations", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Idempotency-Key", "write-1")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("idempotency conflict status = %d", recorder.Code)
	}
}

func TestSelicImportAndReadEndpoints(t *testing.T) {
	db := newFakeDatabase()
	db.observations = []domain.Observation{
		{Date: "2026-01-01", Value: "10", Source: "bcb", RecordedAt: time.Now(), Attributes: json.RawMessage(`{"daily_factor":"1.0001"}`)},
		{Date: "2026-01-02", Value: "11", Source: "bcb", RecordedAt: time.Now()},
	}
	db.count = 42
	handler := newHandler(db)
	response := request(handler, http.MethodGet, "/v1/selic?from=2026-01-01&to=2026-01-31&limit=10&offset=2", "", true)
	if response.Code != http.StatusOK || db.lastCode != "selic_apurada" || db.lastQuery.Limit != 10 || db.lastQuery.Offset != 2 || db.count != 42 {
		t.Fatalf("Selic list response = %d %s query=%#v", response.Code, response.Body.String(), db.lastQuery)
	}
	if strings.Index(response.Body.String(), "2026-01-02") > strings.Index(response.Body.String(), "2026-01-01") {
		t.Fatalf("Selic rows were not returned newest-first: %s", response.Body.String())
	}
	if response := request(handler, http.MethodGet, "/v1/selic?limit=1001", "", true); response.Code != http.StatusBadRequest {
		t.Fatalf("Selic invalid pagination status = %d", response.Code)
	}
	if response := request(handler, http.MethodGet, "/v1/selic?from=nope", "", true); response.Code != http.StatusBadRequest {
		t.Fatalf("Selic invalid date status = %d", response.Code)
	}
	response = request(handler, http.MethodGet, "/v1/selic/latest", "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"daily_factor":"1.0001"`) {
		t.Fatalf("Selic latest response = %d %s", response.Code, response.Body.String())
	}
	response = request(handler, http.MethodGet, "/v1/selic/2026-01-02", "", true)
	if response.Code != http.StatusOK || db.lastQuery.From != "2026-01-02" {
		t.Fatalf("Selic date response = %d %s", response.Code, response.Body.String())
	}
	if response := request(handler, http.MethodGet, "/v1/selic/2026-02-30", "", true); response.Code != http.StatusBadRequest {
		t.Fatalf("Selic invalid path date status = %d", response.Code)
	}
	csvBody := "Data;Taxa (% a.a.)\n02/01/2024;11,25\n"
	if response := request(handler, http.MethodPost, "/v1/selic/import", csvBody, true); response.Code != http.StatusBadRequest {
		t.Fatalf("Selic import without idempotency key status = %d", response.Code)
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/selic/import", strings.NewReader(csvBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Idempotency-Key", "selic-csv-1")
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || db.lastOperation != "selic.csv.import" || db.lastCode != "selic_apurada" || len(db.lastInputs) != 1 || db.lastSource != "csv_import" {
		t.Fatalf("Selic import = %d %s operation=%q", recorder.Code, recorder.Body.String(), db.lastOperation)
	}
	if response := request(handler, http.MethodPost, "/v1/selic/import", "not,csv", true); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid Selic import status = %d", response.Code)
	}
}

func TestOperationsAndWorkerEndpoints(t *testing.T) {
	db := newFakeDatabase()
	db.runs = []domain.SyncRun{{ID: "r1", Source: "b3", Status: "succeeded"}}
	handler := newHandler(db)
	response := request(handler, http.MethodGet, "/v1/operations/sync-runs?source=b3&limit=4", "", true)
	if response.Code != http.StatusOK || db.lastRunSource != "b3" || db.lastRunLimit != 4 || !strings.Contains(response.Body.String(), "succeeded") {
		t.Fatalf("sync runs response = %d %s source=%q limit=%d", response.Code, response.Body.String(), db.lastRunSource, db.lastRunLimit)
	}
	response = request(handler, http.MethodGet, "/v1/system/workers", "", true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"workers"`) || !strings.Contains(response.Body.String(), `"selic"`) {
		t.Fatalf("worker status = %d %s", response.Code, response.Body.String())
	}
	if response := request(handler, http.MethodPost, "/v1/operations/sync/b3?from=invalid", "", true); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid manual sync date status = %d", response.Code)
	}
}

func TestHelperFunctions(t *testing.T) {
	if got := splitCodes(" IBOV, , IDIV "); len(got) != 2 || got[0] != "ibov" || got[1] != "idiv" {
		t.Fatalf("splitCodes() = %v", got)
	}
	if value, err := parseIntDefault("", 7); err != nil || value != 7 {
		t.Fatalf("parseIntDefault empty = %d, %v", value, err)
	}
	if _, err := parseIntDefault("bad", 7); err == nil {
		t.Fatal("parseIntDefault accepted non-integer")
	}
	if got := firstNonEmpty("", "  value ", "other"); got != "value" {
		t.Fatalf("firstNonEmpty() = %q", got)
	}
	if err := validateDate("2024-02-29"); err != nil {
		t.Fatalf("valid leap day rejected: %v", err)
	}
	if err := validateDate("2023-02-29"); err == nil {
		t.Fatal("invalid leap day accepted")
	}
	series := domain.Series{Frequency: "weekly"}
	if !isStale(series, "2020-01-01") || isStale(series, time.Now().UTC().Format("2006-01-02")) {
		t.Fatal("isStale returned unexpected result for weekly series")
	}
	if got := freshnessWarnings(series, nil); len(got) != 1 || got[0] != "no_observations" {
		t.Fatalf("freshnessWarnings(empty) = %v", got)
	}
	if got := toChartPoint(domain.Observation{Date: "2024-01-01", Value: "2", Dimension: "2025"}); got.X != "2024-01-01" || got.Y != "2" || got.Dimension != "2025" {
		t.Fatalf("toChartPoint() = %#v", got)
	}
}
