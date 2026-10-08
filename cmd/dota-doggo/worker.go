package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"dota-doggo/internal/config"
	"dota-doggo/internal/health"
	"dota-doggo/internal/jobs"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/service"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/telegram"
)

func runWorker(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if cfg.DBMaxConns < 2 {
		return errors.New("worker requires DB_MAX_CONNS >= 2 (one dedicated lock session)")
	}
	pool, err := openApplicationDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	startup, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	guard, err := postgres.LockWorker(startup, pool)
	cancelStartup()
	if err != nil {
		return err
	}
	defer guard.Close()
	api, err := opendota.New(opendota.Options{BaseURL: cfg.OpenDotaBaseURL, APIKey: cfg.OpenDotaAPIKey, MaxAttempts: cfg.OpenDotaMaxRetries, Backoff: cfg.RetryBackoff})
	if err != nil {
		return err
	}
	defer api.Close()
	tg, err := telegram.New(telegram.Options{Token: cfg.BotToken, BaseURL: cfg.TelegramBaseURL, ProxyURL: cfg.TelegramProxyURL, MaxAttempts: cfg.TelegramMaxRetries, Backoff: cfg.RetryBackoff})
	if err != nil {
		return err
	}
	defer tg.Close()
	identityCtx, cancelIdentity := context.WithTimeout(ctx, time.Minute)
	me, err := tg.Me(identityCtx)
	cancelIdentity()
	if err != nil {
		return err
	}
	if !me.IsBot {
		return errors.New("Telegram token does not identify a bot")
	}
	background, cancel := context.WithCancelCause(ctx)
	var wg sync.WaitGroup
	check := func(ctx context.Context) error {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := guard.Ping(pingCtx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", jobs.ErrLockLost, err)
		}
		return nil
	}
	monitor, err := health.Start(background, cfg.HealthAddr, "worker", func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return err
		}
		return pool.Ping(ctx)
	})
	if err != nil {
		cancel(nil)
		return err
	}
	defer monitor.Close()
	wg.Go(func() {
		monitorWorker(background, cancel, check, 5*time.Second)
	})
	wg.Go(func() {
		runConstants(background, service.ConstantsCache{Repo: postgres.New(pool), API: api, Interval: cfg.ConstantsSyncInterval}, log)
	})
	defer func() { cancel(nil); wg.Wait() }()
	w := jobs.Worker{Pool: pool, API: api, Sender: tg, Log: log, Check: func(ctx context.Context) error {
		err := check(ctx)
		if err == nil {
			monitor.Progress()
		}
		return err
	}, Secrets: []string{cfg.BotToken, cfg.OpenDotaAPIKey, cfg.DatabaseURL, cfg.TelegramProxyURL}}
	log.Info("worker started", "poll_interval", cfg.PollInterval)
	return (jobs.Runner{Cycle: func(ctx context.Context) error {
		monitor.Begin()
		err := w.Cycle(ctx)
		monitor.Complete(err, cfg.PollInterval)
		return err
	}, Interval: cfg.PollInterval, Log: log}).Run(background)
}

func monitorWorker(ctx context.Context, cancel context.CancelCauseFunc, check func(context.Context) error, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			if err := check(ctx); err != nil {
				if ctx.Err() == nil {
					cancel(err)
				}
				return
			}
		}
	}
}
