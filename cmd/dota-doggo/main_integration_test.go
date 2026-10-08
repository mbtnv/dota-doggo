//go:build integration

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"dota-doggo/internal/testdb"
	"dota-doggo/migrations"
)

func TestDatabaseCommandsWithoutTelegramToken(t *testing.T) {
	ctx, _, pool := testdb.Open(t, false)
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", pool.Config().ConnString())
	t.Setenv("BOT_TOKEN", "")
	version := fmt.Sprintf("Schema version: %d\n", migrations.CurrentVersion)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"migrate", "status"}, "Schema version: 0\n"},
		{[]string{"migrate", "up"}, version},
		{[]string{"migrate"}, version},
		{[]string{"migrate", "status"}, version},
		{[]string{"healthcheck"}, "PostgreSQL connection OK (connectivity only)\n"},
	} {
		var out bytes.Buffer
		if err := run(ctx, tc.args, &out); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want {
			t.Fatalf("%v: %q, want %q", tc.args, out.String(), tc.want)
		}
	}
}

func TestLegacyCLIWithoutTelegramTokenAndRepeat(t *testing.T) {
	ctx, _, pool := testdb.Open(t, false)
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", pool.Config().ConnString())
	t.Setenv("BOT_TOKEN", "")
	path := filepath.Join(t.TempDir(), "players.json")
	if err := os.WriteFile(path, []byte(`[{"id":"123","name":"Player","last_match_id":"100"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Imported 1 players.\n", "Imported 0 players.\n"} {
		var out bytes.Buffer
		if err := run(ctx, []string{"import-legacy", "--path", path, "--chat-id", "-100", "--thread-id", "42"}, &out); err != nil || out.String() != want {
			t.Fatal(out.String(), err)
		}
	}
}
