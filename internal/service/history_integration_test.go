//go:build integration

package service

import (
	"context"
	"errors"
	"testing"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/testdb"
)

func TestHistoryRefreshesStoredSparseMatchAndKeepsTopicCursorsIndependent(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	repo := postgres.New(pool)
	topic, err := repo.EnsureTopic(ctx, -1, nil, nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.EnsureTopic(ctx, -2, nil, nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	id, err := repo.UpsertPlayer(ctx, 123, "Player", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, topicID := range []int64{topic.ID, other.ID} {
		if _, err := repo.AddPlayer(ctx, topicID, id, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	full := historyMatch(t, 100)
	brief := full
	brief.HeroDamage = nil
	m, err := brief.Snapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveMatches(ctx, []domain.Match{m}); err != nil {
		t.Fatal(err)
	}
	api := &historyAPI{pages: map[int][]opendota.MatchData{0: {brief}}, details: map[int64][]opendota.MatchData{100: {full}}}
	history := History{API: api, Commit: func(ctx context.Context, topicID, playerID int64, matches []domain.Match, cursor int64) (int, error) {
		inserted := 0
		err := postgres.InTx(ctx, pool, func(tx *postgres.Store) error {
			var err error
			inserted, err = tx.SaveMatches(ctx, matches)
			if err != nil {
				return err
			}
			return tx.AdvanceCursor(ctx, topicID, playerID, cursor)
		})
		return inserted, err
	}}
	player := domain.Player{ID: id, AccountID: 123}
	result, err := history.Sync(ctx, topic.ID, player, 7, nil)
	if err != nil || result != (HistoryResult{Fetched: 1}) {
		t.Fatal(result, err)
	}
	rows, err := repo.RecentMatches(ctx, []int64{id}, []int64{id}, 1)
	if err != nil || len(rows) != 1 || rows[0].HeroDamage != 30000 {
		t.Fatal(rows, err)
	}
	players, err := repo.Players(ctx, topic.ID)
	if err != nil || players[0].LastSeenMatchID == nil || *players[0].LastSeenMatchID != 100 {
		t.Fatal(players, err)
	}
	players, err = repo.Players(ctx, other.ID)
	if err != nil || players[0].LastSeenMatchID != nil {
		t.Fatal(players, err)
	}
	api.detailErr = map[int64]error{100: errors.New("offline")}
	result, err = history.Sync(ctx, topic.ID, player, 7, nil)
	if err != nil || result.Failed != 1 || result.Inserted != 0 {
		t.Fatal(result, err)
	}
	rows, err = repo.RecentMatches(ctx, []int64{id}, []int64{id}, 1)
	if err != nil || rows[0].HeroDamage != 30000 {
		t.Fatal(rows, err)
	}
}
