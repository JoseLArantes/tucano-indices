package ingest_test

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
	"github.com/JoseLArantes/tucano-indices/internal/ingest"
)

type fakeStore struct {
	series       []domain.Series
	listErr      error
	maxDate      time.Time
	maxDateErr   error
	upsertErr    error
	startErr     error
	finishErr    error
	acquireErr   error
	locked       bool
	acquireCalls int
	starts       int
	finishes     int
	upserts      int
	lastStatus   string
	lastRunError error
	lastInputs   []domain.ObservationInput
	lastCode     string
	lastSource   string
	lastFrom     time.Time
	lastTo       time.Time
	maxDateCalls int
}

func (s *fakeStore) ListSeries(context.Context, int, []string) ([]domain.Series, error) {
	return s.series, s.listErr
}
func (s *fakeStore) Latest(context.Context, string, string) ([]domain.Observation, error) {
	return nil, nil
}
func (s *fakeStore) MaxDate(_ context.Context, code, _ string) (time.Time, error) {
	s.maxDateCalls++
	s.lastCode = code
	return s.maxDate, s.maxDateErr
}
func (s *fakeStore) Upsert(_ context.Context, code string, inputs []domain.ObservationInput, source string) (int, error) {
	s.upserts++
	s.lastCode = code
	s.lastInputs = inputs
	s.lastSource = source
	if s.upsertErr != nil {
		return 0, s.upsertErr
	}
	return len(inputs), nil
}
func (s *fakeStore) StartRun(_ context.Context, source string, from, to time.Time) (string, error) {
	s.starts++
	s.lastCode = source
	s.lastFrom = from
	s.lastTo = to
	if s.startErr != nil {
		return "", s.startErr
	}
	return "run-id", nil
}
func (s *fakeStore) FinishRun(_ context.Context, _ string, status string, _, _ int, runErr error) error {
	s.finishes++
	s.lastStatus = status
	s.lastRunError = runErr
	return s.finishErr
}
func (s *fakeStore) AcquireSourceLock(context.Context, int64) (func(), bool, error) {
	s.acquireCalls++
	return func() {}, !s.locked, s.acquireErr
}

func newSyncer(store *fakeStore, cfg config.Config) *ingest.Syncer {
	return ingest.NewSyncer(nil, store, cfg)
}

func date(value string) time.Time {
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		panic(fmt.Sprintf("invalid test date %s: %v", value, err))
	}
	return parsed
}

func datePtr(value string) *time.Time {
	parsed := date(value)
	return &parsed
}

