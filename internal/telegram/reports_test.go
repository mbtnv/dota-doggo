package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/format"
)

func richSnapshot(t *testing.T, html string) string {
	t.Helper()
	b, err := json.Marshal(format.ReportPart{Format: format.RichHTMLFormat, HTML: html})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRichReportsUseTelegramRichPayloadAndRetryOnlyFailedPart(t *testing.T) {
	var methods, contents []string
	var waits []time.Duration
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			ChatID   int64  `json:"chat_id"`
			ThreadID int64  `json:"message_thread_id"`
			Text     string `json:"text"`
			Rich     struct {
				HTML string `json:"html"`
				Skip bool   `json:"skip_entity_detection"`
			} `json:"rich_message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		if p.ChatID != -100 || p.ThreadID != 42 || p.Text != "" || !p.Rich.Skip {
			t.Errorf("payload %+v", p)
		}
		methods = append(methods, r.URL.Path)
		contents = append(contents, p.Rich.HTML)
		if len(contents) == 2 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":3}}`)
			return
		}
		reply(w, Message{ID: int64(len(contents))})
	}, Options{Wait: func(ctx context.Context, d time.Duration) error {
		if d > 0 {
			waits = append(waits, d)
		}
		return ctx.Err()
	}})
	thread := int64(42)
	one, two := "<h2>Report</h2><table><tr><th>Игрок</th></tr><tr><td>🐕</td></tr></table>", "<details><summary>Второй</summary><p>2–1</p></details>"
	ids, err := c.SendReportParts(context.Background(), domain.Topic{ChatID: -100, ThreadID: &thread}, []string{richSnapshot(t, one), richSnapshot(t, two)})
	if err != nil || !reflect.DeepEqual(ids, []int64{1, 3}) || !reflect.DeepEqual(contents, []string{one, two, two}) || !reflect.DeepEqual(waits, []time.Duration{3 * time.Second}) {
		t.Fatal(ids, contents, waits, err)
	}
	for _, method := range methods {
		if method != "/bot123:test-secret/sendRichMessage" {
			t.Fatal("wrong Telegram method", method)
		}
	}
}

func TestReportBatchValidationMakesNoPartialSends(t *testing.T) {
	calls := 0
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		reply(w, Message{ID: 1})
	}, Options{})
	for _, bad := range []string{`{"format":"unknown","html":"x"}`, richSnapshot(t, "<table><tr><td>broken</table>"), "<b>unclosed", ""} {
		if _, err := c.SendReportParts(context.Background(), domain.Topic{}, []string{richSnapshot(t, "<h2>Valid</h2>"), bad}); err == nil {
			t.Fatal("bad batch accepted", bad)
		}
	}
	if calls != 0 {
		t.Fatal("invalid batch made requests", calls)
	}
}

func TestReportSnapshotsKeepLegacyTransportAndReturnPartialProgress(t *testing.T) {
	var methods []string
	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.URL.Path)
		if len(methods) == 2 {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"ok":false,"error_code":403,"description":"forbidden"}`)
			return
		}
		reply(w, Message{ID: 10})
	}, Options{})
	ids, err := c.SendReportParts(context.Background(), domain.Topic{}, []string{"<b>Legacy daily report</b>", richSnapshot(t, "<h2>New report</h2>"), "never sent"})
	if err == nil || !reflect.DeepEqual(ids, []int64{10}) || !reflect.DeepEqual(methods, []string{"/bot123:test-secret/sendMessage", "/bot123:test-secret/sendRichMessage"}) {
		t.Fatal(ids, methods, err)
	}
}
