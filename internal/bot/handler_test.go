package bot

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/telegram"
)

func TestCommandMentionsAndArguments(t *testing.T) {
	for _, tc := range []struct {
		text, command, args string
		ok                  bool
	}{
		{" /TRACK@Doggo_Bot 123 Mid carry ", "track", "123 Mid carry", true},
		{"/track@other_bot 123", "", "", false}, {"hello /help", "", "", false},
		{"/help", "help", "", true}, {"/last\t5\nmid carry", "last", "5\nmid carry", true},
	} {
		c, a, ok := Command(tc.text, "doggo_bot")
		if c != tc.command || a != tc.args || ok != tc.ok {
			t.Fatalf("%q => %q %q %t", tc.text, c, a, ok)
		}
	}
	for _, text := range []string{"123", "https://www.dotabuff.com/players/123/", "https://www.opendota.com/players/123?x=y"} {
		id, err := AccountID(text)
		if err != nil || id != 123 {
			t.Fatal(text, id, err)
		}
	}
	for _, text := range []string{"0", "-1", "4294967296", "https://x/123", "file://x/players/123", "https://x/players/x"} {
		if _, err := AccountID(text); err == nil {
			t.Fatal(text)
		}
	}
	selected, message := choose([]domain.Player{{ID: 1, AccountID: 123, Alias: ref("same")}, {ID: 2, AccountID: 456, Alias: ref("same")}}, "same")
	if selected != nil || !strings.Contains(message, "неоднозначен") {
		t.Fatal(selected, message)
	}
}
func ref[T any](v T) *T { return &v }

type fakeTelegram struct {
	messages     []string
	destinations []domain.Topic
	admin        bool
	adminErr     error
	checks       int
}

func (f *fakeTelegram) SendHTML(ctx context.Context, topic domain.Topic, text string) ([]int64, error) {
	f.messages = append(f.messages, text)
	f.destinations = append(f.destinations, topic)
	return []int64{int64(len(f.messages))}, nil
}
func (f *fakeTelegram) SendParts(ctx context.Context, topic domain.Topic, parts []string) ([]int64, error) {
	var ids []int64
	for _, p := range parts {
		v, err := f.SendHTML(ctx, topic, p)
		if err != nil {
			return ids, err
		}
		ids = append(ids, v...)
	}
	return ids, nil
}
func (f *fakeTelegram) IsAdmin(context.Context, int64, int64) (bool, error) {
	f.checks++
	return f.admin, f.adminErr
}

type fakeRepository struct {
	Repository
	calls int
}

func (r *fakeRepository) EnsureTopic(context.Context, int64, *int64, *string, string) (domain.Topic, error) {
	r.calls++
	return domain.Topic{}, errors.New("stop after permission check")
}
func TestPermissionsAndPrivateRouting(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		from                               *telegram.User
		adminCheck, admin, allow, adminErr bool
		chatType                           string
		wantCalls, wantChecks              int
	}{
		{"missing user", nil, false, false, false, false, "group", 0, 0},
		{"disabled", &telegram.User{ID: 7}, false, false, false, false, "group", 1, 0},
		{"allowlist", &telegram.User{ID: 7}, true, false, true, false, "group", 1, 0},
		{"administrator", &telegram.User{ID: 7}, true, true, false, false, "supergroup", 1, 1},
		{"member", &telegram.User{ID: 7}, true, false, false, false, "group", 0, 1},
		{"lookup failure", &telegram.User{ID: 7}, true, false, false, true, "group", 0, 1},
		{"private", &telegram.User{ID: 7}, false, false, false, false, "private", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := &fakeTelegram{admin: tc.admin}
			if tc.adminErr {
				tg.adminErr = errors.New("network")
			}
			repo := &fakeRepository{}
			h := Handler{Repo: repo, Telegram: tg, AdminCheck: tc.adminCheck, AllowedUserIDs: map[int64]bool{7: tc.allow}}
			_ = h.Handle(context.Background(), telegram.Message{Chat: telegram.Chat{ID: -1, Type: tc.chatType}, ThreadID: ref(int64(42)), From: tc.from, Text: "/pause"})
			if repo.calls != tc.wantCalls || tg.checks != tc.wantChecks {
				t.Fatal(repo.calls, tg.checks)
			}
			for _, dest := range tg.destinations {
				if dest.ChatID != -1 || !reflect.DeepEqual(dest.ThreadID, ref(int64(42))) {
					t.Fatal(dest)
				}
			}
		})
	}
}
