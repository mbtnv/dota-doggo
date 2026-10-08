package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/format"
	"dota-doggo/internal/store/postgres"
	"github.com/jackc/pgx/v5"
)

// One part per transaction: a later explicit failure cannot roll back an
// acknowledged part. Network/commit uncertainty can still produce duplicates.
func (w *Worker) sendPart(ctx context.Context, topic domain.Topic, part string) (int64, error) {
	if err := w.check(ctx); err != nil {
		return 0, err
	}
	ids, err := w.Sender.SendParts(ctx, topic, []string{part})
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, errors.New("sender did not acknowledge exactly one message")
	}
	return ids[0], w.check(ctx)
}

func (w *Worker) deliverMatch(ctx context.Context, topicID, matchID int64, loaded map[int64][]domain.Match, constants domain.Constants) error {
	for {
		if err := w.check(ctx); err != nil {
			return err
		}
		done := false
		stepCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err := postgres.WithinTopic(stepCtx, w.Pool, topicID, func(tx *postgres.Store, topic domain.Topic) error {
			if topic.Paused {
				done = true
				return nil
			}
			players, err := tx.Players(stepCtx, topic.ID)
			if err != nil {
				return err
			}
			delivery, err := tx.MatchDelivery(stepCtx, topic.ID, matchID)
			fresh := errors.Is(err, pgx.ErrNoRows)
			if err != nil && !fresh {
				return err
			}
			var eligible []domain.Player
			for _, p := range players {
				if p.LastSeenMatchID == nil || matchID <= *p.LastSeenMatchID {
					continue
				}
				if fresh {
					if _, ok := loaded[p.ID]; ok {
						eligible = append(eligible, p)
					}
				} else {
					for _, id := range delivery.PlayerIDs {
						if id != p.ID {
							continue
						}
						if _, ok := loaded[p.ID]; !ok {
							return errors.New("pending notification awaits successful player poll")
						}
						eligible = append(eligible, p)
					}
				}
			}
			if len(eligible) == 0 {
				done = true
				return tx.DeleteMatchDelivery(stepCtx, topic.ID, matchID)
			}
			if fresh {
				matches, err := tx.MatchGroup(stepCtx, playerIDs(players), matchID)
				if err != nil {
					return err
				}
				// Only participants with an actual stored row can acknowledge this game.
				var participants []int64
				for _, p := range eligible {
					for _, m := range matches {
						if m.PlayerID == p.ID {
							participants = append(participants, p.ID)
						}
					}
				}
				if len(participants) == 0 {
					done = true
					return nil
				}
				groups := format.GroupMatches(players, matches)
				parts, err := format.SplitHTML(w.Formatter.Notification(groups[0], constants, topic.Timezone), format.TelegramTextLimit)
				if err != nil {
					return err
				}
				return tx.CreateMatchDelivery(stepCtx, topic.ID, matchID, participants, parts)
			}
			index := len(delivery.MessageIDs)
			if index < len(delivery.Parts) {
				id, err := w.sendPart(stepCtx, topic, delivery.Parts[index])
				if err != nil {
					return err
				}
				if err := tx.AdvanceMatchDelivery(stepCtx, topic.ID, matchID, id); err != nil {
					return err
				}
				index++
			}
			if index == len(delivery.Parts) {
				for _, p := range eligible {
					if err := tx.AdvanceCursor(stepCtx, topic.ID, p.ID, matchID); err != nil {
						return err
					}
				}
				done = true
				return tx.DeleteMatchDelivery(stepCtx, topic.ID, matchID)
			}
			return nil
		})
		cancel()
		if err != nil || done {
			return err
		}
	}
}

func (w *Worker) deliverReport(ctx context.Context, topicID, deliveryID int64, constants domain.Constants, outcomes map[int64]bool) error {
	for {
		if err := w.check(ctx); err != nil {
			return err
		}
		done := false
		stepCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err := postgres.WithinTopic(stepCtx, w.Pool, topicID, func(tx *postgres.Store, topic domain.Topic) error {
			d, err := tx.ReportDelivery(stepCtx, deliveryID)
			if errors.Is(err, pgx.ErrNoRows) {
				done = true
				return nil
			}
			if err != nil {
				return err
			}
			players, err := tx.Players(stepCtx, topic.ID)
			if err != nil {
				return err
			}
			if len(players) == 0 {
				done = true
				return tx.DeleteReportDelivery(stepCtx, d.ID)
			}
			if d.Parts == nil {
				if !topic.Paused {
					state, err := tx.Runtime(stepCtx, topic.ID)
					if err != nil {
						return err
					}
					inFlight := state.StartedAt != nil && (state.FinishedAt == nil || state.FinishedAt.Before(*state.StartedAt))
					if (outcomes != nil && !outcomes[topic.ID]) || state.Error != nil || inFlight {
						done = true
						return nil // Keep this exact period, including across restarts.
					}
				}
				period := domain.Period(d.Period)
				matches, err := tx.Matches(stepCtx, playerIDs(players), d.Start, d.End)
				if err != nil {
					return err
				}
				summaries := domain.TopicSummaries(players, period, d.Start, d.End, matches, period == domain.Day)
				parts := []string{}
				if len(summaries) > 0 {
					header := fmt.Sprintf("<b>%s report</b>\n%s — %s", period, format.Datetime(d.Start, topic.Timezone), format.Datetime(d.End, topic.Timezone))
					parts, err = format.SplitSections(header, w.Formatter.ReportSections(summaries, constants), format.TelegramTextLimit)
					if err != nil {
						return err
					}
				}
				// Commit stable text before the first external send.
				return tx.PrepareReportDelivery(stepCtx, d.ID, parts)
			}
			var messageID *int64
			if n := len(d.MessageIDs); n > 0 {
				messageID = &d.MessageIDs[n-1]
			}
			if len(d.MessageIDs) < len(d.Parts) {
				id, err := w.sendPart(stepCtx, topic, d.Parts[len(d.MessageIDs)])
				if err != nil {
					return err
				}
				if err := tx.AdvanceReportDelivery(stepCtx, d.ID, id); err != nil {
					return err
				}
				d.MessageIDs = append(d.MessageIDs, id)
				messageID = &id
			}
			if len(d.MessageIDs) == len(d.Parts) {
				// Empty days are sealed silently, but only after successful loading.
				if err := tx.RecordReport(stepCtx, topic.ID, domain.ReportRun{Period: d.Period, Trigger: "auto", Start: d.Start, End: d.End, MessageID: messageID}); err != nil {
					return err
				}
				done = true
				return tx.DeleteReportDelivery(stepCtx, d.ID)
			}
			return nil
		})
		cancel()
		if err != nil || done {
			return err
		}
	}
}
