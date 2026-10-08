package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"dota-doggo/internal/bot"
	"dota-doggo/internal/config"
	"dota-doggo/internal/domain"
	"dota-doggo/internal/health"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/service"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/telegram"
)

func runBot(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := openApplicationDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
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
	if !me.IsBot || me.Username == "" {
		return errors.New("Telegram token does not identify a bot")
	}
	repo := postgres.New(pool)
	history := service.History{API: api, Repo: repo, Commit: func(ctx context.Context, topicID, playerID int64, matches []domain.Match, cursor int64) (int, error) {
		return postgres.CommitHistory(ctx, pool, topicID, playerID, matches, cursor)
	}}
	jobs := service.NewJobs(history, tg, log)
	background, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { jobs.Run(background) })
	wg.Go(func() {
		runConstants(background, service.ConstantsCache{Repo: repo, API: api, Interval: cfg.ConstantsSyncInterval}, log)
	})
	defer func() { cancel(); wg.Wait() }()
	monitor, err := health.Start(ctx, cfg.HealthAddr, "bot", pool.Ping)
	if err != nil {
		return err
	}
	defer monitor.Close()
	handler := bot.Handler{Repo: repo, Telegram: tg, API: api, Jobs: jobs, Username: me.Username, DefaultTimezone: cfg.DefaultTimezone, AllowedUserIDs: cfg.AllowedUserIDs, AdminCheck: cfg.TelegramAdminCheck, PollInterval: cfg.PollInterval}
	log.Info("Telegram polling started", "bot_username", me.Username)
	return tg.PollObserved(ctx, handler.Handle, log, func(err error) { monitor.Complete(err, 0) })
}
