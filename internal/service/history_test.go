package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
)

func historyMatch(t *testing.T, id int64) opendota.MatchData {
	t.Helper()
	var m opendota.MatchData
	err := json.Unmarshal([]byte(fmt.Sprintf(`{"match_id":%d,"account_id":"123","player_slot":0,"radiant_win":true,"duration":1800,"start_time":1770000000,"hero_id":74,"game_mode":22,"lobby_type":7,"kills":5,"deaths":2,"assists":8,"hero_damage":30000}`, id)), &m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type historyAPI struct {
	pages       map[int][]opendota.MatchData
	details     map[int64][]opendota.MatchData
	detailErr   map[int64]error
	pageErr     map[int]error
	offsets     []int
	detailCalls int
}

func (a *historyAPI) History(_ context.Context, _ int64, _, limit, offset int) ([]opendota.MatchData, error) {
	if limit != 100 {
		panic("page size")
	}
	a.offsets = append(a.offsets, offset)
	return a.pages[offset], a.pageErr[offset]
}
func (a *historyAPI) MatchPlayers(_ context.Context, id int64) ([]opendota.MatchData, error) {
	a.detailCalls++
	return a.details[id], a.detailErr[id]
}

type historyRepo struct {
	matches map[int64]domain.Match
	cursor  int64
}

func (r *historyRepo) SaveMatches(_ context.Context, matches []domain.Match) (int, error) {
	n := 0
	for _, m := range matches {
		if _, ok := r.matches[m.ID]; !ok {
			n++
		}
		r.matches[m.ID] = m
	}
	return n, nil
}
func (r *historyRepo) AdvanceCursor(_ context.Context, _, _ int64, id int64) error {
	r.cursor = max(r.cursor, id)
	return nil
}
func TestHistoryEnrichesRepeatsAndKeepsSuccessOnPartialFailure(t *testing.T) {
	m := historyMatch(t, 100)
	n := historyMatch(t, 99)
	bad := opendota.MatchData{MatchID: ref(int64(98))}
	api := &historyAPI{pages: map[int][]opendota.MatchData{0: {m, n, bad}}, details: map[int64][]opendota.MatchData{100: {m}}, detailErr: map[int64]error{99: errors.New("detail unavailable"), 98: errors.New("detail unavailable")}}
	repo := &historyRepo{matches: map[int64]domain.Match{}}
	h := History{API: api, Repo: repo}
	result, err := h.Sync(context.Background(), 1, domain.Player{ID: 41, AccountID: 123}, 7, nil)
	if err != nil || result != (HistoryResult{Fetched: 3, Inserted: 2, Failed: 2}) || repo.cursor != 100 || repo.matches[100].HeroDamage != 30000 {
		t.Fatal(result, repo, err)
	}
	result, err = h.Sync(context.Background(), 1, domain.Player{ID: 41, AccountID: 123}, 7, nil)
	if err != nil || result.Inserted != 0 || len(repo.matches) != 2 {
		t.Fatal(result, err)
	}
}
func ref[T any](v T) *T { return &v }
func TestHistoryPaginationCancellationAndRepeatedPage(t *testing.T) {
	page := make([]opendota.MatchData, 100)
	details := map[int64][]opendota.MatchData{}
	for i := range page {
		page[i] = historyMatch(t, int64(1000-i))
		details[int64(1000-i)] = []opendota.MatchData{page[i]}
	}
	api := &historyAPI{pages: map[int][]opendota.MatchData{0: page, 100: page}, details: details}
	repo := &historyRepo{matches: map[int64]domain.Match{}}
	h := History{API: api, Repo: repo}
	result, err := h.Sync(context.Background(), 1, domain.Player{ID: 41, AccountID: 123}, 7, nil)
	if err == nil || result.Fetched != 100 || api.detailCalls != 100 || len(api.offsets) != 2 {
		t.Fatal(result, api, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Sync(ctx, 1, domain.Player{}, 7, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestHistoryPageFailureAndAtomicCommitFailure(t *testing.T) {
	m := historyMatch(t, 100)
	api := &historyAPI{pages: map[int][]opendota.MatchData{0: {m}}, details: map[int64][]opendota.MatchData{100: {m}}}
	h := History{API: api, Commit: func(context.Context, int64, int64, []domain.Match, int64) (int, error) {
		return 0, errors.New("transaction failed")
	}}
	result, err := h.Sync(context.Background(), 1, domain.Player{ID: 41, AccountID: 123}, 7, nil)
	if err == nil || result.Inserted != 0 {
		t.Fatal(result, err)
	}
	api.pageErr = map[int]error{0: errors.New("history failed")}
	if _, err := h.Sync(context.Background(), 1, domain.Player{ID: 41, AccountID: 123}, 7, nil); err == nil {
		t.Fatal("accepted failed page")
	}
}
