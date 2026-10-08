package opendota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *[]time.Duration) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	now := time.Date(2026, 3, 11, 8, 1, 54, 0, time.UTC)
	var delays []time.Duration
	c, err := New(Options{BaseURL: server.URL + "/api", APIKey: "example-key", HTTPClient: server.Client(), Now: func() time.Time { return now }, Wait: func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		now = now.Add(d)
		return ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, &delays
}

func TestEndpointsParametersAndMatchMetadata(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "example-key" {
			t.Error("missing API key")
		}
		switch r.URL.Path {
		case "/api/players/123":
			fmt.Fprint(w, `{"profile":{"account_id":"123","personaname":"Example","profileurl":null}}`)
		case "/api/players/123/recentMatches":
			fmt.Fprint(w, `[]`)
		case "/api/players/123/matches":
			if r.URL.Query().Get("date") != "7" || r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("offset") != "200" {
				t.Error(r.URL.RawQuery)
			}
			fmt.Fprint(w, `[]`)
		case "/api/matches/999":
			fmt.Fprint(w, `{"match_id":999,"start_time":1773152700,"duration":2100,"radiant_win":false,"game_mode":22,"lobby_type":7,"players":[{"account_id":123,"player_slot":128,"hero_id":74,"kills":5,"deaths":5,"assists":10,"gold_per_min":0}]}`)
		case "/api/constants/heroes":
			fmt.Fprint(w, `{"74":{"localized_name":"Invoker"}}`)
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	p, err := c.Profile(ctx, 123)
	if err != nil || p.AccountID != 123 || p.Name != "Example" {
		t.Fatalf("profile %#v: %v", p, err)
	}
	if _, err := c.RecentMatches(ctx, 123); err != nil {
		t.Fatal(err)
	}
	if _, err := c.History(ctx, 123, 7, 100, 200); err != nil {
		t.Fatal(err)
	}
	players, err := c.MatchPlayers(ctx, 999)
	if err != nil || len(players) != 1 {
		t.Fatalf("players %#v: %v", players, err)
	}
	m, err := players[0].Snapshot(1)
	if err != nil || m.ID != 999 || m.Duration() != 35*time.Minute || !m.IsWin() || len(m.PresentMetrics) != 1 || m.PresentMetrics[0] != "gpm" {
		t.Fatalf("snapshot %#v: %v", m, err)
	}
	if _, err := c.Constants(ctx, "heroes"); err != nil {
		t.Fatal(err)
	}
}

func TestRetriesRespectQuotaAndRetryAfter(t *testing.T) {
	var calls int
	c, delays := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Date", "Wed, 11 Mar 2026 08:01:54 GMT")
		if calls == 1 {
			w.Header().Set("X-Rate-Limit-Remaining-Minute", "0")
			w.Header().Set("Retry-After", "8")
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"limited"}`)
			return
		}
		fmt.Fprint(w, `{"74":{"localized_name":"Invoker"}}`)
	})
	if _, err := c.Constants(context.Background(), "heroes"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(*delays) != 2 || (*delays)[1] != 8*time.Second {
		t.Fatalf("calls %d delays %v", calls, *delays)
	}
	snapshot := c.Snapshot()
	*snapshot.ServerTime = snapshot.ServerTime.Add(time.Hour)
	if c.Snapshot().ServerTime.Equal(*snapshot.ServerTime) {
		t.Fatal("snapshot aliases mutable state")
	}
}
func TestDoesNotRetryPermanentHTTPOrInvalidJSON(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{400, `example-key`}, {200, `{broken`}, {200, `[]`}} {
		t.Run(fmt.Sprint(tc.status, tc.body), func(t *testing.T) {
			calls := 0
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			_, err := c.Constants(context.Background(), "heroes")
			if err == nil || calls != 1 {
				t.Fatalf("err %v calls %d", err, calls)
			}
			if strings.Contains(err.Error(), "example-key") {
				t.Fatal("body leaked")
			}
		})
	}
}
func TestExhaustsTemporaryErrors(t *testing.T) {
	calls := 0
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503) })
	_, err := c.Constants(context.Background(), "heroes")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 503 || calls != 3 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}
func TestQuotaWaitCanBeCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("X-Rate-Limit-Remaining-Day", "0")
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Constants(context.Background(), "heroes"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := c.Constants(ctx, "heroes"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestConcurrentCallsAndSnapshots(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Rate-Limit-Remaining-Minute", "60")
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	c, err := New(Options{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, err := c.Constants(context.Background(), "heroes"); err != nil {
				t.Error(err)
			}
			_ = c.Snapshot()
		})
	}
	wg.Wait()
	if calls.Load() != 10 {
		t.Fatal(calls.Load())
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNetworkErrorDoesNotExposeAPIKey(t *testing.T) {
	hc := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "dial", URL: r.URL.String(), Err: errors.New("connection unavailable")}
	})}
	c, err := New(Options{BaseURL: "https://example.invalid/api", APIKey: "example-key", HTTPClient: hc, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Constants(context.Background(), "heroes")
	if err == nil || strings.Contains(err.Error(), "example-key") {
		t.Fatalf("unsafe error: %v", err)
	}
}
func TestInvalidInputsDoNotMakeRequests(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected request") })
	ctx := context.Background()
	for _, fn := range []func() error{func() error { _, e := c.Profile(ctx, 0); return e }, func() error { _, e := c.History(ctx, 1, 366, 100, 0); return e }, func() error { _, e := c.Constants(ctx, "../secret"); return e }, func() error { _, e := c.MatchPlayers(ctx, 0); return e }} {
		if err := fn(); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}
func TestSparseMatchAndValidation(t *testing.T) {
	var matches []MatchData
	b, err := os.ReadFile("../../testdata/opendota-recent.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &matches); err != nil {
		t.Fatal(err)
	}
	m, err := matches[0].Snapshot(1)
	if err != nil || m.GPM != 0 || m.PresentMetrics == nil || len(m.PresentMetrics) != 0 {
		t.Fatalf("sparse snapshot %#v: %v", m, err)
	}
	matches[0].RadiantWin = nil
	if _, err := matches[0].Snapshot(1); err == nil {
		t.Fatal("accepted missing required field")
	}
}

func TestMatchPlayerPrefersAccountAndFallsBackToSlot(t *testing.T) {
	var players []MatchData
	if err := json.Unmarshal([]byte(`[{"account_id":"123","player_slot":1},{"account_id":456,"player_slot":0},{"player_slot":128}]`), &players); err != nil {
		t.Fatal(err)
	}
	p, ok := SelectMatchPlayer(players, 123, 0)
	if !ok || p.PlayerSlot == nil || *p.PlayerSlot != 1 {
		t.Fatal(p, ok)
	}
	p, ok = SelectMatchPlayer(players, 999, 128)
	if !ok || p.PlayerSlot == nil || *p.PlayerSlot != 128 {
		t.Fatal(p, ok)
	}
	if _, ok := SelectMatchPlayer(players, 999, 129); ok {
		t.Fatal("invented participant")
	}
}
