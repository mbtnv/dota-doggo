package domain

import (
	"errors"
	"sort"
	"strconv"
	"time"
)

type Period string

const (
	Day   Period = "day"
	Week  Period = "week"
	Month Period = "month"
)

func (p Period) Valid() bool { return p == Day || p == Week || p == Month }

// Bounds uses calendar arithmetic, never fixed 24-hour durations across DST.
func (p Period) Bounds(now time.Time, timezone string, previous bool) (time.Time, time.Time, error) {
	if !p.Valid() {
		return time.Time{}, time.Time{}, errors.New("unknown report period")
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("unknown IANA timezone")
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	if p == Week {
		offset := (int(start.Weekday()) + 6) % 7
		start = start.AddDate(0, 0, -offset)
	}
	if p == Month {
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	}
	shift := func(t time.Time, by int) time.Time {
		switch p {
		case Day:
			return t.AddDate(0, 0, by)
		case Week:
			return t.AddDate(0, 0, 7*by)
		default:
			return t.AddDate(0, by, 0)
		}
	}
	end := shift(start, 1)
	if previous {
		end = start
		start = shift(start, -1)
	}
	return start.UTC(), end.UTC(), nil
}

type Summary struct {
	PlayerID      int64     `json:"player_id"`
	Label         string    `json:"label"`
	Period        Period    `json:"period_type"`
	Start         time.Time `json:"period_start"`
	End           time.Time `json:"period_end"`
	Matches       int       `json:"matches_count"`
	Wins          int       `json:"wins"`
	Losses        int       `json:"losses"`
	Winrate       float64   `json:"winrate"`
	AvgKills      float64   `json:"avg_kills"`
	AvgDeaths     float64   `json:"avg_deaths"`
	AvgAssists    float64   `json:"avg_assists"`
	AvgGPM        float64   `json:"avg_gpm"`
	AvgXPM        float64   `json:"avg_xpm"`
	AvgDuration   float64   `json:"avg_duration_minutes"`
	AvgHeroDamage float64   `json:"avg_hero_damage"`
	BestStreak    int       `json:"best_streak"`
	WorstStreak   int       `json:"worst_streak"`
	TopHeroes     [][2]int  `json:"top_heroes"`
}

// Summarize filters by player and half-open interval itself, so callers cannot
// accidentally count another player's matches or the next calendar period.
func Summarize(player Player, period Period, start, end time.Time, matches []Match) Summary {
	s := Summary{PlayerID: player.ID, Label: player.Label(), Period: period, Start: start, End: end, TopHeroes: [][2]int{}}
	selected := make([]Match, 0, len(matches))
	heroes := map[int]int{}
	for _, m := range matches {
		if m.PlayerID == player.ID && !m.EndTime.Before(start) && m.EndTime.Before(end) {
			selected = append(selected, m)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].EndTime.Equal(selected[j].EndTime) {
			return selected[i].ID < selected[j].ID
		}
		return selected[i].EndTime.Before(selected[j].EndTime)
	})
	wins, losses := 0, 0
	for _, m := range selected {
		s.Matches++
		heroes[m.HeroID]++
		if m.IsWin() {
			s.Wins++
			wins++
			losses = 0
		} else {
			wins = 0
			losses++
		}
		s.BestStreak = max(s.BestStreak, wins)
		s.WorstStreak = max(s.WorstStreak, losses)
		s.AvgKills += float64(m.Kills)
		s.AvgDeaths += float64(m.Deaths)
		s.AvgAssists += float64(m.Assists)
		s.AvgGPM += float64(m.GPM)
		s.AvgXPM += float64(m.XPM)
		s.AvgDuration += m.Duration().Minutes()
		s.AvgHeroDamage += float64(m.HeroDamage)
	}
	s.Losses = s.Matches - s.Wins
	if s.Matches > 0 {
		n := float64(s.Matches)
		s.Winrate = float64(s.Wins) / n * 100
		s.AvgKills /= n
		s.AvgDeaths /= n
		s.AvgAssists /= n
		s.AvgGPM /= n
		s.AvgXPM /= n
		s.AvgDuration /= n
		s.AvgHeroDamage /= n
	}
	for hero, count := range heroes {
		s.TopHeroes = append(s.TopHeroes, [2]int{hero, count})
	}
	// Equal counts have a deterministic hero-ID order, independent of SQL row order.
	sort.Slice(s.TopHeroes, func(i, j int) bool {
		if s.TopHeroes[i][1] == s.TopHeroes[j][1] {
			return s.TopHeroes[i][0] < s.TopHeroes[j][0]
		}
		return s.TopHeroes[i][1] > s.TopHeroes[j][1]
	})
	if len(s.TopHeroes) > 3 {
		s.TopHeroes = s.TopHeroes[:3]
	}
	return s
}

func SelectPlayers(players []Player, filter string) []Player {
	if filter == "" {
		return players
	}
	// A numeric alias must never shadow another player's explicit account ID.
	for _, p := range players {
		if strconv.FormatInt(p.AccountID, 10) == filter {
			return []Player{p}
		}
	}
	var selected []Player
	for _, p := range players {
		if p.Alias != nil && *p.Alias == filter {
			selected = append(selected, p)
		}
	}
	return selected
}
