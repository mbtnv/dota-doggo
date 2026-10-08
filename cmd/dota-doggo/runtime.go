package main

import (
	"context"
	"log/slog"
	"time"

	"dota-doggo/internal/config"
	"dota-doggo/internal/service"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openApplicationDB(ctx context.Context, cfg config.Config) (*pgxpool.Pool, error) {
	startup, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	db, err := migrations.Open(startup, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	err = migrations.Up(startup, db)
	_ = db.Close()
	if err != nil {
		return nil, err
	}
	return postgres.Open(startup, cfg.DatabaseURL, cfg.DBMaxConns)
}
func runConstants(ctx context.Context, cache service.ConstantsCache, log *slog.Logger) {
	ticker := time.NewTicker(cache.Interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := cache.Sync(ctx); err != nil && ctx.Err() == nil {
			log.Warn("constants refresh failed; cached values retained", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
