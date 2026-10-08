package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"dota-doggo/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) Runtime(ctx context.Context, topicID int64) (domain.RuntimeStatus, error) {
	var state domain.RuntimeStatus
	err := s.db.QueryRow(ctx, "SELECT last_poll_started_at,last_poll_finished_at,last_poll_succeeded_at,last_poll_error FROM topic_runtime_state WHERE topic_id=$1", topicID).Scan(&state.StartedAt, &state.FinishedAt, &state.SucceededAt, &state.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	return state, err
}
func (s *Store) MarkStarted(ctx context.Context, topicID int64, at time.Time) error {
	_, err := s.db.Exec(ctx, `INSERT INTO topic_runtime_state(topic_id,last_poll_started_at,created_at,updated_at)
	 VALUES($1,$2,now(),now()) ON CONFLICT(topic_id) DO UPDATE SET last_poll_started_at=$2,last_poll_error=NULL,updated_at=now()`, topicID, at)
	return err
}
func (s *Store) MarkFinished(ctx context.Context, topicID int64, started, finished time.Time, message *string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO topic_runtime_state(topic_id,last_poll_started_at,last_poll_finished_at,last_poll_succeeded_at,last_poll_error,created_at,updated_at)
	 VALUES($1,$2,$3::timestamptz,CASE WHEN $4::text IS NULL THEN $3::timestamptz ELSE NULL END,$4,now(),now())
	 ON CONFLICT(topic_id) DO UPDATE SET last_poll_started_at=$2,last_poll_finished_at=$3,
	 last_poll_succeeded_at=CASE WHEN $4::text IS NULL THEN $3::timestamptz ELSE topic_runtime_state.last_poll_succeeded_at END,
	 last_poll_error=$4,updated_at=now()`, topicID, started, finished, message)
	return err
}
func (s *Store) HasReport(ctx context.Context, topicID int64, period string, start, end time.Time) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM report_runs WHERE topic_id=$1 AND period_type=$2 AND period_start=$3 AND period_end=$4)", topicID, period, start, end).Scan(&exists)
	return exists, err
}
func (s *Store) RecordReport(ctx context.Context, topicID int64, run domain.ReportRun) error {
	_, err := s.db.Exec(ctx, `INSERT INTO report_runs(topic_id,period_type,period_start,period_end,trigger_source,telegram_message_id,created_at)
	 VALUES($1,$2,$3,$4,$5,$6,now())`, topicID, run.Period, run.Start, run.End, run.Trigger, run.MessageID)
	return err
}
func (s *Store) LatestReports(ctx context.Context, topicID int64) ([]domain.ReportRun, error) {
	rows, err := s.db.Query(ctx, `SELECT DISTINCT ON(period_type) period_type,trigger_source,period_start,period_end,created_at,telegram_message_id
	 FROM report_runs WHERE topic_id=$1 AND period_type IN ('day','week','month') ORDER BY period_type,created_at DESC,id DESC`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ReportRun
	for rows.Next() {
		var r domain.ReportRun
		if err := rows.Scan(&r.Period, &r.Trigger, &r.Start, &r.End, &r.CreatedAt, &r.MessageID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) ConstantsUpdatedAt(ctx context.Context, resource string) (*time.Time, error) {
	var at *time.Time
	err := s.db.QueryRow(ctx, "SELECT max(updated_at) FROM constant_entries WHERE resource=$1", resource).Scan(&at)
	return at, err
}
func (s *Store) SaveConstants(ctx context.Context, entries []domain.ConstantEntry) error {
	if len(entries) == 0 {
		return nil
	}
	type row struct {
		Resource   string          `json:"resource"`
		Code       int             `json:"code"`
		Name       string          `json:"name"`
		RawPayload json.RawMessage `json:"raw_payload"`
	}
	rows := make([]row, 0, len(entries))
	for _, e := range entries {
		raw := e.RawPayload
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		rows = append(rows, row{e.Resource, e.Code, e.Name, raw})
	}
	payload, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO constant_entries(resource,code,name,raw_payload,created_at,updated_at)
	 SELECT resource,code,name,raw_payload::json,now(),now() FROM jsonb_to_recordset($1::jsonb) AS x(resource text,code integer,name text,raw_payload jsonb)
	 ON CONFLICT(resource,code) DO UPDATE SET name=excluded.name,raw_payload=excluded.raw_payload,updated_at=now()`, payload)
	return err
}
func (s *Store) Constants(ctx context.Context) (domain.Constants, error) {
	c := domain.Constants{Heroes: map[int]string{}, GameModes: map[int]string{}, LobbyTypes: map[int]string{}}
	rows, err := s.db.Query(ctx, "SELECT resource,code,name FROM constant_entries ORDER BY resource,code")
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var resource, name string
		var code int
		if err := rows.Scan(&resource, &code, &name); err != nil {
			return c, err
		}
		switch resource {
		case "heroes":
			c.Heroes[code] = name
		case "game_mode":
			c.GameModes[code] = name
		case "lobby_type":
			c.LobbyTypes[code] = name
		}
	}
	return c, rows.Err()
}
