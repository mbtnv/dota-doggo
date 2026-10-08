package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
)

type jobSender struct {
	texts  []string
	topics []domain.Topic
}

func (s *jobSender) SendHTML(_ context.Context, t domain.Topic, text string) ([]int64, error) {
	s.texts = append(s.texts, text)
	s.topics = append(s.topics, t)
	return []int64{1}, nil
}
func TestResyncProgressAndPartialFailureSummary(t *testing.T) {
	api := &historyAPI{pageErr: map[int]error{0: errors.New("private error details")}}
	sender := &jobSender{}
	j := NewJobs(History{API: api}, sender, nil)
	j.run(context.Background(), historyJob{topic: domain.Topic{ID: 1, ChatID: -1, ThreadID: ref(int64(42))}, players: []domain.Player{{ID: 1, AccountID: 123, DisplayName: "<Player>"}}, days: 7})
	if len(sender.texts) != 2 || !strings.Contains(sender.texts[0], "&lt;Player&gt;") || !strings.Contains(sender.texts[1], "Незавершённых игроков: 1") || strings.Contains(sender.texts[1], "private") {
		t.Fatal(sender.texts)
	}
	for _, topic := range sender.topics {
		if topic.ChatID != -1 || topic.ThreadID == nil || *topic.ThreadID != 42 {
			t.Fatal(topic)
		}
	}
}

func TestResyncReportsSuccessfulCounts(t *testing.T) {
	m := historyMatch(t, 100)
	api := &historyAPI{pages: map[int][]opendota.MatchData{0: {m}}, details: map[int64][]opendota.MatchData{100: {m}}}
	sender := &jobSender{}
	j := NewJobs(History{API: api, Repo: &historyRepo{matches: map[int64]domain.Match{}}}, sender, nil)
	j.run(context.Background(), historyJob{topic: domain.Topic{ID: 1}, players: []domain.Player{{ID: 1, AccountID: 123}}, days: 7})
	if len(sender.texts) != 2 || !strings.Contains(sender.texts[1], "Получено: 1 · Добавлено: 1") || !strings.Contains(sender.texts[1], "Незавершённых игроков: 0") {
		t.Fatal(sender.texts)
	}
}

type blockingHistory struct{ started chan struct{} }

func (a *blockingHistory) History(ctx context.Context, _ int64, _, _, _ int) ([]opendota.MatchData, error) {
	a.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}
func (a *blockingHistory) MatchPlayers(context.Context, int64) ([]opendota.MatchData, error) {
	panic("unexpected details")
}
func TestJobsRejectDuplicateAndStopWithoutStartingQueuedWork(t *testing.T) {
	api := &blockingHistory{started: make(chan struct{}, 2)}
	sender := &jobSender{}
	j := NewJobs(History{API: api}, sender, nil)
	players := []domain.Player{{ID: 1, AccountID: 123}}
	if !j.Enqueue(domain.Topic{ID: 1}, players, 7) || j.Enqueue(domain.Topic{ID: 1}, players, 7) {
		t.Fatal("duplicate admission")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { j.Run(ctx); close(done) }()
	select {
	case <-api.started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	if j.Enqueue(domain.Topic{ID: 1}, players, 7) || !j.Enqueue(domain.Topic{ID: 2}, players, 7) {
		t.Fatal("queue state")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("job did not stop")
	}
	if j.Enqueue(domain.Topic{ID: 3}, players, 7) {
		t.Fatal("admitted after shutdown")
	}
	if len(sender.texts) != 1 {
		t.Fatal("shutdown sent a result or started queued work", sender.texts)
	}
	select {
	case <-api.started:
		t.Fatal("started queued work after cancellation")
	default:
	}
}
