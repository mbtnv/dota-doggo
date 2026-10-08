//go:build integration

package bot

import (
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/store/postgres"
	"dota-doggo/internal/testdb"
)

func TestStatusShowsTopicScheduleDurationAndReportBounds(t *testing.T) {
	ctx, _, pool := testdb.Open(t, true)
	repo := postgres.New(pool)
	topic, err := repo.EnsureTopic(ctx, -100, nil, ref("Dota <friends>"), "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	if err := repo.MarkFinished(ctx, topic.ID, now.Add(-10*time.Second), now, nil); err != nil {
		t.Fatal(err)
	}
	start, end, err := domain.Day.Bounds(now, topic.Timezone, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordReport(ctx, topic.ID, domain.ReportRun{Period: "day", Trigger: "auto", Start: start, End: end}); err != nil {
		t.Fatal(err)
	}
	tg := &fakeTelegram{}
	h := Handler{Repo: repo, Telegram: tg, PollInterval: 5 * time.Minute, Now: func() time.Time { return now }}
	if err := h.status(ctx, topic, nil); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(tg.messages, "\n")
	for _, want := range []string{"Dota &lt;friends&gt;", "Создан:", "Интервал опроса: 5m0s", "Длительность опроса: 10s", "Следующий опрос: не раньше 2026-10-08 15:05", "Период автоотчёта day: 2026-10-07 00:00", "Период автоотчёта week:", "Период автоотчёта month:"} {
		if !strings.Contains(text, want) {
			t.Fatal(text, want)
		}
	}
	if err := repo.MarkStarted(ctx, topic.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := h.status(ctx, topic, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tg.messages[len(tg.messages)-1], "Опрос выполняется") {
		t.Fatal(tg.messages)
	}
	topic.Paused = true
	if err := h.status(ctx, topic, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tg.messages[len(tg.messages)-1], "Следующий опрос: на паузе") {
		t.Fatal(tg.messages)
	}
}
