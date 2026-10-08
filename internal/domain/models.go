package domain

import (
	"encoding/json"
	"time"
)

type Topic struct {
	ID        int64
	ChatID    int64
	ThreadID  *int64
	Title     *string
	Timezone  string
	Paused    bool
	CreatedAt time.Time
}

type Player struct {
	ID              int64   `json:"player_id"`
	AccountID       int64   `json:"dota_account_id"`
	DisplayName     string  `json:"display_name"`
	ProfileURL      *string `json:"profile_url"`
	Alias           *string `json:"alias"`
	LastSeenMatchID *int64  `json:"last_seen_match_id"`
}

func (p Player) Label() string {
	if p.Alias != nil && *p.Alias != "" {
		return *p.Alias
	}
	return p.DisplayName
}

type Match struct {
	PlayerID    int64           `json:"player_id"`
	ID          int64           `json:"match_id"`
	StartTime   time.Time       `json:"start_time"`
	EndTime     time.Time       `json:"end_time"`
	HeroID      int             `json:"hero_id"`
	RadiantWin  bool            `json:"radiant_win"`
	PlayerSlot  int             `json:"player_slot"`
	Kills       int             `json:"kills"`
	Deaths      int             `json:"deaths"`
	Assists     int             `json:"assists"`
	GPM         int             `json:"gpm"`
	XPM         int             `json:"xpm"`
	HeroDamage  int             `json:"hero_damage"`
	TowerDamage int             `json:"tower_damage"`
	HeroHealing int             `json:"hero_healing"`
	LastHits    int             `json:"last_hits"`
	GameMode    int             `json:"game_mode"`
	LobbyType   int             `json:"lobby_type"`
	PartySize   *int            `json:"party_size"`
	RawPayload  json.RawMessage `json:"raw_payload"`
	// nil means a complete snapshot; an explicit (possibly empty) slice records
	// which optional metrics were present in a sparse external API response.
	PresentMetrics []string `json:"-"`
}

func (m Match) IsWin() bool             { return m.RadiantWin == (m.PlayerSlot < 128) }
func (m Match) Duration() time.Duration { return m.EndTime.Sub(m.StartTime) }

type RuntimeStatus struct {
	StartedAt, FinishedAt, SucceededAt *time.Time
	Error                              *string
}
type MatchesOverview struct {
	TotalRows, UniqueMatches int64
	LastEnd                  *time.Time
}
type ReportRun struct {
	Period, Trigger       string
	Start, End, CreatedAt time.Time
	MessageID             *int64
}
type ConstantEntry struct {
	Resource   string
	Code       int
	Name       string
	RawPayload json.RawMessage
}
type Constants struct{ Heroes, GameModes, LobbyTypes map[int]string }
