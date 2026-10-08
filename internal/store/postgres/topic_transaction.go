package postgres

import (
	"context"
	"time"

	"dota-doggo/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithinTopic serializes short state transitions shared by worker and resync.
// OpenDota requests must finish before entering this transaction.
func WithinTopic(ctx context.Context, pool *pgxpool.Pool, id int64, fn func(*Store, domain.Topic) error) error {
	return InTx(ctx, pool, func(tx *Store) error {
		topic, err := tx.LockTopic(ctx, id)
		if err != nil {
			return err
		}
		return fn(tx, topic)
	})
}

// LockTopic must be called on a Store backed by an explicit transaction.
func (s *Store) LockTopic(ctx context.Context, id int64) (domain.Topic, error) {
	topic, err := scanTopic(s.db.QueryRow(ctx, "SELECT "+topicColumns+" FROM tracked_topics WHERE id=$1 FOR NO KEY UPDATE", id))
	if err != nil {
		return topic, err
	}
	// Lock existing relations too: untrack cannot remove a participant while
	// its notification is being sent. New relations start next poll.
	rows, err := s.db.Query(ctx, "SELECT id FROM topic_players WHERE topic_id=$1 ORDER BY player_id FOR UPDATE", id)
	if err != nil {
		return topic, err
	}
	for rows.Next() {
		var relation int64
		if err := rows.Scan(&relation); err != nil {
			rows.Close()
			return topic, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return topic, err
	}
	return topic, nil
}

func CommitHistory(ctx context.Context, pool *pgxpool.Pool, topicID, playerID int64, matches []domain.Match, cursor int64) (int, error) {
	var inserted int
	err := WithinTopic(ctx, pool, topicID, func(tx *Store, _ domain.Topic) error {
		var err error
		inserted, err = tx.SaveMatches(ctx, matches)
		if err != nil {
			return err
		}
		return tx.AdvanceCursor(ctx, topicID, playerID, cursor)
	})
	return inserted, err
}

func (s *Store) MatchGroup(ctx context.Context, ids []int64, matchID int64) ([]domain.Match, error) {
	return collectMatches(s.db.Query(ctx, "SELECT "+matchColumns+" FROM player_matches WHERE player_id=ANY($1) AND match_id=$2 ORDER BY player_id", ids, matchID))
}

func (s *Store) HasAutoReport(ctx context.Context, topicID int64, period string, start, end time.Time) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM report_runs WHERE topic_id=$1 AND period_type=$2 AND period_start=$3 AND period_end=$4 AND trigger_source='auto')", topicID, period, start, end).Scan(&exists)
	return exists, err
}
