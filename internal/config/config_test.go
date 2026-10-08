package config

import (
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := values[k]; return v, ok }
}
func TestDefaultsAndLegacyDSN(t *testing.T) {
	c, err := Load(env(map[string]string{"DATABASE_URL": "postgresql+asyncpg://user:password@localhost/dota"}), false)
	if err != nil {
		t.Fatal(err)
	}
	if c.DatabaseURL != "postgresql://user:password@localhost/dota" || c.DefaultTimezone != "UTC" || c.PollInterval != 15*time.Minute || !c.TelegramAdminCheck || c.DBMaxConns != 5 {
		t.Fatalf("incorrect defaults/normalization: %#v", c)
	}
	if _, err := Load(env(map[string]string{"DATABASE_URL": c.DatabaseURL}), true); err == nil || !strings.Contains(err.Error(), "BOT_TOKEN") {
		t.Fatalf("expected bot-specific validation, got %v", err)
	}
}
func TestOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{"DATABASE_URL": "postgres://localhost/dota", "BOT_TOKEN": "token", "DEFAULT_TIMEZONE": "America/New_York", "ALLOWED_TELEGRAM_USER_IDS": "123, 456,123,", "TELEGRAM_ADMIN_CHECK_ENABLED": "false", "TELEGRAM_PROXY_URL": "  socks5://localhost:1080 ", "RETRY_BACKOFF_SECONDS": "0.25", "DB_MAX_CONNS": "3"}), true)
	if err != nil {
		t.Fatal(err)
	}
	if c.TelegramAdminCheck || len(c.AllowedUserIDs) != 2 || !c.AllowedUserIDs[456] || c.RetryBackoff != 250*time.Millisecond || c.TelegramProxyURL != "socks5://localhost:1080" || c.DBMaxConns != 3 {
		t.Fatalf("overrides not applied: %#v", c)
	}
}

func TestBlankProxyIsDisabled(t *testing.T) {
	c, err := Load(env(map[string]string{"DATABASE_URL": "postgres://localhost/dota", "TELEGRAM_PROXY_URL": " \t "}), false)
	if err != nil || c.TelegramProxyURL != "" {
		t.Fatal(c.TelegramProxyURL, err)
	}
}
func TestInvalidConfigurationDoesNotExposeInput(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"DATABASE_URL", "mysql://user:secret@localhost/dota"}, {"DATABASE_URL", "postgres://localhost/"},
		{"POLL_INTERVAL_MINUTES", "0"}, {"POLL_INTERVAL_MINUTES", "NaN"}, {"POLL_INTERVAL_MINUTES", "+Inf"}, {"RETRY_BACKOFF_SECONDS", "1e30"},
		{"RETRY_BACKOFF_SECONDS", "1e-30"}, {"OPENDOTA_MAX_RETRIES", "-1"}, {"DB_MAX_CONNS", "2147483648"},
		{"ALLOWED_TELEGRAM_USER_IDS", "secret"}, {"TELEGRAM_ADMIN_CHECK_ENABLED", "secret"},
		{"DEFAULT_TIMEZONE", "Not/AZone"}, {"OPENDOTA_BASE_URL", "file://secret"}, {"TELEGRAM_PROXY_URL", "ftp://user:secret@host"},
		{"TELEGRAM_PROXY_URL", "://secret"}, {"LOG_LEVEL", "secret"},
		{"TELEGRAM_PROXY_URL", "socks4://user:secret@localhost"}, {"BOT_TOKEN", "invalid secret"},
		{"OPENDOTA_BASE_URL", "https://user:secret@example.com/api"},
		{"TELEGRAM_BASE_URL", "https://user:secret@example.com"}, {"TELEGRAM_BASE_URL", "https://example.com?secret=1"},
		{"HEALTH_ADDR", "0.0.0.0:8080"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			values := map[string]string{"DATABASE_URL": "postgres://localhost/dota"}
			values[tc.key] = tc.value
			_, err := Load(env(values), false)
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("expected validation of %s, got %v", tc.key, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("input leaked: %v", err)
			}
		})
	}
}
