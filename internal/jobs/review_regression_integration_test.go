//go:build integration

package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
)

func TestReviewFailedPollMustNotSealDay(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	topic, playerID := track(t, ctx, repo, -1, 123, "Player", ptr(int64(99)))
	old, err := match(t, 99).Snapshot(playerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveMatches(ctx, []domain.Match{old}); err != nil {
		t.Fatal(err)
	}
	api.fail[123] = errors.New("OpenDota temporary failure")
	if err := w.Cycle(ctx); err == nil {
		t.Fatal("expected polling failure")
	}
	start, end, _ := domain.Day.Bounds(w.now(), "UTC", true)
	sealed, err := repo.HasAutoReport(ctx, topic.ID, "day", start, end)
	if err != nil || sealed {
		t.Fatal("failed poll sealed day", sealed, err)
	}
	delete(api.fail, 123)
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	if err := w.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.Matches(ctx, []int64{playerID}, start, end)
	if err != nil || len(saved) != 2 {
		t.Fatalf("saved=%d err=%v", len(saved), err)
	}
	dayReports := 0
	for _, sent := range sender.snapshot() {
		if strings.Contains(sent.text, "<b>day report</b>") {
			dayReports++
			if !strings.Contains(sent.text, "Matches: 2") {
				t.Fatal("incomplete report", sent.text)
			}
		}
	}
	if dayReports != 1 {
		t.Fatalf("day sealed during failed poll=%t; yesterday's match saved=%d; day reports after recovery=%d", sealed, len(saved), dayReports)
	}
}

func TestReviewRetryMustIncludePersistedPendingMatch(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	topic, playerID := track(t, ctx, repo, -1, 123, "Player", ptr(int64(99)))
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	sender.failMatch = "/matches/100"
	if err := w.PollOnce(ctx); err == nil {
		t.Fatal("expected sending failure")
	}
	sender.failMatch = ""
	w = &Worker{Pool: w.Pool, API: api, Sender: sender, Now: w.Now}
	// The API's bounded recentMatches window no longer includes the pending game.
	api.matches[123] = []opendota.MatchData{match(t, 101)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, sent := range sender.snapshot() {
		ids = append(ids, sent.text)
	}
	if !strings.Contains(strings.Join(ids, "\n"), "/matches/100") {
		t.Fatalf("persisted match 100 was never retried; cursor advanced to %d; messages=%d", *cursor(t, ctx, repo, topic.ID, playerID), len(ids))
	}
	if len(ids) != 2 || !strings.Contains(ids[0], "/matches/100") || !strings.Contains(ids[1], "/matches/101") || *cursor(t, ctx, repo, topic.ID, playerID) != 101 {
		t.Fatal("pending games were not delivered in order", ids)
	}
}

func TestReviewReportRetryMustNotDuplicateAcknowledgedParts(t *testing.T) {
	ctx, _, repo, _, sender, w := setup(t)
	for account := int64(123); account < 145; account++ {
		_, playerID := track(t, ctx, repo, -1, account, fmt.Sprintf("%d %s", account, strings.Repeat("x", 200)), nil)
		m, err := match(t, 100).Snapshot(playerID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.SaveMatches(ctx, []domain.Match{m}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	sender.hook = func(_ context.Context) error {
		calls++
		if calls == 2 {
			return errors.New("explicit send failure")
		}
		return nil
	}
	if err := w.ReportsOnce(ctx, domain.Day); err == nil {
		t.Fatal("expected partial send failure")
	}
	if len(sender.snapshot()) != 1 {
		t.Fatal("expected one acknowledged part", sender.snapshot())
	}
	first := sender.snapshot()[0].text
	topic, err := repo.EnsureTopic(ctx, -1, nil, nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := repo.PendingReportIDs(ctx, topic.ID, "day")
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	snapshot, err := repo.ReportDelivery(ctx, pending[0])
	if err != nil || len(snapshot.MessageIDs) != 1 || len(snapshot.Parts) < 2 {
		t.Fatal(snapshot, err)
	}
	// Changing source data and restarting must retain the original contents.
	changedID, err := repo.UpsertPlayer(ctx, 123, "changed after partial send", nil)
	if err != nil {
		t.Fatal(err)
	}
	extra, err := match(t, 101).Snapshot(changedID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveMatches(ctx, []domain.Match{extra}); err != nil {
		t.Fatal(err)
	}
	w = &Worker{Pool: w.Pool, Sender: sender, Now: w.Now}
	sender.hook = nil
	if err := w.ReportsOnce(ctx, domain.Day); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, sent := range sender.snapshot() {
		if sent.text == first {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("first successfully acknowledged part sent %d times", count)
	}
	if len(sender.snapshot()) != len(snapshot.Parts) {
		t.Fatal("missing report parts", sender.snapshot())
	}
	for i, sent := range sender.snapshot() {
		if sent.text != snapshot.Parts[i] {
			t.Fatal("report snapshot changed")
		}
	}
}

func TestFailedPollDefersOnlyItsTopicAndSurvivesNextDay(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	bad, _ := track(t, ctx, repo, -1, 123, "Deferred", ptr(int64(99)))
	good, _ := track(t, ctx, repo, -2, 456, "Healthy", nil)
	paused, _ := track(t, ctx, repo, -3, 789, "Paused", nil)
	message := "previous failed poll"
	if err := repo.MarkFinished(ctx, paused.ID, w.now(), w.now(), &message); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetPaused(ctx, paused.ID, true); err != nil {
		t.Fatal(err)
	}
	api.fail[123] = errors.New("OpenDota down")
	if err := w.Cycle(ctx); err == nil {
		t.Fatal("expected API failure")
	}
	for _, period := range []domain.Period{domain.Day, domain.Week, domain.Month} {
		start, end, err := period.Bounds(w.now(), "UTC", true)
		if err != nil {
			t.Fatal(err)
		}
		for _, topic := range []domain.Topic{bad, good, paused} {
			exists, err := repo.HasAutoReport(ctx, topic.ID, string(period), start, end)
			if err != nil || exists != (topic.ID != bad.ID) {
				t.Fatal(topic, period, exists, err)
			}
		}
	}
	start, end, _ := domain.Day.Bounds(w.now(), "UTC", true)
	now := w.now().AddDate(0, 0, 1)
	w = &Worker{Pool: w.Pool, API: api, Sender: sender, Now: func() time.Time { return now }}
	delete(api.fail, 123)
	api.matches[123] = []opendota.MatchData{match(t, 100)}
	if err := w.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if exists, err := repo.HasAutoReport(ctx, bad.ID, "day", start, end); err != nil || !exists {
		t.Fatal("deferred period was lost", err)
	}
	count := 0
	for _, sent := range sender.snapshot() {
		if sent.topic.ID == bad.ID && strings.Contains(sent.text, "<b>day report</b>") && strings.Contains(sent.text, "Matches: 1") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("old day was not reported exactly once", sender.snapshot())
	}
}

func TestInitialPollStaysSilentWithNewerSharedHistory(t *testing.T) {
	ctx, _, repo, api, sender, w := setup(t)
	topic, p := track(t, ctx, repo, -1, 123, "New", nil)
	m, err := match(t, 101).Snapshot(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SaveMatches(ctx, []domain.Match{m}); err != nil {
		t.Fatal(err)
	}
	api.matches[123] = []opendota.MatchData{match(t, 99)}
	if err := w.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.snapshot()) != 0 || *cursor(t, ctx, repo, topic.ID, p) != 99 {
		t.Fatal("initial sync notified", sender.snapshot())
	}
}

func TestMultipartNotificationResumesAfterRestartAndResyncSuppressesIt(t *testing.T) {
	for _, resync := range []bool{false, true} {
		t.Run(fmt.Sprint(resync), func(t *testing.T) {
			ctx, _, repo, api, sender, w := setup(t)
			var topic domain.Topic
			var playerIDs []int64
			for account := int64(123); account < 145; account++ {
				var p int64
				topic, p = track(t, ctx, repo, -1, account, fmt.Sprintf("%d %s", account, strings.Repeat("x", 200)), ptr(int64(99)))
				playerIDs = append(playerIDs, p)
				api.matches[account] = []opendota.MatchData{match(t, 100)}
			}
			calls := 0
			sender.hook = func(context.Context) error {
				calls++
				if calls == 2 {
					return errors.New("explicit failure")
				}
				return nil
			}
			if err := w.PollOnce(ctx); err == nil {
				t.Fatal("expected send failure")
			}
			if len(sender.snapshot()) != 1 {
				t.Fatal(sender.snapshot())
			}
			d, err := repo.MatchDelivery(ctx, topic.ID, 100)
			if err != nil || len(d.MessageIDs) != 1 || len(d.Parts) < 2 {
				t.Fatal(d, err)
			}
			sender.hook = nil
			if resync {
				for _, p := range playerIDs {
					if err := repo.AdvanceCursor(ctx, topic.ID, p, 100); err != nil {
						t.Fatal(err)
					}
				}
			}
			for account := range api.matches {
				api.matches[account] = nil
			}
			w = &Worker{Pool: w.Pool, API: api, Sender: sender, Now: w.Now}
			if err := w.PollOnce(ctx); err != nil {
				t.Fatal(err)
			}
			want := len(d.Parts)
			if resync {
				want = 1
			}
			if len(sender.snapshot()) != want {
				t.Fatal("wrong number of delivered parts", len(sender.snapshot()), want)
			}
			for i, sent := range sender.snapshot() {
				if sent.text != d.Parts[i] {
					t.Fatal("notification snapshot changed")
				}
			}
			for _, p := range playerIDs {
				if *cursor(t, ctx, repo, topic.ID, p) != 100 {
					t.Fatal("cursor did not advance")
				}
			}
		})
	}
}