func TestRunValidationLockAndStoreFailures(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	syncer := newSyncer(store, config.Config{InitialYear: 2024})
	if _, err := syncer.Run(ctx, "invalid", ingest.SyncOptions{}); err == nil {
		t.Fatal("Run accepted unknown source")
	}
	if _, err := syncer.Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-02-01"), To: datePtr("2024-01-01")}); err == nil {
		t.Fatal("Run accepted inverted window")
	}
	if store.acquireCalls != 0 {
		t.Fatalf("invalid source/window acquired lock %d times", store.acquireCalls)
	}

	store.locked = true
	result, err := syncer.Run(ctx, "sgs", ingest.SyncOptions{})
	if err != nil || result.Status != "skipped" || store.starts != 0 {
		t.Fatalf("locked run = %#v, %v; starts=%d", result, err, store.starts)
	}

	for _, test := range []struct {
		name  string
		store *fakeStore
	}{
		{name: "acquire", store: &fakeStore{acquireErr: errors.New("acquire failed")}},
		{name: "start", store: &fakeStore{startErr: errors.New("start failed")}},
		{name: "finish", store: &fakeStore{finishErr: errors.New("finish failed")}},
		{name: "list", store: &fakeStore{listErr: errors.New("list failed")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newSyncer(test.store, config.Config{}).Run(ctx, "sgs", ingest.SyncOptions{}); err == nil {
				t.Fatalf("Run ignored %s failure", test.name)
			}
		})
	}

	store = &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}, upsertErr: errors.New("persist failed")}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"data":"02/01/2024","valor":"4,50"}]`)
	}))
	defer server.Close()
	if _, err := newSyncer(store, config.Config{SGSBaseURL: server.URL}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")}); err == nil {
		t.Fatal("Run ignored upsert failure")
	}
	store = &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}, maxDateErr: errors.New("max date failed")}
	if _, err := newSyncer(store, config.Config{}).Run(ctx, "sgs", ingest.SyncOptions{To: datePtr("2024-01-02")}); err == nil {
		t.Fatal("Run ignored MaxDate failure")
	}

	store = &fakeStore{}
	result, err = newSyncer(store, config.Config{}).Run(ctx, " SGS ", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err != nil || result.Status != "succeeded" || store.starts != 1 || store.finishes != 1 {
		t.Fatalf("normalized source run = %#v, %v; starts=%d finishes=%d", result, err, store.starts, store.finishes)
	}
}

func TestSGSFetchChunkingAndErrors(t *testing.T) {
	ctx := context.Background()
	var requests []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bcdata.sgs.433/dados" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != "tucano-indices/1.0" {
			t.Errorf("unexpected headers: %#v", r.Header)
		}
		requests = append(requests, r.URL.Query())
		if len(requests) == 1 {
			_, _ = io.WriteString(w, `[{"data":"02/01/2010","valor":"4,50"}]`)
			return
		}
		_, _ = io.WriteString(w, `[{"data":"02/01/2024","valor":"5,00"}]`)
	}))
	defer server.Close()
	store := &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	result, err := newSyncer(store, config.Config{SGSBaseURL: server.URL}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2010-01-01"), To: datePtr("2024-01-01")})
	if err != nil || result.PointsSeen != 2 || result.PointsStored != 2 || len(requests) != 2 {
		t.Fatalf("SGS run = %#v, %v; requests=%d", result, err, len(requests))
	}
	if requests[0].Get("dataInicial") != "01/01/2010" || requests[0].Get("dataFinal") != "31/12/2019" || requests[1].Get("dataInicial") != "01/01/2020" || requests[1].Get("dataFinal") != "01/01/2024" {
		t.Fatalf("unexpected SGS windows: %#v", requests)
	}
	if store.lastCode != "ipca" || store.lastSource != "sgs" || len(store.lastInputs) != 2 || store.lastInputs[0].Date != "2010-01-02" || store.lastInputs[1].Value != "5.00" {
		t.Fatalf("unexpected persisted SGS points: %#v", store.lastInputs)
	}

	for _, source := range []string{"bcb_sgs:nope", "bcb_sgs:0"} {
		store := &fakeStore{series: []domain.Series{{Code: "bad", Source: source}}}
		if _, err := newSyncer(store, config.Config{SGSBaseURL: server.URL}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")}); err == nil {
			t.Errorf("Run accepted SGS mapping %q", source)
		}
	}
	unmatched := &fakeStore{series: []domain.Series{{Code: "bad", Source: "bcb_sgs"}}}
	result, err = newSyncer(unmatched, config.Config{SGSBaseURL: server.URL}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err != nil || result.PointsSeen != 0 || len(result.Metrics) != 0 {
		t.Fatalf("unmatched SGS source = %#v, %v", result, err)
	}

	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "invalid json", body: `{`},
		{name: "invalid date", body: `[{"data":"bad","valor":"1"}]`},
		{name: "http error", body: "unavailable", status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			store := &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
			if _, err := newSyncer(store, config.Config{SGSBaseURL: server.URL}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")}); err == nil {
				t.Fatal("Run ignored SGS failure")
			}
			if store.lastStatus != "failed" || store.lastRunError == nil {
				t.Fatalf("failure was not recorded: %#v", store)
			}
		})
	}

	emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"data":"02/01/2024","valor":""}]`)
	}))
	defer emptyServer.Close()
	store = &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	result, err = newSyncer(store, config.Config{SGSBaseURL: emptyServer.URL}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err != nil || result.PointsSeen != 0 || len(store.lastInputs) != 0 {
		t.Fatalf("empty SGS value = %#v, %v; inputs=%#v", result, err, store.lastInputs)
	}

	invalidURL := &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	if _, err := newSyncer(invalidURL, config.Config{SGSBaseURL: ": invalid"}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")}); err == nil {
		t.Fatal("Run accepted an invalid SGS URL")
	}
}

