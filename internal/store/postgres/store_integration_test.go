//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/testdb"
)

func ptr[T any](v T) *T { return &v }
func must[T any](t *testing.T, v T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestTopicIdentityPlayersSettingsAndCursors(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	s := postgres.New(pool)
	a, err := s.EnsureTopic(ctx, -100123, nil, ptr("Example"), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.EnsureTopic(ctx, -100123, nil, nil, "Europe/Moscow")
	if err != nil || a.ID != b.ID || b.Timezone != "UTC" {
		t.Fatalf("main chat duplicated/changed: %#v %v", b, err)
	}
	c, err := s.EnsureTopic(ctx, -100123, ptr(int64(42)), nil, "UTC")
	if err != nil || c.ID == a.ID {
		t.Fatalf("topic identity: %#v %v", c, err)
	}
	if err := s.SetTimezone(ctx, c.ID, "Europe/Moscow"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTimezone(ctx, c.ID, "Invalid/Timezone"); err == nil {
		t.Fatal("accepted invalid timezone")
	}
	if err := s.SetPaused(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertPlayer(ctx, 123456789, "Example", nil)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.AddPlayer(ctx, a.ID, id, ptr("mid"), ptr(int64(123)))
	if err != nil || !added {
		t.Fatalf("add: %t %v", added, err)
	}
	added, err = s.AddPlayer(ctx, a.ID, id, ptr("changed"), nil)
	if err != nil || added {
		t.Fatalf("duplicate: %t %v", added, err)
	}
	if _, err := s.AddPlayer(ctx, c.ID, id, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceCursor(ctx, a.ID, id, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceCursor(ctx, a.ID, id, 999); err != nil {
		t.Fatal(err)
	}
	players, err := s.Players(ctx, a.ID)
	if err != nil || len(players) != 1 || players[0].Label() != "mid" || *players[0].LastSeenMatchID != 1000 {
		t.Fatalf("players: %#v %v", players, err)
	}
	other, err := s.Players(ctx, c.ID)
	if err != nil || other[0].LastSeenMatchID != nil {
		t.Fatalf("topic cursor leaked: %#v %v", other, err)
	}
	removed, err := s.RemovePlayer(ctx, a.ID, "mid")
	if err != nil || !removed {
		t.Fatalf("remove: %t %v", removed, err)
	}
	other, err = s.Players(ctx, c.ID)
	if err != nil || len(other) != 1 {
		t.Fatalf("removed another topic: %#v %v", other, err)
	}
	topics, err := s.Topics(ctx)
	if err != nil || len(topics) != 2 || !topics[1].Paused || topics[1].Timezone != "Europe/Moscow" {
		t.Fatalf("settings: %#v %v", topics, err)
	}
}

func TestRemovePlayerPrefersAccountIDOverNumericAlias(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	s := postgres.New(pool)
	topic, err := s.EnsureTopic(ctx, -100123, nil, nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.UpsertPlayer(ctx, 123, "First", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.UpsertPlayer(ctx, 456, "Second", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlayer(ctx, topic.ID, first, ptr("456"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlayer(ctx, topic.ID, second, nil, nil); err != nil {
		t.Fatal(err)
	}
	removed, err := s.RemovePlayer(ctx, topic.ID, "456")
	if err != nil || !removed {
		t.Fatal(removed, err)
	}
	players, err := s.Players(ctx, topic.ID)
	if err != nil || len(players) != 1 || players[0].AccountID != 123 {
		t.Fatalf("removed a numeric alias instead of the explicit account: %#v %v", players, err)
	}
}

func TestSharedHistoryRecentMatchesAndRefresh(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	s := postgres.New(pool)
	p, err := s.UpsertPlayer(ctx, 123, "First", nil)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.UpsertPlayer(ctx, 456, "Second", nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	m := domain.Match{PlayerID: p, ID: 100, StartTime: at.Add(-time.Hour), EndTime: at, HeroID: 74, RadiantWin: true, Kills: 5}
	n := m
	n.PlayerID = q
	older := m
	older.ID = 99
	older.StartTime = older.StartTime.Add(-time.Hour)
	older.EndTime = older.EndTime.Add(-time.Hour)
	count, err := s.SaveMatches(ctx, []domain.Match{m, n, older})
	if err != nil || count != 3 {
		t.Fatalf("insert: %d %v", count, err)
	}
	m.HeroDamage = 23000
	m.RawPayload = []byte(`{"refreshed":true}`)
	count, err = s.SaveMatches(ctx, []domain.Match{m})
	if err != nil || count != 0 {
		t.Fatalf("refresh: %d %v", count, err)
	}
	recent, err := s.RecentMatches(ctx, []int64{p}, []int64{p, q}, 1)
	if err != nil || len(recent) != 2 || recent[0].ID != 100 || recent[0].HeroDamage != 23000 {
		t.Fatalf("shared recent: %#v %v", recent, err)
	}
	all, err := s.RecentMatches(ctx, []int64{p, q}, []int64{p, q}, 2)
	if err != nil || len(all) != 3 {
		t.Fatalf("unique count: %#v %v", all, err)
	}
	period, err := s.Matches(ctx, []int64{p}, at.Add(-time.Hour), at)
	if err != nil || len(period) != 1 || period[0].ID != 99 {
		t.Fatalf("half-open interval: %#v %v", period, err)
	}
	o, err := s.Overview(ctx, []int64{p, q})
	if err != nil || o.TotalRows != 3 || o.UniqueMatches != 2 || !o.LastEnd.Equal(at) {
		t.Fatalf("overview: %#v %v", o, err)
	}
	empty, err := s.Matches(ctx, nil, at, at.Add(time.Hour))
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty player list: %#v %v", empty, err)
	}
}

func TestRuntimeReportsAndConstants(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	s := postgres.New(pool)
	topic, err := s.EnsureTopic(ctx, -1, nil, nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	if err := s.MarkStarted(ctx, topic.ID, at); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFinished(ctx, topic.ID, at, at.Add(time.Second), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFinished(ctx, topic.ID, at.Add(time.Minute), at.Add(2*time.Minute), ptr("network")); err != nil {
		t.Fatal(err)
	}
	runtime, err := s.Runtime(ctx, topic.ID)
	if err != nil || runtime.Error == nil || *runtime.Error != "network" || !runtime.SucceededAt.Equal(at.Add(time.Second)) {
		t.Fatalf("runtime: %#v %v", runtime, err)
	}
	run := domain.ReportRun{Period: "day", Trigger: "auto", Start: at.AddDate(0, 0, -1), End: at, MessageID: ptr(int64(20))}
	if err := s.RecordReport(ctx, topic.ID, run); err != nil {
		t.Fatal(err)
	}
	exists, err := s.HasReport(ctx, topic.ID, run.Period, run.Start, run.End)
	if err != nil || !exists {
		t.Fatalf("report missing: %t %v", exists, err)
	}
	reports, err := s.LatestReports(ctx, topic.ID)
	if err != nil || len(reports) != 1 || *reports[0].MessageID != 20 {
		t.Fatalf("reports: %#v %v", reports, err)
	}
	if err := s.SaveConstants(ctx, []domain.ConstantEntry{{Resource: "heroes", Code: 74, Name: "Invoker"}, {Resource: "game_mode", Code: 22, Name: "All Pick"}, {Resource: "lobby_type", Code: 7, Name: "Ranked"}}); err != nil {
		t.Fatal(err)
	}
	constants, err := s.Constants(ctx)
	if err != nil || constants.Heroes[74] != "Invoker" || constants.GameModes[22] != "All Pick" || constants.LobbyTypes[7] != "Ranked" {
		t.Fatalf("constants: %#v %v", constants, err)
	}
	updated, err := s.ConstantsUpdatedAt(ctx, "heroes")
	if err != nil || updated == nil {
		t.Fatalf("updated: %v %v", updated, err)
	}
}

func TestRollbackAndConcurrentCursor(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	s := postgres.New(pool)
	topic, err := s.EnsureTopic(ctx, -1, nil, nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertPlayer(ctx, 123, "Example", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlayer(ctx, topic.ID, id, nil, nil); err != nil {
		t.Fatal(err)
	}
	err = postgres.InTx(ctx, pool, func(tx *postgres.Store) error {
		if err := tx.AdvanceCursor(ctx, topic.ID, id, 100); err != nil {
			return err
		}
		return errors.New("sending failed")
	})
	if err == nil {
		t.Fatal("transaction should fail")
	}
	players, err := s.Players(ctx, topic.ID)
	if err != nil || players[0].LastSeenMatchID != nil {
		t.Fatalf("rolled-back cursor persisted: %#v %v", players, err)
	}
	var wg sync.WaitGroup
	for _, cursor := range []int64{101, 99, 103, 102, 98} {
		wg.Go(func() {
			if err := s.AdvanceCursor(ctx, topic.ID, id, cursor); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	players, err = s.Players(ctx, topic.ID)
	if err != nil || *players[0].LastSeenMatchID != 103 {
		t.Fatalf("cursor regressed: %#v %v", players, err)
	}
}

func TestSingleWorkerLockAndRelease(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	first, err := postgres.LockWorker(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if second, err := postgres.LockWorker(ctx, pool); err == nil {
		_ = second.Close()
		t.Fatal("second worker acquired lock")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := postgres.LockWorker(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledTransactionDoesNotPersist(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := postgres.InTx(canceled, pool, func(*postgres.Store) error { t.Error("called after cancellation"); return nil }); err == nil {
		t.Fatal("accepted canceled transaction")
	}
}

func TestSparseRefreshPreservesMetricsAndExplicitZeroReplaces(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	s := postgres.New(pool)
	id, err := s.UpsertPlayer(ctx, 123, "Player", nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	m := domain.Match{PlayerID: id, ID: 100, StartTime: at.Add(-time.Hour), EndTime: at, GPM: 600, HeroDamage: 30000, PartySize: ptr(3), RawPayload: []byte(`{"hero_damage":30000,"party_size":3}`)}
	if _, err := s.SaveMatches(ctx, []domain.Match{m}); err != nil {
		t.Fatal(err)
	}
	sparse := m
	sparse.GPM, sparse.HeroDamage, sparse.PartySize = 0, 0, nil
	sparse.PresentMetrics = []string{}
	sparse.RawPayload = []byte(`{"kills":9}`)
	if _, err := s.SaveMatches(ctx, []domain.Match{sparse}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentMatches(ctx, []int64{id}, []int64{id}, 1)
	if err != nil || len(rows) != 1 || rows[0].GPM != 600 || rows[0].HeroDamage != 30000 || rows[0].PartySize == nil || *rows[0].PartySize != 3 {
		t.Fatalf("metrics lost: %#v %v", rows, err)
	}
	var raw map[string]int
	if err := json.Unmarshal(rows[0].RawPayload, &raw); err != nil || raw["hero_damage"] != 30000 || raw["kills"] != 9 {
		t.Fatal(raw, err)
	}
	sparse.PresentMetrics = []string{"hero_damage"}
	sparse.RawPayload = []byte(`{"hero_damage":0}`)
	if _, err := s.SaveMatches(ctx, []domain.Match{sparse}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.RecentMatches(ctx, []int64{id}, []int64{id}, 1)
	if err != nil || rows[0].HeroDamage != 0 || rows[0].GPM != 600 {
		t.Fatal(rows, err)
	}
}

func TestConstantResourceUpdateIsAtomic(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	s := postgres.New(pool)
	if err := s.SaveConstants(ctx, []domain.ConstantEntry{{Resource: "heroes", Code: 74, Name: "Invoker"}}); err != nil {
		t.Fatal(err)
	}
	// Duplicate keys cause PostgreSQL to reject the statement after processing an
	// earlier entry. No earlier change may escape the failed resource update.
	err := s.SaveConstants(ctx, []domain.ConstantEntry{{Resource: "heroes", Code: 74, Name: "Changed"}, {Resource: "heroes", Code: 5, Name: "CM"}, {Resource: "heroes", Code: 5, Name: "Duplicate"}})
	if err == nil {
		t.Fatal("expected duplicate key failure")
	}
	c, err := s.Constants(ctx)
	if err != nil || len(c.Heroes) != 1 || c.Heroes[74] != "Invoker" {
		t.Fatal(c, err)
	}
}
