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
	"github.com/JoseLArantes/tucano-indices/internal/store"
)

type schedule struct {
	source string
	hour   int
	minute int
}

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
	jobs := []schedule{{source: "focus", hour: 7}, {source: "tesouro", hour: 18, minute: 30}, {source: "sgs", hour: 19, minute: 30}, {source: "selic", hour: 20, minute: 30}, {source: "b3", hour: 21}}
	for _, job := range jobs {
		job := job
		go runSchedule(ctx, syncer, job, logger)
	}
	logger.Info("index scheduler started", "timezone", "America/Sao_Paulo", "jobs", jobs)
	<-ctx.Done()
}

func runSchedule(ctx context.Context, syncer *ingest.Syncer, job schedule, logger *slog.Logger) {
	for {
		next := nextWeekday(job)
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
		result, err := syncer.Run(runCtx, job.source, ingest.SyncOptions{})
		cancel()
		if err != nil {
			logger.Error("scheduled index sync failed", "source", job.source, "error", err, "points_seen", result.PointsSeen)
		} else {
			logger.Info("scheduled index sync finished", "source", job.source, "status", result.Status, "points_seen", result.PointsSeen, "points_stored", result.PointsStored)
		}
	}
}

func nextWeekday(job schedule) time.Time {
	return nextWeekdayAfter(time.Now(), job)
}

func nextWeekdayAfter(now time.Time, job schedule) time.Time {
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		location = time.FixedZone("BRT", -3*60*60)
	}
	now = now.In(location)
	next := time.Date(now.Year(), now.Month(), now.Day(), job.hour, job.minute, 0, 0, location)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	for next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
