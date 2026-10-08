package postgres

import (
	"context"
	"errors"
	"time"

	"dota-doggo/internal/domain"
	"github.com/jackc/pgx/v5"
)

const matchColumns = `player_id,match_id,start_time,end_time,hero_id,radiant_win,player_slot,kills,deaths,assists,gpm,xpm,
 hero_damage,tower_damage,hero_healing,last_hits,game_mode,lobby_type,party_size,raw_payload`

func scanMatch(row pgx.Row) (domain.Match, error) {
	var m domain.Match
	err := row.Scan(&m.PlayerID, &m.ID, &m.StartTime, &m.EndTime, &m.HeroID, &m.RadiantWin, &m.PlayerSlot, &m.Kills, &m.Deaths, &m.Assists, &m.GPM, &m.XPM, &m.HeroDamage, &m.TowerDamage, &m.HeroHealing, &m.LastHits, &m.GameMode, &m.LobbyType, &m.PartySize, &m.RawPayload)
	return m, err
}

// SaveMatches returns only newly inserted rows; notifications must instead use
// the topic cursor, because the stored history is shared between topics.
func (s *Store) SaveMatches(ctx context.Context, matches []domain.Match) (int, error) {
	inserted := 0
	for _, m := range matches {
		if m.PlayerID <= 0 || m.ID <= 0 || m.EndTime.Before(m.StartTime) {
			return inserted, errors.New("invalid match identity or timestamps")
		}
		raw := m.RawPayload
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		var fresh bool
		err := s.db.QueryRow(ctx, `INSERT INTO player_matches(`+matchColumns+`,created_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,now())
		 ON CONFLICT(player_id,match_id) DO UPDATE SET start_time=excluded.start_time,end_time=excluded.end_time,
		 hero_id=excluded.hero_id,radiant_win=excluded.radiant_win,player_slot=excluded.player_slot,
		 kills=excluded.kills,deaths=excluded.deaths,assists=excluded.assists,
		 gpm=CASE WHEN $21::text[] IS NULL OR 'gpm'=ANY($21) THEN excluded.gpm ELSE player_matches.gpm END,
		 xpm=CASE WHEN $21::text[] IS NULL OR 'xpm'=ANY($21) THEN excluded.xpm ELSE player_matches.xpm END,
		 hero_damage=CASE WHEN $21::text[] IS NULL OR 'hero_damage'=ANY($21) THEN excluded.hero_damage ELSE player_matches.hero_damage END,
		 tower_damage=CASE WHEN $21::text[] IS NULL OR 'tower_damage'=ANY($21) THEN excluded.tower_damage ELSE player_matches.tower_damage END,
		 hero_healing=CASE WHEN $21::text[] IS NULL OR 'hero_healing'=ANY($21) THEN excluded.hero_healing ELSE player_matches.hero_healing END,
		 last_hits=CASE WHEN $21::text[] IS NULL OR 'last_hits'=ANY($21) THEN excluded.last_hits ELSE player_matches.last_hits END,
		 game_mode=excluded.game_mode,lobby_type=excluded.lobby_type,
		 party_size=CASE WHEN $21::text[] IS NULL OR 'party_size'=ANY($21) THEN excluded.party_size ELSE player_matches.party_size END,
		 raw_payload=(player_matches.raw_payload::jsonb || excluded.raw_payload::jsonb)::json RETURNING (xmax=0)`,
			m.PlayerID, m.ID, m.StartTime, m.EndTime, m.HeroID, m.RadiantWin, m.PlayerSlot, m.Kills, m.Deaths, m.Assists, m.GPM, m.XPM, m.HeroDamage, m.TowerDamage, m.HeroHealing, m.LastHits, m.GameMode, m.LobbyType, m.PartySize, raw, m.PresentMetrics).Scan(&fresh)
		if err != nil {
			return inserted, err
		}
		if fresh {
			inserted++
		}
	}
	return inserted, nil
}
func collectMatches(rows pgx.Rows, err error) ([]domain.Match, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var matches []domain.Match
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}
func (s *Store) Matches(ctx context.Context, playerIDs []int64, start, end time.Time) ([]domain.Match, error) {
	return collectMatches(s.db.Query(ctx, "SELECT "+matchColumns+" FROM player_matches WHERE player_id=ANY($1) AND end_time >= $2 AND end_time < $3 ORDER BY end_time,match_id,player_id", playerIDs, start, end))
}

// RecentMatches limits unique matches, then includes all tracked participants.
// Unlike limiting player rows, a shared game cannot consume multiple slots.
func (s *Store) RecentMatches(ctx context.Context, selectedIDs, allIDs []int64, limit int) ([]domain.Match, error) {
	if limit < 1 {
		return nil, nil
	}
	return collectMatches(s.db.Query(ctx, `SELECT `+matchColumns+` FROM player_matches WHERE player_id=ANY($2) AND match_id IN (
	 SELECT match_id FROM player_matches WHERE player_id=ANY($1) GROUP BY match_id ORDER BY max(end_time) DESC,match_id DESC LIMIT $3)
	 ORDER BY end_time DESC,match_id DESC,player_id`, selectedIDs, allIDs, limit))
}
func (s *Store) Overview(ctx context.Context, playerIDs []int64) (domain.MatchesOverview, error) {
	var o domain.MatchesOverview
	err := s.db.QueryRow(ctx, "SELECT count(*),count(DISTINCT match_id),max(end_time) FROM player_matches WHERE player_id=ANY($1)", playerIDs).Scan(&o.TotalRows, &o.UniqueMatches, &o.LastEnd)
	return o, err
}
