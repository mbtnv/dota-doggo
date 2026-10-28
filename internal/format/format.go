// Package format renders domain data into escaped Telegram HTML.
package format

import (
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"
	"time"

	"dota-doggo/internal/domain"
)

type Participant struct {
	Player domain.Player
	Match  domain.Match
}
type Formatter struct{ Calculators []domain.Calculator }

var heroNames = map[int]string{1: "Anti-Mage", 5: "Crystal Maiden", 74: "Invoker", 138: "Muerta"}
var modeNames = map[int]string{1: "All Pick", 2: "Captains Mode", 22: "All Pick", 23: "Turbo"}
var lobbyNames = map[int]string{0: "Normal", 7: "Ranked", 9: "Battle Cup"}

func escape(s string) string { return html.EscapeString(s) }
func ProfileURL(p domain.Player) string {
	if p.ProfileURL != nil {
		u, err := url.Parse(*p.ProfileURL)
		if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil {
			return u.String()
		}
	}
	return fmt.Sprintf("https://www.dotabuff.com/players/%d", p.AccountID)
}
func matchURL(id int64) string { return fmt.Sprintf("https://www.dotabuff.com/matches/%d", id) }
func name(code int, cache, fallback map[int]string, unknown string) string {
	if s := cache[code]; s != "" {
		return s
	}
	if s := fallback[code]; s != "" {
		return s
	}
	return fmt.Sprintf(unknown, code)
}
func Hero(code int, c domain.Constants) string { return name(code, c.Heroes, heroNames, "Hero #%d") }
func Mode(code int, c domain.Constants) string {
	if code == 22 {
		return "All Pick"
	}
	return name(code, c.GameModes, modeNames, "%d")
}
func Lobby(code int, c domain.Constants) string { return name(code, c.LobbyTypes, lobbyNames, "%d") }
func party(m domain.Match) string {
	if m.PartySize == nil {
		return ""
	}
	if *m.PartySize == 1 {
		return " · Solo"
	}
	return fmt.Sprintf(" · Party %d", *m.PartySize)
}
func Datetime(at time.Time, zone string) string {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	return at.In(loc).Format("2006-01-02 15:04 MST")
}
func damage(m domain.Match) string {
	return fmt.Sprintf("<b>HD/TD/HH</b>: %.1fK / %.1fK / %.1fK", float64(m.HeroDamage)/1000, float64(m.TowerDamage)/1000, float64(m.HeroHealing)/1000)
}
func (f Formatter) kda(m domain.Match) string {
	parts := []string{fmt.Sprintf("<b>KDA</b>: %d/%d/%d", m.Kills, m.Deaths, m.Assists)}
	for _, s := range domain.CalculateStatistics(m, f.Calculators...) {
		parts = append(parts, "<b>"+escape(s.Label)+"</b>: "+escape(s.Value))
	}
	return strings.Join(parts, " | ")
}

// GroupMatches orders games newest first and participants in the caller's player
// order (which may put a /last filter first). Unknown players are excluded.
func GroupMatches(players []domain.Player, matches []domain.Match) [][]Participant {
	byPlayer := map[int64]domain.Player{}
	order := map[int64]int{}
	for i, p := range players {
		byPlayer[p.ID] = p
		order[p.ID] = i
	}
	groups := map[int64][]Participant{}
	seen := map[[2]int64]bool{}
	for _, m := range matches {
		p, ok := byPlayer[m.PlayerID]
		key := [2]int64{m.PlayerID, m.ID}
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		groups[m.ID] = append(groups[m.ID], Participant{p, m})
	}
	out := make([][]Participant, 0, len(groups))
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool { return order[g[i].Player.ID] < order[g[j].Player.ID] })
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i][0].Match, out[j][0].Match
		if a.EndTime.Equal(b.EndTime) {
			return a.ID > b.ID
		}
		return a.EndTime.After(b.EndTime)
	})
	return out
}
