package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseLArantes/tucano-indices/internal/config"
	"github.com/JoseLArantes/tucano-indices/internal/ingest"
	"github.com/JoseLArantes/tucano-indices/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	source := flag.String("source", env("INDICES_SYNC_SOURCE", "all"), "sync source: all, sgs, focus, tesouro, b3, or selic")
	fromFlag := flag.String("from", "", "optional YYYY-MM-DD start date")
	toFlag := flag.String("to", "", "optional YYYY-MM-DD end date")
	flag.Parse()
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(2)
	}
	var from, to *time.Time
	if *fromFlag != "" {
		parsed, err := time.Parse("2006-01-02", *fromFlag)
		if err != nil {
			logger.Error("invalid from date", "error", err)
			os.Exit(2)
		}
		from = &parsed
	}
	if *toFlag != "" {
		parsed, err := time.Parse("2006-01-02", *toFlag)
		if err != nil {
			logger.Error("invalid to date", "error", err)
			os.Exit(2)
		}
		to = &parsed
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	db := store.New(pool)
	syncer := ingest.NewSyncer(pool, db, cfg)
	result, err := syncer.Run(ctx, *source, ingest.SyncOptions{From: from, To: to})
	logger.Info("index sync finished", "source", result.Source, "status", result.Status, "series", result.Metrics, "points_seen", result.PointsSeen, "points_stored", result.PointsStored, "error", result.Error)
	if err != nil {
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
