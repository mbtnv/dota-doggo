// Package opendota implements bounded, cancelable HTTP access to the OpenDota API.
package opendota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxResponseBytes = 16 << 20

type Options struct {
	BaseURL, APIKey string
	HTTPClient      *http.Client
	MaxAttempts     int
	Backoff         time.Duration
	Now             func() time.Time
	Wait            func(context.Context, time.Duration) error
}
type Client struct {
	base     *url.URL
	key      string
	http     *http.Client
	attempts int
	backoff  time.Duration
	now      func() time.Time
	wait     func(context.Context, time.Duration) error
	gate     chan struct{}
	mu       sync.Mutex
	limits   *RateLimits
	next     time.Time
}
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("OpenDota returned HTTP %d", e.Status) }

func New(o Options) (*Client, error) {
	base, err := url.Parse(o.BaseURL)
	if err != nil || base.Hostname() == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.Fragment != "" {
		return nil, errors.New("invalid OpenDota base URL")
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	if o.Backoff == 0 {
		o.Backoff = time.Second
	}
	if o.MaxAttempts < 1 || o.Backoff < 0 {
		return nil, errors.New("invalid OpenDota retry configuration")
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 30 * time.Second, Transport: http.DefaultTransport.(*http.Transport).Clone()}
	}
	hc := *o.HTTPClient
	if hc.Timeout == 0 {
		hc.Timeout = 30 * time.Second
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Wait == nil {
		o.Wait = Wait
	}
	base.Path = strings.TrimRight(base.Path, "/")
	return &Client{base: base, key: o.APIKey, http: &hc, attempts: o.MaxAttempts, backoff: o.Backoff, now: o.Now, wait: o.Wait, gate: make(chan struct{}, 1)}, nil
}
func Wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) retryDelay(retry int) time.Duration {
	d := min(c.backoff, 60*time.Second)
	for n := 1; n < retry && d < 60*time.Second; n++ {
		d = min(d*2, 60*time.Second)
	}
	return d
}
func (c *Client) Snapshot() *RateLimits {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyLimits(c.limits)
}
func (c *Client) RateLimits(ctx context.Context, refresh bool) (*RateLimits, error) {
	if refresh || c.Snapshot() == nil {
		if _, err := c.Constants(ctx, "lobby_type"); err != nil {
			return nil, err
		}
	}
	return c.Snapshot(), nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	u := *c.base
	u.Path += path
	q := u.Query()
	for k, vs := range params {
		q[k] = append([]string(nil), vs...)
	}
	if c.key != "" {
		q.Set("api_key", c.key)
	}
	u.RawQuery = q.Encode()
	var last error
	for attempt := 0; attempt < c.attempts; attempt++ {
		delay := max(c.next.Sub(c.now()), 0)
		if attempt > 0 {
			delay = max(delay, c.retryDelay(attempt))
		}
		if err := c.wait(ctx, delay); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return errors.New("invalid OpenDota request")
		}
		req.Header.Set("Accept", "application/json")
		response, err := c.http.Do(req)
		if err != nil {
			for {
				var ue *url.Error
				if !errors.As(err, &ue) {
					break
				}
				err = ue.Err
			}
			last = fmt.Errorf("OpenDota request failed: %w", err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		pause := retryAfter(response.Header, c.now())
		if snapshot := ParseLimits(response.Header); snapshot != nil {
			c.mu.Lock()
			c.limits = snapshot
			c.mu.Unlock()
			pause = max(pause, snapshot.RecommendedPause)
		}
		c.next = c.now().Add(pause)
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		_ = response.Body.Close()
		if len(body) > maxResponseBytes {
			return errors.New("OpenDota response exceeds size limit")
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			last = &HTTPError{Status: response.StatusCode}
			if response.StatusCode == 429 || response.StatusCode >= 500 {
				continue
			}
			return last
		}
		if readErr != nil {
			last = fmt.Errorf("read OpenDota response: %w", readErr)
			continue
		}
		if err := json.Unmarshal(body, out); err != nil {
			return errors.New("invalid JSON response from OpenDota")
		}
		return nil
	}
	return last
}

func (c *Client) Profile(ctx context.Context, id int64) (Profile, error) {
	if id <= 0 {
		return Profile{}, errors.New("invalid account ID")
	}
	var payload struct {
		Profile *struct {
			AccountID accountID `json:"account_id"`
			Name      string    `json:"personaname"`
			URL       *string   `json:"profileurl"`
		} `json:"profile"`
	}
	if err := c.get(ctx, fmt.Sprintf("/players/%d", id), nil, &payload); err != nil {
		return Profile{}, err
	}
	if payload.Profile == nil || int64(payload.Profile.AccountID) != id || payload.Profile.Name == "" {
		return Profile{}, errors.New("OpenDota profile is unavailable")
	}
	p := payload.Profile
	return Profile{AccountID: int64(p.AccountID), Name: p.Name, URL: p.URL}, nil
}
func (c *Client) RecentMatches(ctx context.Context, id int64) ([]MatchData, error) {
	if id <= 0 {
		return nil, errors.New("invalid account ID")
	}
	var out []MatchData
	err := c.get(ctx, fmt.Sprintf("/players/%d/recentMatches", id), nil, &out)
	return out, err
}
func (c *Client) History(ctx context.Context, id int64, days, limit, offset int) ([]MatchData, error) {
	if id <= 0 || days < 1 || days > 365 || limit < 1 || limit > 100 || offset < 0 {
		return nil, errors.New("invalid history parameters")
	}
	params := url.Values{"date": {strconv.Itoa(days)}, "limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	var out []MatchData
	err := c.get(ctx, fmt.Sprintf("/players/%d/matches", id), params, &out)
	return out, err
}
func (c *Client) MatchPlayers(ctx context.Context, id int64) ([]MatchData, error) {
	if id <= 0 {
		return nil, errors.New("invalid match ID")
	}
	var raw map[string]json.RawMessage
	if err := c.get(ctx, fmt.Sprintf("/matches/%d", id), nil, &raw); err != nil {
		return nil, err
	}
	if len(raw["players"]) == 0 || string(raw["players"]) == "null" {
		return nil, errors.New("match players are unavailable")
	}
	var root MatchData
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, errors.New("invalid match metadata")
	}
	if root.MatchID == nil {
		root.MatchID = &id
	}
	var players []MatchData
	if err := json.Unmarshal(raw["players"], &players); err != nil {
		return nil, errors.New("invalid match players")
	}
	for i := range players {
		players[i].inherit(root)
	}
	return players, nil
}
func (c *Client) Constants(ctx context.Context, resource string) (map[string]json.RawMessage, error) {
	switch resource {
	case "heroes", "game_mode", "lobby_type":
	default:
		return nil, errors.New("unknown constants resource")
	}
	var out map[string]json.RawMessage
	err := c.get(ctx, "/constants/"+resource, nil, &out)
	if err == nil && out == nil {
		err = errors.New("empty constants payload")
	}
	return out, err
}
