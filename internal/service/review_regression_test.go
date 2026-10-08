package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
)

type liveContextSender struct{ jobSender }

func (s *liveContextSender) SendHTML(ctx context.Context, topic domain.Topic, text string) ([]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.jobSender.SendHTML(ctx, topic, text)
}

func TestReviewTimedOutResyncMustReportResult(t *testing.T) {
	sender := &liveContextSender{}
	api := &blockingHistory{started: make(chan struct{}, 1)}
	j := NewJobs(History{API: api}, sender, nil)
	j.timeout = 10 * time.Millisecond
	ctx := context.Background()
	j.run(ctx, historyJob{topic: domain.Topic{ID: 1}, players: []domain.Player{{ID: 1, AccountID: 123, DisplayName: "Player"}, {ID: 2, AccountID: 456, DisplayName: "Not started"}}, days: 365})
	if len(sender.texts) != 2 || !strings.Contains(sender.texts[1], "лимит времени") || !strings.Contains(sender.texts[1], "Незавершённых игроков: 2") || !strings.Contains(sender.texts[1], "Not started") {
		t.Fatalf("job deadline expired; only %d progress message and no terminal result", len(sender.texts))
	}
}

type partialDeadlineAPI struct{ first, second opendota.MatchData }

func (a *partialDeadlineAPI) History(context.Context, int64, int, int, int) ([]opendota.MatchData, error) {
	return []opendota.MatchData{a.first, a.second}, nil
}
func (a *partialDeadlineAPI) MatchPlayers(ctx context.Context, id int64) ([]opendota.MatchData, error) {
	if id == *a.first.MatchID {
		return []opendota.MatchData{a.first}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestResyncTimeoutKeepsAndReportsPartialCounts(t *testing.T) {
	sender := &liveContextSender{}
	api := &partialDeadlineAPI{first: historyMatch(t, 100), second: historyMatch(t, 101)}
	repo := &historyRepo{matches: map[int64]domain.Match{}}
	history := History{API: api, Commit: func(ctx context.Context, _, _ int64, matches []domain.Match, _ int64) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return repo.SaveMatches(ctx, matches)
	}}
	j := NewJobs(history, sender, nil)
	j.timeout = 50 * time.Millisecond
	j.run(context.Background(), historyJob{topic: domain.Topic{ID: 1}, players: []domain.Player{{ID: 1, AccountID: 123, DisplayName: "<partial>"}, {ID: 2, AccountID: 456, DisplayName: "<waiting>"}}, days: 7})
	if len(sender.texts) != 2 || len(repo.matches) != 1 {
		t.Fatal(sender.texts, repo.matches)
	}
	result := sender.texts[1]
	for _, want := range []string{"лимит времени", "Получено: 2 · Добавлено: 1", "Незавершённых игроков: 2", "&lt;partial&gt;", "&lt;waiting&gt;"} {
		if !strings.Contains(result, want) {
			t.Fatal(result, want)
		}
	}
}
