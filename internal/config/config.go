package config

import (
	"errors"
	"os"
	"strings"
	"time"
)

type Config struct {
	Name           string
	Environment    string
	PublicURL      string
	HTTPAddr       string
	DatabaseURL    string
	APIToken       string
	SGSBaseURL     string
	FocusBaseURL   string
	TesouroCSVURL  string
	TesouroCKANURL string
	B3BaseURL      string
	SelicSearchURL string
	HTTPTimeout    time.Duration
	InitialYear    int
}

func Load() (Config, error) {
	cfg := Config{
		Name:           env("APP_NAME", "tucano-indices"),
		Environment:    env("APP_ENV", "development"),
		PublicURL:      env("APP_PUBLIC_URL", "http://localhost:8011"),
		HTTPAddr:       env("HTTP_ADDR", ":8080"),
		DatabaseURL:    strings.TrimSpace(os.Getenv("DATABASE_URL")),
		APIToken:       env("API_TOKEN", strings.TrimSpace(os.Getenv("BEARER_TOKEN"))),
		SGSBaseURL:     env("INDICES_SGS_BASE_URL", "https://api.bcb.gov.br/dados/serie"),
		FocusBaseURL:   env("INDICES_FOCUS_BASE_URL", "https://olinda.bcb.gov.br/olinda/servico/Expectativas/versao/v1/odata"),
		TesouroCSVURL:  strings.TrimSpace(os.Getenv("INDICES_TESOURO_CSV_URL")),
		TesouroCKANURL: strings.TrimSpace(os.Getenv("INDICES_TESOURO_CKAN_URL")),
		B3BaseURL:      env("INDICES_B3_BASE_URL", "https://sistemaswebb3-listados.b3.com.br/indexStatisticsProxy/IndexCall"),
		SelicSearchURL: env("INDICES_SELIC_SEARCH_URL", "https://www3.bcb.gov.br/novoselic/rest/taxaSelicApurada/pub/search"),
		HTTPTimeout:    45 * time.Second,
		InitialYear:    2000,
	}
	if raw := strings.TrimSpace(os.Getenv("INDICES_HTTP_TIMEOUT")); raw != "" {
		if timeout, err := time.ParseDuration(raw); err == nil && timeout > 0 {
			cfg.HTTPTimeout = timeout
		}
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