func TestFocusFetchAndErrors(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ExpectativasMercadoAnuais" {
			t.Errorf("path = %q", r.URL.Path)
		}
		query := r.URL.Query()
		if !strings.Contains(query.Get("$filter"), "Indicador eq 'IPCA'") || query.Get("$select") != "Indicador,Data,DataReferencia,Mediana,baseCalculo" || query.Get("$orderby") != "Data asc" || query.Get("$format") != "json" || query.Get("$top") != "100000" {
			t.Errorf("unexpected Focus query: %v", query)
		}
		_, _ = io.WriteString(w, `{"value":[{"Data":"2024-01-02","DataReferencia":"2025","Mediana":4.25},{"Data":"2024-01-03","DataReferencia":"","Mediana":4.5}]}`)
	}))
	defer server.Close()
	store := &fakeStore{series: []domain.Series{{Code: "focus_ipca", Source: "bcb_focus"}}}
	result, err := newSyncer(store, config.Config{FocusBaseURL: server.URL}).Run(ctx, "focus", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-31")})
	if err != nil || result.PointsSeen != 1 || len(store.lastInputs) != 1 || store.lastInputs[0].Dimension != "2025" || store.lastInputs[0].Value != "4.2500000000" {
		t.Fatalf("Focus run = %#v, %v; inputs=%#v", result, err, store.lastInputs)
	}

	unsupported := &fakeStore{series: []domain.Series{{Code: "unknown", Source: "bcb_focus"}}}
	if _, err := newSyncer(unsupported, config.Config{FocusBaseURL: server.URL}).Run(ctx, "focus", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-31")}); err == nil {
		t.Fatal("Run accepted unsupported Focus series")
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{`},
		{name: "invalid date", body: `{"value":[{"Data":"bad","DataReferencia":"2025","Mediana":4.25}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			store := &fakeStore{series: []domain.Series{{Code: "focus_ipca", Source: "bcb_focus"}}}
			if _, err := newSyncer(store, config.Config{FocusBaseURL: server.URL}).Run(ctx, "focus", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-31")}); err == nil {
				t.Fatal("Run ignored Focus failure")
			}
		})
	}

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer errorServer.Close()
	store = &fakeStore{series: []domain.Series{{Code: "focus_ipca", Source: "bcb_focus"}}}
	if _, err := newSyncer(store, config.Config{FocusBaseURL: errorServer.URL}).Run(ctx, "focus", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-31")}); err == nil {
		t.Fatal("Run ignored Focus HTTP failure")
	}
}

func TestTesouroDirectCSVAndErrors(t *testing.T) {
	ctx := context.Background()
	csvBody := "\ufeffTipo Título;Data Vencimento;Data Base;Taxa Compra Manhã;Taxa Venda Manhã\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2035;02/01/2024;5,10;5,30\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2040;03/01/2024;5,00;\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2045;03/01/2024;;5,20\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2050;03/01/2024;6,00;\n" +
		"Tesouro Prefixado;01/01/2030;02/01/2024;10,00;11,00\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2035;01/01/2024;5,00;5,20\n" +
		"Tesouro IPCA+ com Juros Semestrais;bad;03/01/2024;5,00;5,20\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2035;bad;5,00;5,20\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2035;03/01/2024;;\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rates.csv" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, csvBody)
	}))
	defer server.Close()
	store := &fakeStore{series: []domain.Series{{Code: "ntnb", Source: "tesouro"}}}
	result, err := newSyncer(store, config.Config{TesouroCSVURL: server.URL + "/rates.csv"}).Run(ctx, "tesouro", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-03")})
	if err != nil || result.PointsSeen != 4 || len(store.lastInputs) != 4 {
		t.Fatalf("Tesouro run = %#v, %v; inputs=%#v", result, err, store.lastInputs)
	}
	want := []struct {
		value     string
		dimension string
	}{
		{value: "5.2000000000", dimension: "2035"},
		{value: "5.0000000000", dimension: "2040"},
		{value: "5.2000000000", dimension: "2045"},
		{value: "6.0000000000", dimension: "2050"},
	}
	for i, expected := range want {
		if store.lastInputs[i].Value != expected.value || store.lastInputs[i].Dimension != expected.dimension || store.lastInputs[i].Source != "tesouro_transparente" {
			t.Fatalf("input %d = %#v, want %#v", i, store.lastInputs[i], expected)
		}
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "empty", body: ""},
		{name: "missing columns", body: "Tipo Título;Data Base\nTesouro IPCA+ com Juros Semestrais;02/01/2024\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			store := &fakeStore{series: []domain.Series{{Code: "ntnb", Source: "tesouro"}}}
			if _, err := newSyncer(store, config.Config{TesouroCSVURL: server.URL + "/rates.csv"}).Run(ctx, "tesouro", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-03")}); err == nil {
				t.Fatal("Run ignored invalid Tesouro CSV")
			}
		})
	}

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer errorServer.Close()
	store = &fakeStore{series: []domain.Series{{Code: "ntnb", Source: "tesouro"}}}
	if _, err := newSyncer(store, config.Config{TesouroCSVURL: errorServer.URL + "/rates.csv"}).Run(ctx, "tesouro", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-03")}); err == nil {
		t.Fatal("Run ignored Tesouro HTTP failure")
	}
}

