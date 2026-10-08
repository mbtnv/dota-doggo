package opendota

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"dota-doggo/internal/domain"
)

type accountID int64

func (id *accountID) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var text string
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &text); err != nil {
			return err
		}
	} else {
		text = string(b)
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n < 0 {
		return errors.New("invalid OpenDota account ID")
	}
	*id = accountID(n)
	return nil
}

type Profile struct {
	AccountID int64
	Name      string
	URL       *string
}

// MatchData distinguishes missing optional metrics from real zero values.
type MatchData struct {
	AccountID   *accountID `json:"account_id,omitempty"`
	MatchID     *int64     `json:"match_id,omitempty"`
	PlayerSlot  *int       `json:"player_slot,omitempty"`
	RadiantWin  *bool      `json:"radiant_win,omitempty"`
	Duration    *int64     `json:"duration,omitempty"`
	StartTime   *int64     `json:"start_time,omitempty"`
	HeroID      *int       `json:"hero_id,omitempty"`
	GameMode    *int       `json:"game_mode,omitempty"`
	LobbyType   *int       `json:"lobby_type,omitempty"`
	Kills       *int       `json:"kills,omitempty"`
	Deaths      *int       `json:"deaths,omitempty"`
	Assists     *int       `json:"assists,omitempty"`
	GPM         *int       `json:"gold_per_min,omitempty"`
	XPM         *int       `json:"xp_per_min,omitempty"`
	HeroDamage  *int       `json:"hero_damage,omitempty"`
	TowerDamage *int       `json:"tower_damage,omitempty"`
	HeroHealing *int       `json:"hero_healing,omitempty"`
	LastHits    *int       `json:"last_hits,omitempty"`
	PartySize   *int       `json:"party_size,omitempty"`
}

func value[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

func (m MatchData) Snapshot(playerID int64) (domain.Match, error) {
	if playerID <= 0 || m.MatchID == nil || *m.MatchID <= 0 || m.PlayerSlot == nil || *m.PlayerSlot < 0 || *m.PlayerSlot > 255 || m.RadiantWin == nil || m.Duration == nil || *m.Duration < 0 || *m.Duration > math.MaxInt64/int64(time.Second) || m.StartTime == nil || *m.StartTime <= 0 || m.HeroID == nil || m.GameMode == nil || m.LobbyType == nil || m.Kills == nil || m.Deaths == nil || m.Assists == nil {
		return domain.Match{}, fmt.Errorf("incomplete or invalid match %d", value(m.MatchID))
	}
	start := time.Unix(*m.StartTime, 0).UTC()
	raw, err := json.Marshal(m)
	if err != nil {
		return domain.Match{}, err
	}
	fields := make([]string, 0, 7)
	for _, field := range []struct {
		name  string
		value *int
	}{{"gpm", m.GPM}, {"xpm", m.XPM}, {"hero_damage", m.HeroDamage}, {"tower_damage", m.TowerDamage}, {"hero_healing", m.HeroHealing}, {"last_hits", m.LastHits}, {"party_size", m.PartySize}} {
		if field.value != nil {
			fields = append(fields, field.name)
		}
	}
	return domain.Match{PlayerID: playerID, ID: *m.MatchID, StartTime: start, EndTime: start.Add(time.Duration(*m.Duration) * time.Second), HeroID: *m.HeroID, RadiantWin: *m.RadiantWin, PlayerSlot: *m.PlayerSlot, Kills: *m.Kills, Deaths: *m.Deaths, Assists: *m.Assists, GPM: value(m.GPM), XPM: value(m.XPM), HeroDamage: value(m.HeroDamage), TowerDamage: value(m.TowerDamage), HeroHealing: value(m.HeroHealing), LastHits: value(m.LastHits), GameMode: *m.GameMode, LobbyType: *m.LobbyType, PartySize: m.PartySize, RawPayload: raw, PresentMetrics: fields}, nil
}

// inherit fills only match-level values. These normally live on the root of
// /matches/{id}, whereas player metrics live in its players array.
func (m *MatchData) inherit(root MatchData) {
	if m.MatchID == nil {
		m.MatchID = root.MatchID
	}
	if m.StartTime == nil {
		m.StartTime = root.StartTime
	}
	if m.Duration == nil {
		m.Duration = root.Duration
	}
	if m.RadiantWin == nil {
		m.RadiantWin = root.RadiantWin
	}
	if m.GameMode == nil {
		m.GameMode = root.GameMode
	}
	if m.LobbyType == nil {
		m.LobbyType = root.LobbyType
	}
}

func SelectMatchPlayer(players []MatchData, account int64, slot int) (MatchData, bool) {
	for _, p := range players {
		if p.AccountID != nil && int64(*p.AccountID) == account {
			return p, true
		}
	}
	for _, p := range players {
		if p.PlayerSlot != nil && *p.PlayerSlot == slot {
			return p, true
		}
	}
	return MatchData{}, false
}
