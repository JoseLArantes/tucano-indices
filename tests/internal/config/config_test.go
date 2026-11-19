package config_test

import (
	"testing"
	"time"

	"github.com/JoseLArantes/tucano-indices/internal/config"
)

var configEnv = []string{
	"DATABASE_URL", "API_TOKEN", "BEARER_TOKEN", "APP_NAME", "APP_PUBLIC_URL",
	"APP_ENV", "HTTP_ADDR", "INDICES_HTTP_TIMEOUT", "INDICES_INITIAL_YEAR",
	"INDICES_SGS_BASE_URL", "INDICES_FOCUS_BASE_URL", "INDICES_TESOURO_CKAN_URL",
	"INDICES_TESOURO_CSV_URL", "INDICES_B3_BASE_URL", "INDICES_SELIC_SEARCH_URL",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range configEnv {
		t.Setenv(key, "")
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", " ")
	if _, err := config.Load(); err == nil || err.Error() != "DATABASE_URL is required" {
		t.Fatalf("Load() error = %v, want required DATABASE_URL", err)
	}
}

func TestLoadUsesDefaultsAndBearerFallback(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", " postgres://example/db ")
	t.Setenv("API_TOKEN", " ")
	t.Setenv("BEARER_TOKEN", " fallback-token ")
	t.Setenv("INDICES_HTTP_TIMEOUT", "invalid")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://example/db" || cfg.APIToken != "fallback-token" {
		t.Fatalf("unexpected required values: %#v", cfg)
	}
	if cfg.Name != "tucano-indices" || cfg.HTTPAddr != ":8080" || cfg.InitialYear != 2000 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.HTTPTimeout != 45*time.Second {
		t.Fatalf("HTTPTimeout = %v, want 45s", cfg.HTTPTimeout)
	}
	if cfg.SGSBaseURL == "" || cfg.FocusBaseURL == "" || cfg.B3BaseURL == "" || cfg.SelicSearchURL == "" {
		t.Fatalf("source defaults are missing: %#v", cfg)
	}
	if cfg.PublicURL != "http://localhost:8011" || cfg.Environment != "development" {
		t.Fatalf("unexpected application defaults: %#v", cfg)
	}
}

func TestLoadAcceptsValidOverrides(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("API_TOKEN", " primary ")
	t.Setenv("BEARER_TOKEN", "fallback")
	t.Setenv("APP_NAME", " test-service ")
	t.Setenv("APP_PUBLIC_URL", " https://indices.example ")
	t.Setenv("APP_ENV", " production ")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9000")
	t.Setenv("INDICES_HTTP_TIMEOUT", "1500ms")
	t.Setenv("INDICES_SGS_BASE_URL", "https://sgs.example")
	t.Setenv("INDICES_FOCUS_BASE_URL", "https://focus.example")
	t.Setenv("INDICES_TESOURO_CKAN_URL", "https://ckan.example")
	t.Setenv("INDICES_TESOURO_CSV_URL", "https://example.test/rates.csv")
	t.Setenv("INDICES_B3_BASE_URL", "https://b3.example")
	t.Setenv("INDICES_SELIC_SEARCH_URL", "https://selic.example")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "primary" || cfg.Name != "test-service" || cfg.HTTPAddr != "127.0.0.1:9000" {
		t.Fatalf("overrides were not applied: %#v", cfg)
	}
	if cfg.PublicURL != "https://indices.example" || cfg.Environment != "production" || cfg.InitialYear != 2000 {
		t.Fatalf("application overrides were not applied: %#v", cfg)
	}
	if cfg.HTTPTimeout != 1500*time.Millisecond || cfg.TesouroCSVURL != "https://example.test/rates.csv" {
		t.Fatalf("source/timeout overrides were not applied: %#v", cfg)
	}
	if cfg.SGSBaseURL != "https://sgs.example" || cfg.FocusBaseURL != "https://focus.example" || cfg.TesouroCKANURL != "https://ckan.example" || cfg.B3BaseURL != "https://b3.example" || cfg.SelicSearchURL != "https://selic.example" {
		t.Fatalf("source overrides were not applied: %#v", cfg)
	}
}

func TestLoadPrefersAPITokenOverBearerToken(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("API_TOKEN", "api-token")
	t.Setenv("BEARER_TOKEN", "bearer-token")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "api-token" {
		t.Fatalf("APIToken = %q, want api-token", cfg.APIToken)
	}
}