func TestTesouroCKANDiscovery(t *testing.T) {
	ctx := context.Background()
	csvBody := "Tipo Título;Data Vencimento;Data Base;Taxa Compra Manhã;Taxa Venda Manhã\n" +
		"Tesouro IPCA+ com Juros Semestrais;15/05/2035;02/01/2024;5,10;5,30\n"
	var ckanCalls, csvCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ckan":
			ckanCalls++
			_, _ = io.WriteString(w, `{"success":true,"result":{"resources":[{"format":"CSV","url":"`+serverURL(r)+`/rates.csv"}]}}`)
		case "/rates.csv":
			csvCalls++
			_, _ = io.WriteString(w, csvBody)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	store := &fakeStore{series: []domain.Series{{Code: "ntnb", Source: "tesouro"}}}
	result, err := newSyncer(store, config.Config{TesouroCKANURL: server.URL + "/ckan"}).Run(ctx, "tesouro", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err != nil || result.PointsSeen != 1 || ckanCalls != 1 || csvCalls != 1 {
		t.Fatalf("CKAN run = %#v, %v; ckan=%d csv=%d", result, err, ckanCalls, csvCalls)
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}

func TestB3FetchAndErrors(t *testing.T) {
	ctx := context.Background()
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
		var request struct {
			Index    string `json:"index"`
			Language string `json:"language"`
			Year     string `json:"year"`
		}
		if err := json.Unmarshal(payload, &request); err != nil {
			t.Errorf("decode request JSON: %v", err)
		}
		years = append(years, request.Year)
		if request.Index != "IBOVESPA" || request.Language != "pt-br" {
			t.Errorf("unexpected B3 request: %#v", request)
		}
		_, _ = io.WriteString(w, `{"results":[{"day":"2","rateValue1":"123.456,78","rateValue2":"1.234,50"},{"day":31,"rateValue2":"9,90"},{"day":true,"rateValue1":"1"},{"day":32,"rateValue1":"1"}]}`)
	}))
	defer server.Close()
	store := &fakeStore{series: []domain.Series{{Code: "ibov", Source: "b3_statistics:IBOVESPA"}}}
	result, err := newSyncer(store, config.Config{B3BaseURL: server.URL}).Run(ctx, "b3", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2025-02-28")})
	if err != nil || result.PointsSeen != 4 || len(years) != 2 || years[0] != "2024" || years[1] != "2025" {
		t.Fatalf("B3 run = %#v, %v; years=%v", result, err, years)
	}
	if len(store.lastInputs) != 4 || store.lastInputs[0].Date != "2024-01-02" || store.lastInputs[0].Value != "123456.78" || store.lastInputs[1].Date != "2024-02-02" || store.lastInputs[2].Date != "2025-01-02" || store.lastInputs[3].Date != "2025-02-02" {
		t.Fatalf("unexpected B3 inputs: %#v", store.lastInputs)
	}

	for _, source := range []string{"b3_statistics:IBOVESPA:extra"} {
		store := &fakeStore{series: []domain.Series{{Code: "ibov", Source: source}}}
		if _, err := newSyncer(store, config.Config{B3BaseURL: server.URL}).Run(ctx, "b3", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-01")}); err == nil {
			t.Errorf("Run accepted B3 mapping %q", source)
		}
	}
	unmatched := &fakeStore{series: []domain.Series{{Code: "ibov", Source: "b3_statistics"}}}
	result, err = newSyncer(unmatched, config.Config{B3BaseURL: server.URL}).Run(ctx, "b3", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-01")})
	if err != nil || result.PointsSeen != 0 || len(result.Metrics) != 0 {
		t.Fatalf("unmatched B3 source = %#v, %v", result, err)
	}

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{`},
		{name: "invalid value", body: `{"results":[{"day":"2","rateValue1":"not-a-number"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			store := &fakeStore{series: []domain.Series{{Code: "ibov", Source: "b3_statistics:IBOVESPA"}}}
			if _, err := newSyncer(store, config.Config{B3BaseURL: server.URL}).Run(ctx, "b3", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-31")}); err == nil {
				t.Fatal("Run ignored B3 failure")
			}
		})
	}

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer errorServer.Close()
	store = &fakeStore{series: []domain.Series{{Code: "ibov", Source: "b3_statistics:IBOVESPA"}}}
	if _, err := newSyncer(store, config.Config{B3BaseURL: errorServer.URL}).Run(ctx, "b3", ingest.SyncOptions{From: datePtr("2024-01-01"), To: datePtr("2024-01-31")}); err == nil {
		t.Fatal("Run ignored B3 HTTP failure")
	}

	years = nil
	store = &fakeStore{series: []domain.Series{{Code: "ibov", Source: "b3_statistics:IBOVESPA"}}}
	result, err = newSyncer(store, config.Config{B3BaseURL: server.URL}).Run(ctx, "b3", ingest.SyncOptions{To: datePtr("1998-01-31")})
	if err != nil || result.PointsSeen != 1 || len(years) != 1 || years[0] != "1998" {
		t.Fatalf("B3 initial year run = %#v, %v; years=%v", result, err, years)
	}

	store = &fakeStore{series: []domain.Series{{Code: "ibov", Source: "b3_statistics:IBOVESPA"}}, maxDate: date("2024-02-01")}
	result, err = newSyncer(store, config.Config{B3BaseURL: server.URL}).Run(ctx, "b3", ingest.SyncOptions{To: datePtr("2024-02-28")})
	if err != nil || result.PointsSeen != 1 || store.maxDateCalls != 1 {
		t.Fatalf("B3 incremental run = %#v, %v; maxDateCalls=%d", result, err, store.maxDateCalls)
	}
}

func TestSelicFetchPaginationAndErrors(t *testing.T) {
	ctx := context.Background()
	var pages []string
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("User-Agent") != "tucano-indices/selic-sync" {
			t.Errorf("unexpected NovoSelic request: %s %#v", r.Method, r.Header)
		}
		if r.URL.Query().Get("token") != "x" {
			t.Errorf("missing existing query string: %s", r.URL.String())
		}
		var request struct {
			From string `json:"dataInicial"`
			To   string `json:"dataFinal"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.From != "02/01/2024" || request.To != "02/01/2024" {
			t.Errorf("unexpected NovoSelic range: %#v", request)
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "1" {
			_, _ = io.WriteString(w, `{"totalItems":2,"registros":[{"dataCotacao":"02/01/2024","taxaAnual":11.25,"fatorDiario":1.0004,"qtdOperacoes":25},{"dataCotacao":"03/01/2024","taxaAnual":11.30,"qtdOperacoes":30}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"totalItems":2,"registros":[]}`)
	}))
	defer server.Close()
	store := &fakeStore{series: []domain.Series{{Code: "selic_apurada", Source: "bcb_novoselic"}, {Code: "other", Source: "bcb_novoselic"}}}
	result, err := newSyncer(store, config.Config{SelicSearchURL: server.URL + "?token=x"}).Run(ctx, "selic", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err != nil || result.PointsSeen != 2 || len(store.lastInputs) != 2 || requests != 2 || len(pages) != 2 {
		t.Fatalf("Selic run = %#v, %v; requests=%d pages=%v", result, err, requests, pages)
	}
	if result.Metrics == nil || len(result.Metrics) != 1 || result.Metrics[0] != "selic_apurada" || store.lastCode != "selic_apurada" || store.lastSource != "selic" {
		t.Fatalf("unexpected Selic metrics: %#v", result.Metrics)
	}
	var attrs map[string]any
	if err := json.Unmarshal(store.lastInputs[0].Attributes, &attrs); err != nil || attrs["operations_count"] != float64(25) || attrs["daily_factor"] != "1.0004000000" {
		t.Fatalf("unexpected Selic attributes: %#v, %v", attrs, err)
	}

	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{name: "errors", body: `{"erros":[{"mensagem":"invalid range"}]}`},
		{name: "invalid date", body: `{"registros":[{"dataCotacao":"bad","taxaAnual":1}]}`},
		{name: "invalid json", body: `{`},
		{name: "http error", body: "unavailable", status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.status != 0 {
					w.WriteHeader(test.status)
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			store := &fakeStore{series: []domain.Series{{Code: "selic_apurada", Source: "bcb_novoselic"}}}
			if _, err := newSyncer(store, config.Config{SelicSearchURL: server.URL}).Run(ctx, "selic", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")}); err == nil {
				t.Fatal("Run ignored Selic failure")
			}
		})
	}

	invalidURL := &fakeStore{series: []domain.Series{{Code: "selic_apurada", Source: "bcb_novoselic"}}}
	if _, err := newSyncer(invalidURL, config.Config{SelicSearchURL: ": invalid"}).Run(ctx, "selic", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")}); err == nil {
		t.Fatal("Run accepted an invalid Selic URL")
	}
}

func TestSelicPagination(t *testing.T) {
	ctx := context.Background()
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "1" {
			records := make([]string, 100)
			for i := range records {
				records[i] = fmt.Sprintf(`{"dataCotacao":"02/01/2024","taxaAnual":%d}`, i+1)
			}
			_, _ = io.WriteString(w, `{"totalItems":101,"registros":[`+strings.Join(records, ",")+`]}`)
			return
		}
		_, _ = io.WriteString(w, `{"totalItems":101,"registros":[{"dataCotacao":"03/01/2024","taxaAnual":101}]}`)
	}))
	defer server.Close()
	store := &fakeStore{series: []domain.Series{{Code: "selic_apurada", Source: "bcb_novoselic"}}}
	result, err := newSyncer(store, config.Config{SelicSearchURL: server.URL}).Run(ctx, "selic", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-03")})
	if err != nil || result.PointsSeen != 101 || len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Fatalf("Selic pagination = %#v, %v; pages=%v", result, err, pages)
	}
}

func TestRunWindowSelection(t *testing.T) {
	ctx := context.Background()
	var windows []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		windows = append(windows, r.URL.Query())
		_, _ = io.WriteString(w, `[{"data":"02/01/2024","valor":"4,50"}]`)
	}))
	defer server.Close()

	store := &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}, maxDate: date("2024-01-10")}
	if _, err := newSyncer(store, config.Config{SGSBaseURL: server.URL}).Run(ctx, "sgs", ingest.SyncOptions{To: datePtr("2024-01-31")}); err != nil {
		t.Fatal(err)
	}
	if windows[0].Get("dataInicial") != "05/01/2024" || windows[0].Get("dataFinal") != "31/01/2024" || store.maxDateCalls != 1 {
		t.Fatalf("latest-based window = %v; maxDateCalls=%d", windows[0], store.maxDateCalls)
	}

	windows = nil
	store = &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	if _, err := newSyncer(store, config.Config{SGSBaseURL: server.URL, InitialYear: 2022}).Run(ctx, "sgs", ingest.SyncOptions{To: datePtr("2024-01-31")}); err != nil {
		t.Fatal(err)
	}
	if windows[0].Get("dataInicial") != "01/01/2022" {
		t.Fatalf("initial-year window = %v", windows[0])
	}

	windows = nil
	store = &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}, maxDate: date("2020-01-01")}
	if _, err := newSyncer(store, config.Config{SGSBaseURL: server.URL}).Run(ctx, "sgs", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")}); err != nil {
		t.Fatal(err)
	}
	if windows[0].Get("dataInicial") != "02/01/2024" || store.maxDateCalls != 0 {
		t.Fatalf("explicit window = %v; maxDateCalls=%d", windows[0], store.maxDateCalls)
	}
}

func TestRunAllAggregationAndPartialFailure(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{}
	result, err := newSyncer(store, config.Config{}).Run(ctx, "all", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err != nil || result.Source != "all" || result.Status != "succeeded" || store.starts != 5 || store.finishes != 5 {
		t.Fatalf("aggregate run = %#v, %v; starts=%d finishes=%d", result, err, store.starts, store.finishes)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()
	store = &fakeStore{series: []domain.Series{{Code: "ipca", Source: "bcb_sgs:433"}}}
	result, err = newSyncer(store, config.Config{SGSBaseURL: server.URL}).Run(ctx, "all", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err == nil || result.Status != "failed" || !strings.Contains(result.Error, "sgs") || store.starts != 5 {
		t.Fatalf("partial aggregate = %#v, %v; starts=%d", result, err, store.starts)
	}

	store = &fakeStore{listErr: errors.New("catalog failed")}
	result, err = newSyncer(store, config.Config{}).Run(ctx, "all", ingest.SyncOptions{From: datePtr("2024-01-02"), To: datePtr("2024-01-02")})
	if err == nil || result.Status != "failed" || store.starts != 5 || store.finishes != 5 {
		t.Fatalf("catalog aggregate failure = %#v, %v; starts=%d finishes=%d", result, err, store.starts, store.finishes)
	}
}

func TestParseSelicCSV(t *testing.T) {
	fixture := "\ufeffRelatório BCB\r\nData;Taxa (% a.a.);Fator diário;Financeiro (R$);Operações;Taxa média;Taxa mediana;Taxa modal;Desvio padrão;Índice de curtose\r\n02/01/2024;11,25;1,0004;1.234,50;1200;11,20;11,25;11,25;0,10;2,00\r\n2024-01-03;10,50;;;;abc;;;;\r\ninvalid;12,00;;;;;;;;\r\n"
	items, err := ingest.ParseSelicCSV([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(items))
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
	if items[1].Date != "2024-01-03" || items[1].Value != "10.5000000000" {
		t.Fatalf("unexpected ISO-date point: %#v", items[1])
	}
	var secondAttributes map[string]any
	if err := json.Unmarshal(items[1].Attributes, &secondAttributes); err != nil {
		t.Fatal(err)
	}
	if _, ok := secondAttributes["operations_count"]; ok {
		t.Fatalf("non-integer operations count should be omitted: %#v", secondAttributes)
	}

	for _, raw := range []string{"", "title;description\n", "Data;Taxa (% a.a.)\ninvalid;12,5\n", "Data;Fator diário\n02/01/2024;1,0\n", "Taxa (% a.a.);Fator diário\n12,5;1,0\n"} {
		if _, err := ingest.ParseSelicCSV([]byte(raw)); err == nil {
			t.Errorf("ParseSelicCSV(%q) succeeded, want error", raw)
		}
	}
}
