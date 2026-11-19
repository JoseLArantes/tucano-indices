package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseLArantes/tucano-indices/internal/config"
	"github.com/JoseLArantes/tucano-indices/internal/httpapi"
	"github.com/JoseLArantes/tucano-indices/internal/ingest"
	"github.com/JoseLArantes/tucano-indices/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(2)
	}
	if cfg.APIToken == "" {
		logger.Error("load configuration", "error", "API_TOKEN is required")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		logger.Error("parse database URL", "error", err)
		os.Exit(1)
	}
	poolConfig.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		logger.Error("ping database", "error", err)
		os.Exit(1)
	}
	db := store.New(pool)
	syncer := ingest.NewSyncer(pool, db, cfg)
	openAPIPath := strings.TrimSpace(os.Getenv("OPENAPI_SPEC_PATH"))
	if openAPIPath == "" {
		openAPIPath = "api/openapi.yaml"
	}
	openAPI, err := os.ReadFile(openAPIPath)
	if err != nil {
		logger.Error("read OpenAPI specification", "path", openAPIPath, "error", err)
		os.Exit(1)
	}
	handler := httpapi.New(db, syncer, cfg.APIToken, cfg.Name, openAPI)
	server := &http.Server{Addr: cfg.HTTPAddr, Handler: handler.Router(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("indices API listening", "address", cfg.HTTPAddr, "environment", cfg.Environment)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve API", "error", err)
		os.Exit(1)
	}
}
