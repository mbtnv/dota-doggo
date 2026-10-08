//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dota-doggo/internal/bot"
	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/telegram"
	"dota-doggo/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ptr[T any](v T) *T { return &v }

type fakeAPI struct {
	matches map[int64][]opendota.MatchData
	fail    map[int64]error
	hook    func(int64)
}

func (a *fakeAPI) RecentMatches(ctx context.Context, id int64) ([]opendota.MatchData, error) {
	out := append([]opendota.MatchData(nil), a.matches[id]...)
	if a.hook != nil {
		a.hook(id)
	}
	return out, a.fail[id]
}
func (a *fakeAPI) Profile(_ context.Context, id int64) (opendota.Profile, error) {
	return opendota.Profile{AccountID: id, Name: "Player"}, nil
}
func (a *fakeAPI) RateLimits(context.Context, bool) (*opendota.RateLimits, error) { return nil, nil }

type delivery struct {
	topic domain.Topic
	text  string
}
type fakeSender struct {
	mu        sync.Mutex
	sent      []delivery
	failMatch string
	failTopic int64
	hook      func(context.Context) error
}

func (s *fakeSender) SendHTML(ctx context.Context, topic domain.Topic, text string) ([]int64, error) {
	if s.hook != nil {
		if err := s.hook(ctx); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.failMatch != "" && strings.Contains(text, s.failMatch)) || s.failTopic == topic.ID && s.failTopic != 0 {
		return nil, errors.New("Telegram unavailable secret-token")
	}
	s.sent = append(s.sent, delivery{topic, text})
	return []int64{int64(len(s.sent))}, nil
}
func (s *fakeSender) SendParts(ctx context.Context, topic domain.Topic, parts []string) ([]int64, error) {
	var ids []int64
	for _, part := range parts {
		v, err := s.SendHTML(ctx, topic, part)
		if err != nil {
			return ids, err
		}
		ids = append(ids, v...)
	}
	return ids, nil
}
func (s *fakeSender) IsAdmin(context.Context, int64, int64) (bool, error) { return true, nil }
func (s *fakeSender) snapshot() []delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]delivery(nil), s.sent...)
}
func match(t *testing.T, id int64) opendota.MatchData {
	t.Helper()
	var m opendota.MatchData
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"match_id":%d,"player_slot":0,"radiant_win":true,"duration":1800,"start_time":1791399600,"hero_id":74,"game_mode":22,"lobby_type":7,"kills":5,"deaths":2,"assists":8}`, id)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func setup(t *testing.T) (context.Context, *pgxpool.Pool, *postgres.Store, *fakeAPI, *fakeSender, *Worker) {
	t.Helper()
	ctx, _, pool := testdb.Open(t, true)
	repo := postgres.New(pool)
	api := &fakeAPI{matches: map[int64][]opendota.MatchData{}, fail: map[int64]error{}}
	sender := &fakeSender{}
	w := &Worker{Pool: pool, API: api, Sender: sender, Now: func() time.Time { return time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC) }, Secrets: []string{"secret-token"}}
	return ctx, pool, repo, api, sender, w
}
func track(t *testing.T, ctx context.Context, repo *postgres.Store, chat, account int64, alias string, cursor *int64) (domain.Topic, int64) {
	t.Helper()
	topic, err := repo.EnsureTopic(ctx, chat, nil, nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	id, err := repo.UpsertPlayer(ctx, account, alias, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddPlayer(ctx, topic.ID, id, &alias, nil); err != nil {
		t.Fatal(err)
	}
	if cursor != nil {
		if err := repo.AdvanceCursor(ctx, topic.ID, id, *cursor); err != nil {
			t.Fatal(err)
		}
	}
	return topic, id
}
func cursor(t *testing.T, ctx context.Context, repo *postgres.Store, topicID, playerID int64) *int64 {
	t.Helper()
	players, err := repo.Players(ctx, topicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range players {
		if p.ID == playerID {
			return p.LastSeenMatchID
		}
	}
	t.Fatal("missing player")
	return nil
}

func TestTrackingPollingReportsAndRestart(t *testing.T) {
	ctx, pool, repo, api, sender, w := setup(t)
	h := bot.Handler{Repo: repo, API: api, Telegram: sender}
	if err := h.Handle(ctx, telegram.Message{Text: "/track 123 carry", Chat: telegram.Chat{ID: -100, Type: "supergroup"}, ThreadID: ptr(int64(42)), From: &telegram.User{ID: 7}}); err != nil {
		t.Fatal(err)
	}
	api.matches[123] = []opendota.MatchData{match(t, 99)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 1 {
		t.Fatal("initial sync notified")
	}
	api.matches[123] = []opendota.MatchData{match(t, 100), match(t, 99)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.ReportsOnce(ctx, domain.Day); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 3 || !strings.Contains(sent[1].text, "/matches/100") || !strings.Contains(sent[2].text, "Matches: 2") {
		t.Fatal(sent)
	}
	for _, d := range sent {
		if d.topic.ChatID != -100 || d.topic.ThreadID == nil || *d.topic.ThreadID != 42 {
			t.Fatal(d)
		}
	}
	restarted := Worker{Pool: pool, API: api, Sender: sender, Now: w.Now}
	if err := restarted.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ReportsOnce(ctx, domain.Day); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 3 {
		t.Fatal("restart duplicated sends")
	}
}
func TestIndependentTopicCursorsAndSharedGroup(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	a, p := track(t, ctx, repo, -1, 123, "carry", nil)
	b, _ := track(t, ctx, repo, -2, 123, "other alias", nil)
	_, q := track(t, ctx, repo, -1, 456, "support", nil)
	api.matches[123] = []opendota.MatchData{match(t, 99)}
	api.matches[456] = []opendota.MatchData{match(t, 99)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 0 {
		t.Fatal("initial notifications")
	}
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	api.matches[456] = []opendota.MatchData{match(t, 100)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 2 || !strings.Contains(sent[0].text, "carry, support") || !strings.Contains(sent[1].text, "other alias") {
		t.Fatal(sent)
	}
	for _, pair := range [][2]int64{{a.ID, p}, {a.ID, q}, {b.ID, p}} {
		if c := cursor(t, ctx, repo, pair[0], pair[1]); c == nil || *c != 100 {
			t.Fatal(c)
		}
	}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 2 {
		t.Fatal("duplicate")
	}
}
func TestEmptyInitialResponseDoesNotInitializeCursor(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	topic, id := track(t, ctx, repo, -1, 123, "Player", nil)
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if cursor(t, ctx, repo, topic.ID, id) != nil {
		t.Fatal("empty cursor initialized")
	}
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 0 {
		t.Fatal("first nonempty response notified")
	}
	if c := cursor(t, ctx, repo, topic.ID, id); c == nil || *c != 100 {
		t.Fatal(c)
	}
}
func TestFailedSendKeepsEarlierGamesAndRetriesOnlyUnacknowledged(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	topic, id := track(t, ctx, repo, -1, 123, "Player", ptr(int64(100)))
	api.matches[123] = []opendota.MatchData{match(t, 103), match(t, 101), match(t, 102)}
	sender.failMatch = "/matches/102"
	if err := w.PollOnce(ctx); err == nil {
		t.Fatal("send failure hidden")
	}
	if c := cursor(t, ctx, repo, topic.ID, id); c == nil || *c != 101 {
		t.Fatal(c)
	}
	state, err := repo.Runtime(ctx, topic.ID)
	if err != nil || state.Error == nil || strings.Contains(*state.Error, "secret-token") || state.SucceededAt != nil {
		t.Fatal(state, err)
	}
	sender.failMatch = ""
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 3 || !strings.Contains(sent[0].text, "/matches/101") || !strings.Contains(sent[1].text, "/matches/102") || !strings.Contains(sent[2].text, "/matches/103") {
		t.Fatal(sent)
	}
	state, err = repo.Runtime(ctx, topic.ID)
	if err != nil || state.Error != nil || state.SucceededAt == nil {
		t.Fatal(state, err)
	}
}
func TestMalformedPlayerDoesNotHideItsMatchesOrBlockOthers(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	topic, p := track(t, ctx, repo, -1, 123, "bad", ptr(int64(99)))
	_, q := track(t, ctx, repo, -1, 456, "good", ptr(int64(99)))
	bad := match(t, 100)
	bad.HeroID = nil
	api.matches[123] = []opendota.MatchData{match(t, 101), bad}
	api.matches[456] = []opendota.MatchData{match(t, 100)}
	if err := w.PollOnce(ctx); err == nil {
		t.Fatal("malformed response hidden")
	}
	if *cursor(t, ctx, repo, topic.ID, p) != 99 || *cursor(t, ctx, repo, topic.ID, q) != 100 || len(sender.snapshot()) != 1 {
		t.Fatal("player isolation failed")
	}
}
func TestConcurrentResyncSuppressesLoadedNotificationsOnlyInItsTopic(t *testing.T) {
	ctx, pool, repo, api, sender, w := setup(t)
	a, p := track(t, ctx, repo, -1, 123, "Player", ptr(int64(99)))
	b, _ := track(t, ctx, repo, -2, 123, "Other", ptr(int64(99)))
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	loaded, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	api.hook = func(int64) { once.Do(func() { close(loaded); <-release }) }
	done := make(chan error, 1)
	go func() { done <- w.PollOnce(ctx) }()
	select {
	case <-loaded:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	m, err := match(t, 101).Snapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.CommitHistory(ctx, pool, a.ID, p, []domain.Match{m}, 101); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	// Shared persisted history is also a delivery source. Resync suppresses
	// both games only in A; B delivers 100 and the already stored 101.
	if len(sent) != 2 || sent[0].topic.ID != b.ID || sent[1].topic.ID != b.ID ||
		!strings.Contains(sent[0].text, "/matches/100") || !strings.Contains(sent[1].text, "/matches/101") ||
		*cursor(t, ctx, repo, a.ID, p) != 101 || *cursor(t, ctx, repo, b.ID, p) != 101 {
		t.Fatal(sent)
	}
}
func TestTopicLockSerializesSendAndResync(t *testing.T) {
	ctx, pool, repo, api, sender, w := setup(t)
	topic, p := track(t, ctx, repo, -1, 123, "Player", ptr(int64(99)))
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	sending, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	sender.hook = func(ctx context.Context) error {
		once.Do(func() { close(sending) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	go func() { done <- w.PollOnce(ctx) }()
	<-sending
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err := postgres.CommitHistory(blocked, pool, topic.ID, p, nil, 200)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resync bypassed send lock: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.CommitHistory(ctx, pool, topic.ID, p, nil, 200); err != nil {
		t.Fatal(err)
	}
	if *cursor(t, ctx, repo, topic.ID, p) != 200 {
		t.Fatal("cursor decreased")
	}
}
func TestPauseDuringAPIRequestAndCancellationPreventFurtherSends(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	topic, p := track(t, ctx, repo, -1, 123, "Player", ptr(int64(99)))
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	api.hook = func(int64) {
		if err := repo.SetPaused(ctx, topic.ID, true); err != nil {
			t.Error(err)
		}
	}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 0 || *cursor(t, ctx, repo, topic.ID, p) != 99 {
		t.Fatal("pause ignored")
	}
	api.hook = nil
	if err := repo.SetPaused(ctx, topic.ID, false); err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithCancel(ctx)
	sender.hook = func(ctx context.Context) error { cancel(); <-ctx.Done(); return ctx.Err() }
	if err := w.PollOnce(stopCtx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if *cursor(t, ctx, repo, topic.ID, p) != 99 || len(sender.snapshot()) != 0 {
		t.Fatal("canceled send advanced cursor")
	}
}
