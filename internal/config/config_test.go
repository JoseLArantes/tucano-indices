package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", " ")
	if _, err := Load(); err == nil || err.Error() != "DATABASE_URL is required" {
		t.Fatalf("Load() error = %v, want required DATABASE_URL", err)
	}
}

func TestLoadUsesDefaultsAndBearerFallback(t *testing.T) {
	t.Setenv("DATABASE_URL", " postgres://example/db ")
	t.Setenv("API_TOKEN", " ")
	t.Setenv("BEARER_TOKEN", " fallback-token ")
	t.Setenv("INDICES_HTTP_TIMEOUT", "invalid")
	cfg, err := Load()
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
}

func TestLoadAcceptsValidOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example/db")
	t.Setenv("API_TOKEN", " primary ")
	t.Setenv("BEARER_TOKEN", "fallback")
	t.Setenv("APP_NAME", " test-service ")
	t.Setenv("HTTP_ADDR", "127.0.0.1:9000")
	t.Setenv("INDICES_HTTP_TIMEOUT", "1500ms")
	t.Setenv("INDICES_TESOURO_CSV_URL", "https://example.test/rates.csv")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIToken != "primary" || cfg.Name != "test-service" || cfg.HTTPAddr != "127.0.0.1:9000" {
		t.Fatalf("overrides were not applied: %#v", cfg)
	}
	if cfg.HTTPTimeout != 1500*time.Millisecond || cfg.TesouroCSVURL != "https://example.test/rates.csv" {
		t.Fatalf("source/timeout overrides were not applied: %#v", cfg)
	}
}
