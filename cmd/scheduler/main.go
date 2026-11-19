package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JoseLArantes/tucano-indices/internal/config"
	"github.com/JoseLArantes/tucano-indices/internal/ingest"
	"github.com/JoseLArantes/tucano-indices/internal/scheduler"
	"github.com/JoseLArantes/tucano-indices/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	syncer := ingest.NewSyncer(pool, store.New(pool), cfg)
	jobs := []scheduler.Job{{Source: "focus", Hour: 7}, {Source: "tesouro", Hour: 18, Minute: 30}, {Source: "sgs", Hour: 19, Minute: 30}, {Source: "selic", Hour: 20, Minute: 30}, {Source: "b3", Hour: 21}}
	for _, job := range jobs {
		job := job
		go runSchedule(ctx, syncer, job, logger)
	}
	logger.Info("index scheduler started", "timezone", "America/Sao_Paulo", "jobs", jobs)
	<-ctx.Done()
}

func runSchedule(ctx context.Context, syncer *ingest.Syncer, job scheduler.Job, logger *slog.Logger) {
	for {
		next := job.Next(time.Now())
		delay := time.Until(next)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		result, err := syncer.Run(runCtx, job.Source, ingest.SyncOptions{})
		cancel()
		if err != nil {
			logger.Error("scheduled index sync failed", "source", job.Source, "error", err, "points_seen", result.PointsSeen)
		} else {
			logger.Info("scheduled index sync finished", "source", job.Source, "status", result.Status, "points_seen", result.PointsSeen, "points_stored", result.PointsStored)
		}
	}
}
