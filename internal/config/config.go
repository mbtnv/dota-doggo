// Package config loads and validates application configuration without logging secrets.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"dota-doggo/internal/health"
)

type Config struct {
	BotToken, DatabaseURL, OpenDotaBaseURL, OpenDotaAPIKey string
	DefaultTimezone, TelegramProxyURL                      string
	TelegramBaseURL, HealthAddr                            string
	PollInterval, RetryBackoff, ConstantsSyncInterval      time.Duration
	AllowedUserIDs                                         map[int64]bool
	TelegramAdminCheck                                     bool
	OpenDotaMaxRetries, TelegramMaxRetries                 int
	DBMaxConns                                             int32
	LogLevel                                               slog.Level
}

// Load accepts a lookup function so tests never depend on the developer's .env.
// BotToken is required only for commands that actually connect to Telegram.
func Load(lookup func(string) (string, bool), requireBot bool) (Config, error) {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok {
			return v
		}
		return fallback
	}
	c := Config{
		BotToken:         strings.TrimSpace(get("BOT_TOKEN", "")),
		DatabaseURL:      strings.TrimSpace(get("DATABASE_URL", "")),
		OpenDotaBaseURL:  strings.TrimRight(get("OPENDOTA_BASE_URL", "https://api.opendota.com/api"), "/"),
		OpenDotaAPIKey:   get("OPENDOTA_API_KEY", ""),
		DefaultTimezone:  get("DEFAULT_TIMEZONE", "UTC"),
		TelegramProxyURL: strings.TrimSpace(get("TELEGRAM_PROXY_URL", "")),
		TelegramBaseURL:  strings.TrimRight(get("TELEGRAM_BASE_URL", "https://api.telegram.org"), "/"),
		HealthAddr:       strings.TrimSpace(get("HEALTH_ADDR", "")),
		AllowedUserIDs:   map[int64]bool{},
	}
	var problems []error
	bad := func(key, reason string) { problems = append(problems, fmt.Errorf("%s: %s", key, reason)) }
	if err := health.ValidateAddress(c.HealthAddr); err != nil {
		problems = append(problems, err)
	}
	positive := func(key, fallback string) float64 {
		v, err := strconv.ParseFloat(get(key, fallback), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			bad(key, "must be a positive number")
			return 1
		}
		return v
	}
	integer := func(key, fallback string) int {
		v, err := strconv.ParseInt(get(key, fallback), 10, 32)
		if err != nil || v < 1 {
			bad(key, "must be a positive 32-bit integer")
			return 1
		}
		return int(v)
	}
	duration := func(key, fallback string, unit time.Duration) time.Duration {
		v := positive(key, fallback)
		if v >= float64(math.MaxInt64)/float64(unit) || v*float64(unit) < 1 {
			bad(key, "duration out of range")
			return unit
		}
		return time.Duration(v * float64(unit))
	}
	c.PollInterval = duration("POLL_INTERVAL_MINUTES", "15", time.Minute)
	c.RetryBackoff = duration("RETRY_BACKOFF_SECONDS", "1", time.Second)
	c.ConstantsSyncInterval = duration("CONSTANTS_SYNC_INTERVAL_HOURS", "24", time.Hour)
	c.OpenDotaMaxRetries = integer("OPENDOTA_MAX_RETRIES", "3")
	c.TelegramMaxRetries = integer("TELEGRAM_SEND_MAX_RETRIES", "3")
	c.DBMaxConns = int32(integer("DB_MAX_CONNS", "5"))
	var err error
	c.TelegramAdminCheck, err = strconv.ParseBool(get("TELEGRAM_ADMIN_CHECK_ENABLED", "true"))
	if err != nil {
		bad("TELEGRAM_ADMIN_CHECK_ENABLED", "must be a boolean")
	}
	if err := c.LogLevel.UnmarshalText([]byte(strings.ToUpper(get("LOG_LEVEL", "INFO")))); err != nil {
		bad("LOG_LEVEL", "must be DEBUG, INFO, WARN or ERROR")
	}
	for _, raw := range strings.Split(get("ALLOWED_TELEGRAM_USER_IDS", ""), ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			bad("ALLOWED_TELEGRAM_USER_IDS", "must contain positive integer IDs")
			continue
		}
		c.AllowedUserIDs[id] = true
	}
	if requireBot && c.BotToken == "" {
		bad("BOT_TOKEN", "required")
	}
	if c.BotToken != "" && strings.ContainsAny(c.BotToken, "/?# \t\r\n") {
		bad("BOT_TOKEN", "invalid token")
	}
	c.DatabaseURL = strings.Replace(c.DatabaseURL, "postgresql+asyncpg://", "postgresql://", 1)
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.Path == "" || u.Path == "/" {
		bad("DATABASE_URL", "must be a PostgreSQL URL with host and database")
	}
	u, err = url.Parse(c.OpenDotaBaseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		bad("OPENDOTA_BASE_URL", "must be an HTTP(S) URL")
	}
	u, err = url.Parse(c.TelegramBaseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		bad("TELEGRAM_BASE_URL", "must be an HTTP(S) URL without credentials, query or fragment")
	}
	if c.TelegramProxyURL != "" {
		u, err := url.Parse(c.TelegramProxyURL)
		if err != nil || u == nil || u.Hostname() == "" {
			bad("TELEGRAM_PROXY_URL", "invalid proxy URL")
		} else {
			switch u.Scheme {
			case "http", "https", "socks4", "socks5":
				if u.Scheme == "socks4" && u.User != nil {
					if password, ok := u.User.Password(); ok && password != "" {
						bad("TELEGRAM_PROXY_URL", "SOCKS4 does not support password authentication")
					}
				}
			default:
				bad("TELEGRAM_PROXY_URL", "unsupported proxy scheme")
			}
		}
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		bad("DEFAULT_TIMEZONE", "unknown IANA timezone")
	}
	return c, errors.Join(problems...)
}
