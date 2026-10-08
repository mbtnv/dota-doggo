package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
)

func client(t *testing.T, handler http.HandlerFunc, options Options) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	options.Token, options.BaseURL = "123:test-secret", s.URL
	c, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNetworkRetriesBackoffAndRedactsToken(t *testing.T) {
	var delays []time.Duration
	calls := 0
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, &url.Error{Op: "dial", URL: r.URL.String(), Err: errors.New("connection refused")}
	})}
	c, err := New(Options{Token: "123:test-secret", HTTPClient: hc, MaxAttempts: 5, Wait: func(ctx context.Context, d time.Duration) error {
		if d > 0 {
			delays = append(delays, d)
		}
		return ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Me(context.Background())
	if err == nil || strings.Contains(err.Error(), "test-secret") || calls != 5 || !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}) {
		t.Fatal(err, calls, delays)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Me(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestSendHTMLSplitsAndInvalidBatchMakesNoRequests(t *testing.T) {
	var texts []string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		texts = append(texts, p.Text)
		reply(w, Message{ID: int64(len(texts))})
	}, Options{})
	if _, err := c.SendParts(context.Background(), domain.Topic{}, []string{"valid", "<b>unclosed"}); err == nil || len(texts) != 0 {
		t.Fatal(err, texts)
	}
	ids, err := c.SendHTML(context.Background(), domain.Topic{}, "<b>"+strings.Repeat("😀&amp;", 2000)+"</b>")
	if err != nil || len(ids) != 2 || len(texts) != 2 {
		t.Fatal(ids, err)
	}
	for _, text := range texts {
		if !strings.HasPrefix(text, "<b>") || !strings.HasSuffix(text, "</b>") {
			t.Fatal(text)
		}
	}
}
func reply(w http.ResponseWriter, result any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}
func TestSendPartsRetriesOnlyFailedPartAndRoutesTopic(t *testing.T) {
	var texts []string
	var waits []time.Duration
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/bot123:test-secret/sendMessage" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		var p struct {
			ChatID, ThreadID int64
			Text, ParseMode  string
			Preview          map[string]bool
		}
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Error(err)
		}
		_ = json.Unmarshal(raw["chat_id"], &p.ChatID)
		_ = json.Unmarshal(raw["message_thread_id"], &p.ThreadID)
		_ = json.Unmarshal(raw["text"], &p.Text)
		_ = json.Unmarshal(raw["parse_mode"], &p.ParseMode)
		_ = json.Unmarshal(raw["link_preview_options"], &p.Preview)
		if p.ChatID != -100 || p.ThreadID != 42 || p.ParseMode != "HTML" || !p.Preview["is_disabled"] {
			t.Errorf("payload %#v", p)
		}
		texts = append(texts, p.Text)
		if len(texts) == 2 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":7}}`)
			return
		}
		reply(w, Message{ID: int64(len(texts))})
	}, Options{Wait: func(ctx context.Context, d time.Duration) error {
		if d > 0 {
			waits = append(waits, d)
		}
		return ctx.Err()
	}})
	thread := int64(42)
	ids, err := c.SendParts(context.Background(), domain.Topic{ChatID: -100, ThreadID: &thread}, []string{"<b>one</b>", "two"})
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 3}) || !reflect.DeepEqual(texts, []string{"<b>one</b>", "two", "two"}) || !reflect.DeepEqual(waits, []time.Duration{7 * time.Second}) {
		t.Fatalf("ids=%v texts=%v waits=%v err=%v", ids, texts, waits, err)
	}
}
func TestTelegramPermanentFailureReturnsPartialIDs(t *testing.T) {
	count := 0
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			reply(w, Message{ID: 10})
			return
		}
		w.WriteHeader(403)
		fmt.Fprint(w, `{"ok":false,"error_code":403,"description":"denied 123:test-secret"}`)
	}, Options{})
	ids, err := c.SendParts(context.Background(), domain.Topic{}, []string{"one", "two", "three"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != 403 || strings.Contains(err.Error(), "test-secret") || !reflect.DeepEqual(ids, []int64{10}) || count != 2 {
		t.Fatalf("%v %v %d", ids, err, count)
	}
}
func TestPollingDispatchesOrderedUniqueUpdatesAndRecovers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	round := 0
	var handled []int64
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		round++
		var p struct {
			Offset  int64    `json:"offset"`
			Timeout int      `json:"timeout"`
			Allowed []string `json:"allowed_updates"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if p.Timeout != 30 || !reflect.DeepEqual(p.Allowed, []string{"message"}) {
			t.Errorf("poll payload %#v", p)
		}
		switch round {
		case 1:
			w.WriteHeader(502)
		case 2:
			if p.Offset != 0 {
				t.Errorf("early offset %d", p.Offset)
			}
			reply(w, []Update{{ID: 12, Message: &Message{ID: 12}}, {ID: 10, Message: &Message{ID: 10}}, {ID: 10, Message: &Message{ID: 10}}, {ID: 11}})
		default:
			if p.Offset != 13 {
				t.Errorf("offset %d", p.Offset)
			}
			cancel()
			reply(w, []Update{})
		}
	}, Options{MaxAttempts: 1, Wait: func(ctx context.Context, d time.Duration) error { return ctx.Err() }})
	err := c.Poll(ctx, func(ctx context.Context, m Message) error {
		handled = append(handled, m.ID)
		return errors.New("command failed")
	}, nil)
	if err != nil || !reflect.DeepEqual(handled, []int64{10, 12}) {
		t.Fatalf("handled %v: %v", handled, err)
	}
}
func TestPollingPermanentConflictStops(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		fmt.Fprint(w, `{"ok":false,"error_code":409}`)
	}, Options{})
	var ae *APIError
	if err := c.Poll(context.Background(), func(context.Context, Message) error { t.Fatal("dispatch"); return nil }, nil); !errors.As(err, &ae) || ae.Code != 409 {
		t.Fatalf("error %v", err)
	}
}
func TestTelegramIdentityAdminsAndInvalidResponses(t *testing.T) {
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			reply(w, User{ID: 7, Username: "doggo_bot", IsBot: true})
		case strings.HasSuffix(r.URL.Path, "/getChatAdministrators"):
			reply(w, []any{map[string]any{"user": User{ID: 9}, "status": "creator"}, map[string]any{"user": User{ID: 10}, "status": "member"}})
		default:
			fmt.Fprint(w, `{invalid`)
		}
	}, Options{})
	me, err := c.Me(context.Background())
	if err != nil || me.Username != "doggo_bot" {
		t.Fatal(me, err)
	}
	for _, id := range []int64{9, 10, 11} {
		yes, err := c.IsAdmin(context.Background(), -1, id)
		if err != nil || yes != (id == 9) {
			t.Fatal(id, yes, err)
		}
	}
	if _, err := c.Updates(context.Background(), 0, 0); err == nil {
		t.Fatal("invalid response accepted")
	}
	if _, err := c.SendHTML(context.Background(), domain.Topic{}, "<b>unclosed"); err == nil {
		t.Fatal("invalid HTML accepted")
	}
}
