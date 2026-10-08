package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestRedactsMessagesErrorsGroupsAndBoundAttributes(t *testing.T) {
	var out bytes.Buffer
	token := "123456789:abcdefghijklmnopqrstuvwxyz_123456789"
	log := New(&out, slog.LevelDebug, "private-api-key").With("token", token).WithGroup("request")
	log.Error("https://api.telegram.org/bot"+token+"/sendMessage",
		"error", errors.New("traceback: private-api-key postgres://user:password@host/db"),
		slog.Group("nested", "secret", "private-api-key"))
	text := out.String()
	for _, secret := range []string{token, "private-api-key", "password", "user:"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret %q leaked: %s", secret, text)
		}
	}
	if !strings.Contains(text, "REDACTED") {
		t.Fatal("expected redaction")
	}
}
func TestRedactionPreservesOrdinaryValues(t *testing.T) {
	text := "topic -100123 / player 456 / elapsed 1.5s"
	if Redact(text, "") != text {
		t.Fatal("ordinary log changed")
	}
}

func TestSharedGroupIsNotMutatedDuringConcurrentLogging(t *testing.T) {
	var out bytes.Buffer
	log := New(&out, slog.LevelInfo, "secret-value")
	group := slog.Group("shared", "value", "secret-value")
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { log.Info("example", group) })
	}
	wg.Wait()
	if group.Value.Group()[0].Value.String() != "secret-value" {
		t.Fatal("mutated caller attribute")
	}
	if strings.Contains(out.String(), "secret-value") {
		t.Fatal("unredacted concurrent log")
	}
}
