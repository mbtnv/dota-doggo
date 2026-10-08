//go:build integration

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/testdb"
)

func TestLegacyImportIsRepeatableAndPreservesCurrentState(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	repo := postgres.New(pool)
	topic, err := repo.EnsureTopic(ctx, -1, nil, nil, "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetPaused(ctx, topic.ID, true); err != nil {
		t.Fatal(err)
	}
	profile := "https://example.com/current"
	id, err := repo.UpsertPlayer(ctx, 123, "Current name", &profile)
	if err != nil {
		t.Fatal(err)
	}
	alias := "carry"
	if _, err := repo.AddPlayer(ctx, topic.ID, id, &alias, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.AdvanceCursor(ctx, topic.ID, id, 200); err != nil {
		t.Fatal(err)
	}
	players, err := ParseLegacy(strings.NewReader(`[{"id":"123","name":"Old name","last_match_id":100},{"id":456,"name":"New","last_match_id":"50"}]`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{1, 0} {
		var inserted int
		err := postgres.InTx(ctx, pool, func(tx *postgres.Store) error {
			var err error
			inserted, err = ImportLegacy(ctx, tx, LegacyTopic{ChatID: -1, Timezone: "UTC"}, players)
			return err
		})
		if err != nil || inserted != want {
			t.Fatal(inserted, err)
		}
	}
	topic, err = repo.Topic(ctx, -1, nil)
	if err != nil || !topic.Paused || topic.Timezone != "America/New_York" {
		t.Fatal(topic, err)
	}
	current, err := repo.Players(ctx, topic.ID)
	if err != nil || len(current) != 2 || current[0].DisplayName != "Current name" || current[0].Label() != "carry" || current[0].ProfileURL == nil || *current[0].ProfileURL != profile || *current[0].LastSeenMatchID != 200 || *current[1].LastSeenMatchID != 50 {
		t.Fatal(current, err)
	}
}

type failingLegacy struct {
	*postgres.Store
	calls int
}

func (r *failingLegacy) AddPlayer(ctx context.Context, topicID, playerID int64, alias *string, user *int64) (bool, error) {
	r.calls++
	if r.calls == 2 {
		return false, errors.New("injected database failure")
	}
	return r.Store.AddPlayer(ctx, topicID, playerID, alias, user)
}
func TestLegacyImportRollsBackWholeFile(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	err := postgres.InTx(ctx, pool, func(tx *postgres.Store) error {
		_, err := ImportLegacy(ctx, &failingLegacy{Store: tx}, LegacyTopic{ChatID: -1, Timezone: "UTC"}, []LegacyPlayer{{1, "One", 100}, {2, "Two", 200}})
		return err
	})
	if err == nil {
		t.Fatal("failure hidden")
	}
	topics, err := postgres.New(pool).Topics(ctx)
	if err != nil || len(topics) != 0 {
		t.Fatal(topics, err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM players").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
