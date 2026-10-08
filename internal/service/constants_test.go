package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"dota-doggo/internal/domain"
)

type constantsMemory struct {
	at      map[string]time.Time
	entries map[string][]domain.ConstantEntry
}

func (m *constantsMemory) ConstantsUpdatedAt(ctx context.Context, r string) (*time.Time, error) {
	at, ok := m.at[r]
	if !ok {
		return nil, nil
	}
	return &at, nil
}
func (m *constantsMemory) SaveConstants(ctx context.Context, e []domain.ConstantEntry) error {
	m.entries[e[0].Resource] = e
	return nil
}
func (m *constantsMemory) Constants(context.Context) (domain.Constants, error) {
	return domain.Constants{}, nil
}

type constantsFake struct {
	calls []string
	fail  string
}

func (f *constantsFake) Constants(ctx context.Context, r string) (map[string]json.RawMessage, error) {
	f.calls = append(f.calls, r)
	if r == f.fail {
		return nil, errors.New("offline")
	}
	return map[string]json.RawMessage{"22": json.RawMessage(`{"name":"game_mode_all_draft"}`)}, nil
}
func TestCacheSkipsFreshAndPreservesCacheOnFailure(t *testing.T) {
	now := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	old := domain.ConstantEntry{Resource: "heroes", Code: 74, Name: "Invoker"}
	repo := &constantsMemory{at: map[string]time.Time{"lobby_type": now}, entries: map[string][]domain.ConstantEntry{"heroes": {old}}}
	api := &constantsFake{fail: "heroes"}
	c := ConstantsCache{Repo: repo, API: api, Now: func() time.Time { return now }}
	if err := c.Sync(context.Background()); err == nil {
		t.Fatal("failure hidden")
	}
	if len(api.calls) != 2 || repo.entries["heroes"][0].Name != "Invoker" || repo.entries["game_mode"][0].Name != "All Pick" {
		t.Fatalf("calls %#v cache %#v", api.calls, repo.entries)
	}
}
func TestParseConstantsNamesAndBadEntries(t *testing.T) {
	payload := map[string]json.RawMessage{"74": json.RawMessage(`{"localized_name":"Invoker","name":"npc_dota_hero_invoker"}`), "bad": json.RawMessage(`{}`), "2": json.RawMessage(`[]`), "3": json.RawMessage(`null`)}
	entries := ParseConstants("heroes", payload)
	if len(entries) != 1 || entries[0].Name != "Invoker" {
		t.Fatal(entries)
	}
	entries = ParseConstants("lobby_type", map[string]json.RawMessage{"7": json.RawMessage(`{"name":"lobby_type_ranked"}`)})
	if entries[0].Name != "Ranked" {
		t.Fatal(entries)
	}
}
