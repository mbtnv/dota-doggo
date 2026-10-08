package postgres

import (
	"context"
	"errors"
	"time"

	"dota-doggo/internal/domain"
	"github.com/jackc/pgx/v5"
)

const topicColumns = "id,telegram_chat_id,telegram_thread_id,title,timezone,is_paused,created_at"

func scanTopic(row pgx.Row) (domain.Topic, error) {
	var t domain.Topic
	err := row.Scan(&t.ID, &t.ChatID, &t.ThreadID, &t.Title, &t.Timezone, &t.Paused, &t.CreatedAt)
	return t, err
}
func (s *Store) Topic(ctx context.Context, chatID int64, threadID *int64) (domain.Topic, error) {
	return scanTopic(s.db.QueryRow(ctx, "SELECT "+topicColumns+" FROM tracked_topics WHERE telegram_chat_id=$1 AND telegram_thread_id IS NOT DISTINCT FROM $2", chatID, threadID))
}
func (s *Store) EnsureTopic(ctx context.Context, chatID int64, threadID *int64, title *string, timezone string) (domain.Topic, error) {
	_, err := s.db.Exec(ctx, `INSERT INTO tracked_topics(telegram_chat_id,telegram_thread_id,title,timezone,is_paused,created_at)
	 VALUES($1,$2,$3,$4,false,now()) ON CONFLICT DO NOTHING`, chatID, threadID, title, timezone)
	if err != nil {
		return domain.Topic{}, err
	}
	return s.Topic(ctx, chatID, threadID)
}
func (s *Store) Topics(ctx context.Context) ([]domain.Topic, error) {
	rows, err := s.db.Query(ctx, "SELECT "+topicColumns+" FROM tracked_topics ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Topic
	for rows.Next() {
		t, err := scanTopic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) SetTimezone(ctx context.Context, id int64, zone string) error {
	if _, err := time.LoadLocation(zone); err != nil {
		return errors.New("unknown IANA timezone")
	}
	_, err := s.db.Exec(ctx, "UPDATE tracked_topics SET timezone=$2 WHERE id=$1", id, zone)
	return err
}
func (s *Store) SetPaused(ctx context.Context, id int64, paused bool) error {
	_, err := s.db.Exec(ctx, "UPDATE tracked_topics SET is_paused=$2 WHERE id=$1", id, paused)
	return err
}

func (s *Store) UpsertPlayer(ctx context.Context, accountID int64, name string, profileURL *string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO players(dota_account_id,display_name,profile_url,created_at) VALUES($1,$2,$3,now())
	 ON CONFLICT(dota_account_id) DO UPDATE SET display_name=excluded.display_name,profile_url=excluded.profile_url RETURNING id`, accountID, name, profileURL).Scan(&id)
	return id, err
}

// GetOrCreatePlayer preserves current profile metadata during legacy import.
func (s *Store) GetOrCreatePlayer(ctx context.Context, accountID int64, name string, profileURL *string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO players(dota_account_id,display_name,profile_url,created_at) VALUES($1,$2,$3,now()) ON CONFLICT(dota_account_id) DO NOTHING RETURNING id`, accountID, name, profileURL).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = s.db.QueryRow(ctx, "SELECT id FROM players WHERE dota_account_id=$1", accountID).Scan(&id)
	}
	return id, err
}
func (s *Store) AddPlayer(ctx context.Context, topicID, playerID int64, alias *string, userID *int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `INSERT INTO topic_players(topic_id,player_id,alias,added_by_telegram_user_id,created_at)
	 VALUES($1,$2,$3,$4,now()) ON CONFLICT(topic_id,player_id) DO NOTHING`, topicID, playerID, alias, userID)
	return tag.RowsAffected() > 0, err
}
func (s *Store) RemovePlayer(ctx context.Context, topicID int64, filter string) (bool, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM topic_players WHERE id=(SELECT tp.id FROM topic_players tp JOIN players p ON p.id=tp.player_id
 WHERE tp.topic_id=$1 AND (tp.alias=$2 OR p.dota_account_id::text=$2)
 ORDER BY (p.dota_account_id::text=$2) DESC, tp.id LIMIT 1)`, topicID, filter)
	return tag.RowsAffected() > 0, err
}
func (s *Store) Players(ctx context.Context, topicID int64) ([]domain.Player, error) {
	rows, err := s.db.Query(ctx, `SELECT p.id,p.dota_account_id,p.display_name,p.profile_url,tp.alias,tp.last_seen_match_id
	 FROM topic_players tp JOIN players p ON p.id=tp.player_id WHERE tp.topic_id=$1 ORDER BY tp.id`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Player
	for rows.Next() {
		var p domain.Player
		if err := rows.Scan(&p.ID, &p.AccountID, &p.DisplayName, &p.ProfileURL, &p.Alias, &p.LastSeenMatchID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AdvanceCursor cannot move backwards when resync races a worker.
func (s *Store) AdvanceCursor(ctx context.Context, topicID, playerID, matchID int64) error {
	_, err := s.db.Exec(ctx, "UPDATE topic_players SET last_seen_match_id=GREATEST(COALESCE(last_seen_match_id,0),$3) WHERE topic_id=$1 AND player_id=$2", topicID, playerID, matchID)
	return err
}
