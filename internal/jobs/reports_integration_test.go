//go:build integration

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/service"
)

func TestAutoReportUsesLocalPreviousPeriodAndManualDoesNotSuppressIt(t *testing.T) {
	ctx, _, repo, _, sender, w := setup(t)
	topic, p := track(t, ctx, repo, -1, 123, "active", nil)
	track(t, ctx, repo, -1, 456, "empty", nil)
	if err := repo.SetTimezone(ctx, topic.ID, "America/New_York"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetPaused(ctx, topic.ID, true); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	w.Now = func() time.Time { return now }
	start, end, err := domain.Day.Bounds(now, "America/New_York", true)
	if err != nil {
		t.Fatal(err)
	}
	if end.Sub(start) != 23*time.Hour {
		t.Fatal(start, end)
	}
	m := domain.Match{PlayerID: p, ID: 100, StartTime: start.Add(30 * time.Minute), EndTime: start.Add(time.Hour), HeroID: 74}
	boundary := m
	boundary.ID = 101
	boundary.StartTime = end.Add(-time.Hour)
	boundary.EndTime = end
	if _, err := repo.SaveMatches(ctx, []domain.Match{m, boundary}); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordReport(ctx, topic.ID, domain.ReportRun{Period: "day", Trigger: "manual", Start: start, End: end}); err != nil {
		t.Fatal(err)
	}
	if err := w.ReportsOnce(ctx, domain.Day); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 1 || !sent[0].rich || !strings.Contains(sent[0].text, "<b>Матчи:</b> 1") || strings.Contains(sent[0].text, "empty") || !strings.Contains(sent[0].text, "08.03.2026") {
		t.Fatal(sent)
	}
	runs, err := repo.LatestReports(ctx, topic.ID)
	if err != nil || len(runs) != 1 || runs[0].Trigger != "auto" || runs[0].MessageID == nil || !runs[0].Start.Equal(start) || !runs[0].End.Equal(end) {
		t.Fatal(runs, err)
	}
	if err := w.ReportsOnce(ctx, domain.Day); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 1 {
		t.Fatal("duplicate auto report")
	}
}
func TestEmptyDayIsSealedSilentlyAndWeekMonthRemainAvailable(t *testing.T) {
	ctx, _, repo, _, sender, w := setup(t)
	topic, _ := track(t, ctx, repo, -1, 123, "Player", nil)
	for _, period := range []domain.Period{domain.Day, domain.Week, domain.Month} {
		if err := w.ReportsOnce(ctx, period); err != nil {
			t.Fatal(err)
		}
	}
	sent := sender.snapshot()
	if len(sent) != 2 || !strings.Contains(sent[0].text, "week report") || !strings.Contains(sent[1].text, "month report") {
		t.Fatal(sent)
	}
	runs, err := repo.LatestReports(ctx, topic.ID)
	if err != nil || len(runs) != 3 {
		t.Fatal(runs, err)
	}
	for _, r := range runs {
		if r.Period == "day" && r.MessageID != nil {
			t.Fatal("empty day sent a message")
		}
	}
	for _, period := range []domain.Period{domain.Day, domain.Week, domain.Month} {
		if err := w.ReportsOnce(ctx, period); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.snapshot()) != 2 {
		t.Fatal("repeat empty reports")
	}
	w.Now = func() time.Time { return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC) }
	if err := w.ReportsOnce(ctx, domain.Month); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 3 {
		t.Fatal("next month was suppressed")
	}
}

