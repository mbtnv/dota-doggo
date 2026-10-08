// Package jobs contains the worker's serial polling and reporting tasks.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/format"
	"dota-doggo/internal/logging"
	"dota-doggo/internal/opendota"
	"dota-doggo/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLockLost = errors.New("worker lock lost")

type RecentAPI interface {
	RecentMatches(context.Context, int64) ([]opendota.MatchData, error)
}
type Sender interface {
	SendHTML(context.Context, domain.Topic, string) ([]int64, error)
	SendParts(context.Context, domain.Topic, []string) ([]int64, error)
}
type Worker struct {
	Pool      *pgxpool.Pool
	API       RecentAPI
	Sender    Sender
	Formatter format.Formatter
	Now       func() time.Time
	Log       *slog.Logger
	Secrets   []string
	// Check verifies ownership of the dedicated worker session before sends.
	Check func(context.Context) error
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}
func (w *Worker) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.Check != nil {
		return w.Check(ctx)
	}
	return nil
}
func playerIDs(players []domain.Player) []int64 {
	out := make([]int64, len(players))
	for i, p := range players {
		out[i] = p.ID
	}
	return out
}

func (w *Worker) PollOnce(ctx context.Context) error {
	_, err := w.pollOnce(ctx)
	return err
}

// Outcomes are per topic: a failed poll must not seal its report periods.
func (w *Worker) pollOnce(ctx context.Context) (map[int64]bool, error) {
	outcomes := map[int64]bool{}
	repo := postgres.New(w.Pool)
	topics, err := repo.Topics(ctx)
	if err != nil {
		return outcomes, err
	}
	constants, err := repo.Constants(ctx)
	if err != nil {
		return outcomes, err
	}
	var failures []error
	for _, topic := range topics {
		if err := w.check(ctx); err != nil {
			return outcomes, errors.Join(append(failures, err)...)
		}
		if topic.Paused {
			continue
		}
		started := w.now()
		if err := repo.MarkStarted(ctx, topic.ID, started); err != nil {
			failures = append(failures, err)
			continue
		}
		err := w.pollTopic(ctx, topic, constants)
		var message *string
		if err != nil {
			text := logging.Redact(err.Error(), w.Secrets...)
			message = &text
			failures = append(failures, fmt.Errorf("topic %d: %w", topic.ID, err))
		}
		// Persist cancellation/failure status even when the request scope ended.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		finishErr := repo.MarkFinished(finishCtx, topic.ID, started, w.now(), message)
		cancel()
		outcomes[topic.ID] = err == nil && finishErr == nil
		if finishErr != nil {
			failures = append(failures, finishErr)
		}
	}
	return outcomes, errors.Join(failures...)
}

func (w *Worker) pollTopic(ctx context.Context, topic domain.Topic, constants domain.Constants) error {
	repo := postgres.New(w.Pool)
	players, err := repo.Players(ctx, topic.ID)
	if err != nil {
		return err
	}
	loaded := map[int64][]domain.Match{}
	var failures []error
	for _, player := range players {
		if err := w.check(ctx); err != nil {
			return errors.Join(append(failures, err)...)
		}
		apiCtx, cancel := context.WithTimeout(ctx, time.Minute)
		recent, err := w.API.RecentMatches(apiCtx, player.AccountID)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("player %d: %w", player.AccountID, err))
			continue
		}
		var matches []domain.Match
		for _, data := range recent {
			m, snapshotErr := data.Snapshot(player.ID)
			if snapshotErr != nil {
				err = snapshotErr
				break
			}
			matches = append(matches, m)
		}
		// Keep this player's cursor unchanged if any row is malformed. Advancing
		// past a malformed older match would otherwise hide it on the next poll.
		if err != nil {
			failures = append(failures, fmt.Errorf("player %d: %w", player.AccountID, err))
			continue
		}
		loaded[player.ID] = domain.NewMatches(matches, nil)
	}
	// Persist history and initialize only still-tracked players. Read cursors
	// after locking, since resync may have advanced them during API loading.
	var order []int64
	stateCtx, cancelState := context.WithTimeout(ctx, time.Minute)
	err = postgres.WithinTopic(stateCtx, w.Pool, topic.ID, func(tx *postgres.Store, current domain.Topic) error {
		if current.Paused {
			return nil
		}
		players, err := tx.Players(stateCtx, current.ID)
		if err != nil {
			return err
		}
		for _, player := range players {
			matches, ok := loaded[player.ID]
			if !ok {
				continue
			}
			if _, err := tx.SaveMatches(stateCtx, matches); err != nil {
				return err
			}
			if player.LastSeenMatchID == nil && len(matches) > 0 {
				if err := tx.AdvanceCursor(stateCtx, current.ID, player.ID, matches[len(matches)-1].ID); err != nil {
					return err
				}
				// This player's first nonempty poll must remain silent, even if
				// another topic has already stored newer games in shared history.
				delete(loaded, player.ID)
			}
		}
		loadedIDs := make([]int64, 0, len(loaded))
		for id := range loaded {
			loadedIDs = append(loadedIDs, id)
		}
		order, err = tx.PendingMatchIDs(stateCtx, current.ID, loadedIDs)
		if err != nil {
			return err
		}
		return w.check(stateCtx)
	})
	cancelState()
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, id := range order {
		if err := w.deliverMatch(ctx, topic.ID, id, loaded, constants); err != nil {
			return errors.Join(append(failures, err)...)
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) ReportsOnce(ctx context.Context, period domain.Period) error {
	return w.reportsOnce(ctx, period, nil)
}

func (w *Worker) reportsOnce(ctx context.Context, period domain.Period, outcomes map[int64]bool) error {
	if !period.Valid() {
		return errors.New("invalid report period")
	}
	repo := postgres.New(w.Pool)
	topics, err := repo.Topics(ctx)
	if err != nil {
		return err
	}
	constants, err := repo.Constants(ctx)
	if err != nil {
		return err
	}
	now := w.now()
	var failures []error
	for _, topic := range topics {
		if err := w.check(ctx); err != nil {
			return errors.Join(append(failures, err)...)
		}
		// Queue even blocked periods before the next calendar boundary. Their
		// contents are rendered only after this topic has recovered.
		err := postgres.WithinTopic(ctx, w.Pool, topic.ID, func(tx *postgres.Store, current domain.Topic) error {
			start, end, err := period.Bounds(now, current.Timezone, true)
			if err != nil {
				return err
			}
			players, err := tx.Players(ctx, current.ID)
			if err != nil || len(players) == 0 {
				return err
			}
			return tx.QueueReport(ctx, current.ID, string(period), start, end)
		})
		if err == nil {
			var ids []int64
			ids, err = repo.PendingReportIDs(ctx, topic.ID, string(period))
			if err == nil {
				for _, id := range ids {
					if err = w.deliverReport(ctx, topic.ID, id, constants, outcomes); err != nil {
						break
					}
				}
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("topic %d %s report: %w", topic.ID, period, err))
		}
	}
	return errors.Join(failures...)
}
func (w *Worker) Cycle(ctx context.Context) error {
	var failures []error
	outcomes, err := w.pollOnce(ctx)
	if err != nil {
		failures = append(failures, err)
	}
	for _, period := range []domain.Period{domain.Day, domain.Week, domain.Month} {
		if err := w.check(ctx); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := w.reportsOnce(ctx, period, outcomes); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
