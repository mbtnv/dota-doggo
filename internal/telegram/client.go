// Package telegram is a narrow HTTP Bot API adapter with cancelable polling.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/format"
	"dota-doggo/internal/logging"
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}
type Chat struct {
	ID    int64   `json:"id"`
	Type  string  `json:"type"`
	Title *string `json:"title"`
}
type Message struct {
	ID       int64  `json:"message_id"`
	ThreadID *int64 `json:"message_thread_id"`
	From     *User  `json:"from"`
	Chat     Chat   `json:"chat"`
	Text     string `json:"text"`
}
type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}
type Options struct {
	Token, BaseURL, ProxyURL string
	HTTPClient               *http.Client
	MaxAttempts              int
	Backoff                  time.Duration
	Wait                     func(context.Context, time.Duration) error
}
type Client struct {
	base, token string
	http        *http.Client
	attempts    int
	backoff     time.Duration
	wait        func(context.Context, time.Duration) error
}
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string   { return fmt.Sprintf("Telegram API %d: %s", e.Code, e.Description) }
func (e *APIError) Temporary() bool { return e.Code == 429 || e.Code >= 500 }

func New(o Options) (*Client, error) {
	if o.Token == "" || strings.ContainsAny(o.Token, "/?# \t\r\n") {
		return nil, errors.New("invalid Telegram bot token")
	}
	if o.BaseURL == "" {
		o.BaseURL = "https://api.telegram.org"
	}
	u, err := url.Parse(o.BaseURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid Telegram base URL")
	}
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	if o.Backoff == 0 {
		o.Backoff = time.Second
	}
	if o.MaxAttempts < 1 || o.Backoff < 0 {
		return nil, errors.New("invalid Telegram retry configuration")
	}
	if o.HTTPClient != nil && o.ProxyURL != "" {
		return nil, errors.New("provide either Telegram HTTP client or proxy, not both")
	}
	if o.HTTPClient == nil {
		o.HTTPClient, err = HTTPClient(o.ProxyURL)
		if err != nil {
			return nil, err
		}
	}
	hc := *o.HTTPClient
	if hc.Timeout == 0 {
		hc.Timeout = 45 * time.Second
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if o.Wait == nil {
		o.Wait = wait
	}
	return &Client{base: strings.TrimRight(o.BaseURL, "/"), token: o.Token, http: &hc, attempts: o.MaxAttempts, backoff: o.Backoff, wait: o.Wait}, nil
}
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
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

func (c *Client) call(ctx context.Context, method string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return errors.New("invalid Telegram payload")
	}
	var last error
	var nextDelay time.Duration
	for attempt := 0; attempt < c.attempts; attempt++ {
		if err := c.wait(ctx, nextDelay); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
		if err != nil {
			return errors.New("invalid Telegram request")
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := c.http.Do(req)
		if err != nil {
			for {
				var ue *url.Error
				if !errors.As(err, &ue) {
					break
				}
				err = ue.Err
			}
			last = fmt.Errorf("Telegram request failed: %w", err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			nextDelay = c.retryDelay(attempt + 1)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
		_ = res.Body.Close()
		if len(raw) > 4<<20 {
			return errors.New("Telegram response exceeds size limit")
		}
		if readErr != nil {
			last = fmt.Errorf("read Telegram response: %w", readErr)
			nextDelay = c.retryDelay(attempt + 1)
			continue
		}
		var response struct {
			OK          bool            `json:"ok"`
			Result      json.RawMessage `json:"result"`
			Code        int             `json:"error_code"`
			Description string          `json:"description"`
			Parameters  struct {
				RetryAfter int64 `json:"retry_after"`
			} `json:"parameters"`
		}
		decodeErr := json.Unmarshal(raw, &response)
		if decodeErr == nil && response.OK && res.StatusCode >= 200 && res.StatusCode < 300 {
			if out == nil {
				return nil
			}
			if len(response.Result) == 0 || string(response.Result) == "null" {
				return errors.New("missing Telegram result")
			}
			if err := json.Unmarshal(response.Result, out); err != nil {
				return errors.New("invalid Telegram result")
			}
			return nil
		}
		if response.Code == 0 {
			response.Code = res.StatusCode
		}
		if response.Code >= 200 && response.Code < 300 {
			return errors.New("invalid Telegram API response")
		}
		if response.Description == "" {
			response.Description = "request failed"
		}
		pause := time.Duration(min(max(response.Parameters.RetryAfter, 0), 86400)) * time.Second
		ae := &APIError{Code: response.Code, Description: logging.Redact(response.Description, c.token), RetryAfter: pause}
		last = ae
		if !ae.Temporary() {
			return ae
		}
		nextDelay = max(c.retryDelay(attempt+1), ae.RetryAfter)
	}
	return last
}
func (c *Client) Me(ctx context.Context) (User, error) {
	var user User
	err := c.call(ctx, "getMe", struct{}{}, &user)
	return user, err
}
func (c *Client) Updates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	if timeout < 0 || timeout > 30 {
		return nil, errors.New("invalid long polling timeout")
	}
	var updates []Update
	err := c.call(ctx, "getUpdates", struct {
		Offset  int64    `json:"offset"`
		Timeout int      `json:"timeout"`
		Allowed []string `json:"allowed_updates"`
	}{offset, timeout, []string{"message"}}, &updates)
	return updates, err
}
func (c *Client) IsAdmin(ctx context.Context, chatID, userID int64) (bool, error) {
	var members []struct {
		User   User   `json:"user"`
		Status string `json:"status"`
	}
	if err := c.call(ctx, "getChatAdministrators", struct {
		ChatID int64 `json:"chat_id"`
	}{chatID}, &members); err != nil {
		return false, err
	}
	for _, member := range members {
		if member.User.ID == userID && (member.Status == "administrator" || member.Status == "creator") {
			return true, nil
		}
	}
	return false, nil
}

func (c *Client) SendParts(ctx context.Context, topic domain.Topic, parts []string) ([]int64, error) {
	// Validate the whole batch before causing any partial send.
	for _, part := range parts {
		validated, err := format.SplitHTML(part, format.TelegramTextLimit)
		if err != nil || len(validated) > 1 || (part != "" && len(validated) == 0) {
			return nil, errors.New("invalid Telegram HTML message")
		}
	}
	var ids []int64
	for _, part := range parts {
		if part == "" {
			continue
		}
		var message Message
		err := c.call(ctx, "sendMessage", struct {
			ChatID    int64  `json:"chat_id"`
			ThreadID  *int64 `json:"message_thread_id,omitempty"`
			Text      string `json:"text"`
			ParseMode string `json:"parse_mode"`
			Preview   struct {
				Disabled bool `json:"is_disabled"`
			} `json:"link_preview_options"`
		}{topic.ChatID, topic.ThreadID, part, "HTML", struct {
			Disabled bool `json:"is_disabled"`
		}{true}}, &message)
		if err != nil {
			return ids, err
		}
		if message.ID <= 0 {
			return ids, errors.New("invalid Telegram message ID")
		}
		ids = append(ids, message.ID)
	}
	return ids, nil
}
func (c *Client) SendHTML(ctx context.Context, topic domain.Topic, text string) ([]int64, error) {
	parts, err := format.SplitHTML(text, format.TelegramTextLimit)
	if err != nil {
		return nil, err
	}
	return c.SendParts(ctx, topic, parts)
}

// Poll acknowledges each update after dispatch, skips duplicates within a batch,
// and backs off on temporary errors. A permanent API failure (e.g. conflict with
// another poller) is returned to the process rather than retried forever.
func (c *Client) Poll(ctx context.Context, handle func(context.Context, Message) error, log *slog.Logger) error {
	return c.PollObserved(ctx, handle, log, nil)
}

// PollObserved reports completed getUpdates attempts to the process monitor.
// Command failures do not mean that receiving updates has stopped working.
func (c *Client) PollObserved(ctx context.Context, handle func(context.Context, Message) error, log *slog.Logger, observe func(error)) error {
	var offset int64
	delay := c.retryDelay(1)
	for ctx.Err() == nil {
		updates, err := c.Updates(ctx, offset, 30)
		if observe != nil {
			observe(err)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ae *APIError
			if errors.As(err, &ae) && !ae.Temporary() {
				return err
			}
			if log != nil {
				log.Warn("Telegram polling failed", "error", err)
			}
			if err := c.wait(ctx, delay); err != nil {
				return nil
			}
			delay = min(delay*2, 60*time.Second)
			continue
		}
		delay = c.retryDelay(1)
		sort.SliceStable(updates, func(i, j int) bool { return updates[i].ID < updates[j].ID })
		for _, update := range updates {
			if ctx.Err() != nil {
				return nil
			}
			if update.ID < offset {
				continue
			}
			if update.Message != nil {
				if err := handle(ctx, *update.Message); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					if log != nil {
						log.Error("Telegram command failed", "update_id", update.ID, "error", err)
					}
				}
			}
			offset = update.ID + 1
		}
	}
	return nil
}
