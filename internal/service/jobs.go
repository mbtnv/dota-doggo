package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"time"

	"dota-doggo/internal/domain"
)

type JobSender interface {
	SendHTML(context.Context, domain.Topic, string) ([]int64, error)
}
type historyJob struct {
	topic   domain.Topic
	players []domain.Player
	days    int
}

// Jobs owns a bounded FIFO and runs one resync at a time. Polling never waits for
// API history. The mutex protects shutdown and duplicate-topic admission.
type Jobs struct {
	History History
	Sender  JobSender
	Log     *slog.Logger
	queue   chan historyJob
	mu      sync.Mutex
	pending map[int64]bool
	closed  bool
	timeout time.Duration
}

func NewJobs(history History, sender JobSender, log *slog.Logger) *Jobs {
	return &Jobs{History: history, Sender: sender, Log: log, queue: make(chan historyJob, 16), pending: map[int64]bool{}}
}
func (j *Jobs) Enqueue(topic domain.Topic, players []domain.Player, days int) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed || j.pending[topic.ID] || days < 1 || days > 365 || len(players) == 0 {
		return false
	}
	select {
	case j.queue <- historyJob{topic, append([]domain.Player(nil), players...), days}:
		j.pending[topic.ID] = true
		return true
	default:
		return false
	}
}
func (j *Jobs) Run(ctx context.Context) {
	defer func() { j.mu.Lock(); j.closed = true; j.mu.Unlock() }()
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case job := <-j.queue:
			if ctx.Err() != nil {
				return
			}
			j.run(ctx, job)
			j.mu.Lock()
			delete(j.pending, job.topic.ID)
			j.mu.Unlock()
		}
	}
}
func (j *Jobs) run(parent context.Context, job historyJob) {
	limit := j.timeout
	if limit <= 0 {
		limit = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	var total HistoryResult
	var failed []string
	for index, player := range job.players {
		if ctx.Err() != nil {
			for _, remaining := range job.players[index:] {
				failed = append(failed, remaining.Label())
			}
			break
		}
		_, err := j.Sender.SendHTML(ctx, job.topic, "Обновляю историю: <b>"+html.EscapeString(player.Label())+"</b>")
		if err != nil && j.Log != nil {
			j.Log.Warn("resync progress send failed", "topic_id", job.topic.ID, "error", err)
		}
		result, err := j.History.Sync(ctx, job.topic.ID, player, job.days, nil)
		total.Fetched += result.Fetched
		total.Inserted += result.Inserted
		total.Failed += result.Failed
		if err != nil {
			failed = append(failed, player.Label())
			if j.Log != nil {
				j.Log.Error("resync failed", "topic_id", job.topic.ID, "player_id", player.ID, "error", err)
			}
		}
	}
	// Shutdown cancels all sends. The job's own deadline still gets a terminal
	// result through a new short scope derived from the running process.
	if parent.Err() != nil {
		return
	}
	header := "История обновлена."
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		header = "История обновлена частично: истёк лимит времени. Повторите /resync."
	}
	text := fmt.Sprintf("%s Получено: %d · Добавлено: %d · Ошибок матчей/деталей: %d · Незавершённых игроков: %d", header, total.Fetched, total.Inserted, total.Failed, len(failed))
	if len(failed) > 0 {
		labels := make([]string, len(failed))
		for i, label := range failed {
			labels[i] = html.EscapeString(label)
		}
		text += "\nНе завершены: " + strings.Join(labels, ", ")
	}
	finishCtx, finishCancel := context.WithTimeout(parent, 30*time.Second)
	defer finishCancel()
	_, err := j.Sender.SendHTML(finishCtx, job.topic, text)
	if err != nil && j.Log != nil {
		j.Log.Error("resync result send failed", "topic_id", job.topic.ID, "error", err)
	}
}
