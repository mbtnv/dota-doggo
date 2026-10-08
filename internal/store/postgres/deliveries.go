package postgres

import (
	"context"
	"time"
)

type ReportDelivery struct {
	ID         int64
	Period     string
	Start, End time.Time
	Parts      []string
	MessageIDs []int64
}
type MatchDelivery struct {
	PlayerIDs  []int64
	Parts      []string
	MessageIDs []int64
}

// PendingMatchIDs uses persisted history, including games outside recentMatches.
// Only players with a successful response may advance in this poll. Existing
// multipart deliveries are included so resync/untrack can discard stale work.
func (s *Store) PendingMatchIDs(ctx context.Context, topicID int64, loadedIDs []int64) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT match_id FROM (
	 SELECT m.match_id FROM topic_players tp JOIN player_matches m ON m.player_id=tp.player_id
	 WHERE tp.topic_id=$1 AND tp.player_id=ANY($2) AND m.match_id>tp.last_seen_match_id
	 UNION SELECT match_id FROM match_deliveries WHERE topic_id=$1
	) pending ORDER BY match_id`, topicID, loadedIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Store) MatchDelivery(ctx context.Context, topicID, matchID int64) (MatchDelivery, error) {
	var d MatchDelivery
	err := s.db.QueryRow(ctx, `SELECT player_ids,parts,message_ids FROM match_deliveries WHERE topic_id=$1 AND match_id=$2`, topicID, matchID).Scan(&d.PlayerIDs, &d.Parts, &d.MessageIDs)
	return d, err
}
func (s *Store) CreateMatchDelivery(ctx context.Context, topicID, matchID int64, players []int64, parts []string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO match_deliveries(topic_id,match_id,player_ids,parts) VALUES($1,$2,$3,$4)`, topicID, matchID, players, parts)
	return err
}
func (s *Store) AdvanceMatchDelivery(ctx context.Context, topicID, matchID, messageID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE match_deliveries SET message_ids=array_append(message_ids,$3) WHERE topic_id=$1 AND match_id=$2`, topicID, matchID, messageID)
	return err
}
func (s *Store) DeleteMatchDelivery(ctx context.Context, topicID, matchID int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM match_deliveries WHERE topic_id=$1 AND match_id=$2`, topicID, matchID)
	return err
}
func (s *Store) QueueReport(ctx context.Context, topicID int64, period string, start, end time.Time) error {
	_, err := s.db.Exec(ctx, `INSERT INTO report_deliveries(topic_id,period_type,period_start,period_end)
	 SELECT $1::integer,$2::text,$3::timestamptz,$4::timestamptz WHERE NOT EXISTS(SELECT 1 FROM report_runs WHERE topic_id=$1 AND period_type=$2
	 AND period_start=$3 AND period_end=$4 AND trigger_source='auto') ON CONFLICT DO NOTHING`, topicID, period, start, end)
	return err
}
func (s *Store) PendingReportIDs(ctx context.Context, topicID int64, period string) ([]int64, error) {
	rows, err := s.db.Query(ctx, `SELECT id FROM report_deliveries WHERE topic_id=$1 AND period_type=$2 ORDER BY period_start,id`, topicID, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (s *Store) ReportDelivery(ctx context.Context, id int64) (ReportDelivery, error) {
	var d ReportDelivery
	d.ID = id
	err := s.db.QueryRow(ctx, `SELECT period_type,period_start,period_end,parts,message_ids FROM report_deliveries WHERE id=$1`, id).Scan(&d.Period, &d.Start, &d.End, &d.Parts, &d.MessageIDs)
	return d, err
}
func (s *Store) PrepareReportDelivery(ctx context.Context, id int64, parts []string) error {
	_, err := s.db.Exec(ctx, `UPDATE report_deliveries SET parts=$2 WHERE id=$1`, id, parts)
	return err
}
func (s *Store) AdvanceReportDelivery(ctx context.Context, id, messageID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE report_deliveries SET message_ids=array_append(message_ids,$2) WHERE id=$1`, id, messageID)
	return err
}
func (s *Store) DeleteReportDelivery(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `DELETE FROM report_deliveries WHERE id=$1`, id)
	return err
}
