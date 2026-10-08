package main

import (
	"bytes"
	"context"
	"dota-doggo/internal/config"
	"dota-doggo/internal/health"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestHelpWithoutConfiguration(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "migrate") {
		t.Fatal(out.String())
	}
}
func TestRejectsUnknownAndInvalidCommands(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"bot", "extra"}, {"worker", "extra"}, {"import-legacy"}, {"migrate", "down"}, {"migrate", "up", "extra"}, {"healthcheck", "extra"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestImportFlagsAndHelpDoNotNeedConfiguration(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"import-legacy", "--help"}, &out); err != nil || !strings.Contains(out.String(), "chat-id") {
		t.Fatal(out.String(), err)
	}
	o, err := parseImport([]string{"--chat-id", "-100", "--thread-id", "42", "--path", "players.json", "--title", "Topic"}, io.Discard)
	if err != nil || o.Topic.ChatID != -100 || o.Topic.ThreadID == nil || *o.Topic.ThreadID != 42 || o.Topic.Title == nil || *o.Topic.Title != "Topic" {
		t.Fatal(o, err)
	}
	for _, args := range [][]string{{"--chat-id", "0"}, {"--chat-id", "-1", "--thread-id", "0"}, {"--chat-id", "-1", "extra"}} {
		if _, err := parseImport(args, io.Discard); err == nil {
			t.Fatal(args)
		}
	}
}
func TestWorkerRejectsPoolWithoutRoomForLockBeforeIO(t *testing.T) {
	err := runWorker(context.Background(), config.Config{DBMaxConns: 1}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), "DB_MAX_CONNS") {
		t.Fatal(err)
	}
}

func TestProcessHealthWithoutDatabaseOrToken(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "")
	t.Setenv("BOT_TOKEN", "")
	m, err := health.Start(context.Background(), "127.0.0.1:0", "bot", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	t.Setenv("HEALTH_ADDR", m.Address())
	m.Complete(nil, 0)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"healthcheck", "bot"}, &out); err != nil || out.String() != "bot ready\n" {
		t.Fatal(out.String(), err)
	}
	if err := run(context.Background(), []string{"healthcheck", "worker"}, &out); err == nil {
		t.Fatal("accepted readiness of the other component")
	}
}