func TestPendingLegacyDailyReportResumesWithoutChangingFormat(t *testing.T) {
	ctx, _, repo, _, sender, w := setup(t)
	topic, _ := track(t, ctx, repo, -1, 123, "Player", nil)
	start, end, err := domain.Day.Bounds(w.now(), "UTC", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.QueueReport(ctx, topic.ID, "day", start, end); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.PendingReportIDs(ctx, topic.ID, "day")
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	parts := []string{"<b>Previously acknowledged part</b>", "<b>Pending legacy part</b>"}
	if err := repo.PrepareReportDelivery(ctx, pending[0], parts); err != nil {
		t.Fatal(err)
	}
	if err := repo.AdvanceReportDelivery(ctx, pending[0], 99); err != nil {
		t.Fatal(err)
	}
	if err := w.ReportsOnce(ctx, domain.Day); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 1 || sent[0].rich || sent[0].text != parts[1] {
		t.Fatal("legacy delivery reformatted or duplicated", sent)
	}
	runs, err := repo.LatestReports(ctx, topic.ID)
	if err != nil || len(runs) != 1 || runs[0].Trigger != "auto" || runs[0].MessageID == nil || *runs[0].MessageID != 1 {
		t.Fatal(runs, err)
	}
	if err := w.ReportsOnce(ctx, domain.Day); err != nil || len(sender.snapshot()) != 1 {
		t.Fatal("legacy report sent again", err)
	}
}
func TestReportFailureDoesNotRollbackOtherTopicsAndCanRetry(t *testing.T) {
	ctx, _, repo, _, sender, w := setup(t)
	a, p := track(t, ctx, repo, -1, 123, "First", nil)
	b, _ := track(t, ctx, repo, -2, 123, "Second", nil)
	m, err := match(t, 100).Snapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveMatches(ctx, []domain.Match{m}); err != nil {
		t.Fatal(err)
	}
	sender.failTopic = a.ID
	if err := w.ReportsOnce(ctx, domain.Day); err == nil {
		t.Fatal("send error hidden")
	}
	start, end, _ := domain.Day.Bounds(w.now(), "UTC", true)
	if exists, err := repo.HasAutoReport(ctx, a.ID, "day", start, end); err != nil || exists {
		t.Fatal(exists, err)
	}
	if exists, err := repo.HasAutoReport(ctx, b.ID, "day", start, end); err != nil || !exists {
		t.Fatal(exists, err)
	}
	sender.failTopic = 0
	if err := w.ReportsOnce(ctx, domain.Day); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 2 || sent[0].topic.ID != b.ID || sent[1].topic.ID != a.ID {
		t.Fatal(sent)
	}
}
func TestConcurrentAutoReportAttemptsSendOnce(t *testing.T) {
	ctx, _, repo, _, sender, w := setup(t)
	_, p := track(t, ctx, repo, -1, 123, "Player", nil)
	m, err := match(t, 100).Snapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveMatches(ctx, []domain.Match{m}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if err := w.ReportsOnce(ctx, domain.Day); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(sender.snapshot()) != 1 {
		t.Fatal("concurrent duplicate report")
	}
}
func (a *fakeAPI) Constants(context.Context, string) (map[string]json.RawMessage, error) {
	return nil, errors.New("OpenDota offline")
}
func TestPollingUsesCachedConstantsWhenRefreshFails(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	_, playerID := track(t, ctx, repo, -1, 123, "Player", ptr(int64(99)))
	stored, err := match(t, 100).Snapshot(playerID)
	if err != nil {
		t.Fatal(err)
	}
	stored.GPM = 600
	stored.PresentMetrics = nil
	if _, err := repo.SaveMatches(ctx, []domain.Match{stored}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveConstants(ctx, []domain.ConstantEntry{{Resource: "heroes", Code: 74, Name: "Cached Invoker"}}); err != nil {
		t.Fatal(err)
	}
	cache := service.ConstantsCache{Repo: repo, API: api, Interval: time.Nanosecond}
	if err := cache.Sync(ctx); err == nil {
		t.Fatal("expected refresh failure")
	}
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sent := sender.snapshot()
	if len(sent) != 1 || !strings.Contains(sent[0].text, "Cached Invoker") || !strings.Contains(sent[0].text, "GPM/XPM</b>: 600 /") {
		t.Fatal(sent)
	}
}
