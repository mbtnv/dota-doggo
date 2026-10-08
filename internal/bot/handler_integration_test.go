//go:build integration

package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/format"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/telegram"
	"dota-doggo/internal/testdb"
)

type fakeAPI struct {
	OpenDota
	limits *opendota.RateLimits
}

func (*fakeAPI) Profile(_ context.Context, id int64) (opendota.Profile, error) {
	return opendota.Profile{AccountID: id, Name: "Player <name>"}, nil
}
func (f *fakeAPI) RateLimits(context.Context, bool) (*opendota.RateLimits, error) {
	return f.limits, nil
}

type fakeQueue struct {
	calls   int
	topic   domain.Topic
	players []domain.Player
	days    int
}

func (q *fakeQueue) Enqueue(topic domain.Topic, players []domain.Player, days int) bool {
	q.calls++
	q.topic = topic
	q.players = players
	q.days = days
	return true
}

func TestCommandsPersistSettingsReportsAndRouteTopics(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	repo := postgres.New(pool)
	tg := &fakeTelegram{}
	jobs := &fakeQueue{}
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	api := &fakeAPI{}
	h := Handler{Repo: repo, Telegram: tg, API: api, Jobs: jobs, Username: "doggo_bot", DefaultTimezone: "UTC", Now: func() time.Time { return at }}
	m := telegram.Message{Chat: telegram.Chat{ID: -100, Type: "supergroup"}, ThreadID: ref(int64(42)), From: &telegram.User{ID: 7}}
	command := func(text, want string) {
		t.Helper()
		m.Text = text
		before := len(tg.messages)
		if err := h.Handle(ctx, m); err != nil {
			t.Fatal(text, err)
		}
		if len(tg.messages) <= before || !strings.Contains(strings.Join(tg.messages[before:], "\n"), want) {
			t.Fatalf("%s: %v want %q", text, tg.messages[before:], want)
		}
	}
	command("/help@doggo_bot", "/resync")
	command("/limits", "не передал")
	api.limits = &opendota.RateLimits{RemainingMinute: ref(int64(42)), LimitMinute: ref(int64(60))}
	command("/limits", "Минута: 42 / 60")
	command("/players", "пока нет")
	command("/status", "не было")
	command("/track https://www.dotabuff.com/players/123 mid <carry>", "mid &lt;carry&gt;")
	command("/track 123", "уже отслеживается")
	command("/track 456 support", "Добавлен")
	command("/track 789 empty", "Добавлен")
	command("/players", "123")
	command("/set_timezone Invalid/Zone", "Неизвестная")
	command("/set_timezone Europe/Moscow", "Europe/Moscow")
	command("/pause", "приостановлен")
	command("/status", "Paused: true")
	command("/resume", "возобновлён")
	topic, err := repo.Topic(ctx, -100, m.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	players, err := repo.Players(ctx, topic.ID)
	if err != nil || len(players) != 3 {
		t.Fatal(players, err)
	}
	for _, p := range players[:2] {
		match := domain.Match{PlayerID: p.ID, ID: 1000, StartTime: at.Add(-time.Hour), EndTime: at.Add(-time.Minute), HeroID: 74, RadiantWin: true, Kills: 5, Deaths: 2, Assists: 9}
		if _, err := repo.SaveMatches(ctx, []domain.Match{match}); err != nil {
			t.Fatal(err)
		}
		if err := repo.AdvanceCursor(ctx, topic.ID, p.ID, 1000); err != nil {
			t.Fatal(err)
		}
	}
	command("/report day 123", "Matches: 1")
	command("/report day", "Matches: 1")
	if strings.Contains(tg.messages[len(tg.messages)-1], "empty") {
		t.Fatal("day report included an empty player")
	}
	command("/report week", "mid &lt;carry&gt;")
	command("/report month", "support")
	command("/report year", "day, week или month")
	command("/report day missing", "не найден")
	command("/leaders day", "WR 100.00%")
	command("/last 1 123", "support") // selected player's shared match includes both participants
	command("/last 11", "Использование")
	command("/resync 30 mid <carry>", "поставлено в очередь")
	if jobs.calls != 1 || jobs.days != 30 || len(jobs.players) != 1 || jobs.players[0].AccountID != 123 || jobs.topic.ID != topic.ID {
		t.Fatal(jobs)
	}
	command("/resync 366", "Использование")
	command("/status", "Уникальных матчей: 1")
	reports, err := repo.LatestReports(ctx, topic.ID)
	if err != nil || len(reports) != 3 {
		t.Fatal(reports, err)
	}
	for _, dest := range tg.destinations {
		if dest.ChatID != -100 || dest.ThreadID == nil || *dest.ThreadID != 42 {
			t.Fatal(dest)
		}
	}
	other, err := repo.EnsureTopic(ctx, -100, ref(int64(99)), nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AddPlayer(ctx, other.ID, players[0].ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	command("/untrack 123", "История сохранена")
	command("/untrack 123", "не найден")
	remaining, err := repo.Players(ctx, other.ID)
	if err != nil || len(remaining) != 1 {
		t.Fatal(remaining, err)
	}
	overview, err := repo.Overview(ctx, []int64{players[0].ID})
	if err != nil || overview.TotalRows != 1 {
		t.Fatal(overview, err)
	}
}

func TestAmbiguousAliasDoesNotRemoveOrResyncArbitraryPlayer(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	repo := postgres.New(pool)
	tg := &fakeTelegram{}
	queue := &fakeQueue{}
	h := Handler{Repo: repo, Telegram: tg, API: &fakeAPI{}, Jobs: queue}
	m := telegram.Message{Chat: telegram.Chat{ID: -1, Type: "group"}, From: &telegram.User{ID: 7}}
	for _, command := range []string{"/track 123 duplicate", "/track 456 duplicate", "/untrack duplicate", "/resync 7 duplicate", "/report day duplicate", "/last 5 duplicate"} {
		m.Text = command
		if err := h.Handle(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, message := range tg.messages[2:] {
		if !strings.Contains(message, "неоднозначен") {
			t.Fatal(message)
		}
	}
	topic, err := repo.Topic(ctx, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	players, err := repo.Players(ctx, topic.ID)
	if err != nil || len(players) != 2 || queue.calls != 0 {
		t.Fatal(players, queue, err)
	}
}

func TestHTTPPollingTracksFixtureAndRepliesToItsTopic(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fixture, err := os.ReadFile("../../testdata/telegram-updates.json")
	if err != nil {
		t.Fatal(err)
	}
	var updates []telegram.Update
	if err := json.Unmarshal(fixture, &updates); err != nil {
		t.Fatal(err)
	}
	destination := make(chan telegram.Message, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/players/123456789":
			fmt.Fprint(w, `{"profile":{"account_id":123456789,"personaname":"HTTP Player"}}`)
		case "/bot123:test/getUpdates":
			var p struct {
				Offset int64 `json:"offset"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}
			if p.Offset == 0 {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": updates})
			} else {
				if p.Offset != 2 {
					t.Errorf("offset %d", p.Offset)
				}
				cancel()
				fmt.Fprint(w, `{"ok":true,"result":[]}`)
			}
		case "/bot123:test/sendMessage":
			var p struct {
				ChatID   int64  `json:"chat_id"`
				ThreadID *int64 `json:"message_thread_id"`
				Text     string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}
			destination <- telegram.Message{Chat: telegram.Chat{ID: p.ChatID}, ThreadID: p.ThreadID, Text: p.Text}
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":123}}`)
		default:
			t.Errorf("unexpected HTTP request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	api, err := opendota.New(opendota.Options{BaseURL: server.URL + "/api", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	tg, err := telegram.New(telegram.Options{Token: "123:test", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()
	repo := postgres.New(pool)
	h := Handler{Repo: repo, Telegram: tg, API: api}
	if err := tg.Poll(ctx, h.Handle, nil); err != nil {
		t.Fatal(err)
	}
	reply := <-destination
	original := updates[0].Message
	if reply.Chat.ID != original.Chat.ID || reply.ThreadID == nil || *reply.ThreadID != *original.ThreadID || !strings.Contains(reply.Text, "Добавлен") {
		t.Fatal(reply)
	}
	// Read with a fresh context: polling intentionally canceled its own scope.
	topic, err := repo.Topic(context.Background(), original.Chat.ID, original.ThreadID)
	if err != nil {
		t.Fatal(err)
	}
	players, err := repo.Players(context.Background(), topic.ID)
	if err != nil || len(players) != 1 || players[0].AccountID != 123456789 || players[0].Label() != "mid" {
		t.Fatal(players, err)
	}
}

func TestLastCommandSplitsLongHTMLWithoutDroppingSharedPlayers(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	repo := postgres.New(pool)
	tg := &fakeTelegram{}
	topic, err := repo.EnsureTopic(ctx, -1, ref(int64(42)), nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []int64{123, 456, 789, 790, 791, 792, 793, 794, 795, 796} {
		id, err := repo.UpsertPlayer(ctx, account, "Player", nil)
		if err != nil {
			t.Fatal(err)
		}
		alias := strings.Repeat("Игрок😀 & ", 25)
		if _, err := repo.AddPlayer(ctx, topic.ID, id, &alias, nil); err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC()
		match := domain.Match{PlayerID: id, ID: 1000, StartTime: at.Add(-time.Hour), EndTime: at, HeroID: 74}
		if _, err := repo.SaveMatches(ctx, []domain.Match{match}); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler{Repo: repo, Telegram: tg}
	if err := h.Handle(ctx, telegram.Message{Text: "/last 1 123", Chat: telegram.Chat{ID: -1, Type: "supergroup"}, ThreadID: ref(int64(42))}); err != nil {
		t.Fatal(err)
	}
	if len(tg.messages) < 2 {
		t.Fatal("long shared match was not split")
	}
	for i, text := range tg.messages {
		length, err := format.HTMLLength(text)
		if err != nil || length > format.TelegramTextLimit {
			t.Fatal(length, err)
		}
		if _, err := format.SplitHTML(text, format.TelegramTextLimit); err != nil {
			t.Fatal(err)
		}
		if tg.destinations[i].ThreadID == nil || *tg.destinations[i].ThreadID != 42 {
			t.Fatal(tg.destinations[i])
		}
	}
	joined := strings.Join(tg.messages, "")
	if !strings.Contains(joined, "/players/123") || !strings.Contains(joined, "/players/456") || strings.Count(joined, "/matches/1000") != 1 {
		t.Fatal("shared participants or match link lost")
	}
}
