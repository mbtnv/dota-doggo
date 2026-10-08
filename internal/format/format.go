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
func outcome(m domain.Match) string {
	if m.IsWin() {
		return "🟢<b>Win</b>"
	}
	return "🔴<b>Lose</b>"
}
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
func (f Formatter) Notification(group []Participant, c domain.Constants, zone string) string {
	if len(group) == 0 {
		return ""
	}
	if len(group) > 1 {
		return f.RecentMatch(group, c, zone)
	}
	p, m := group[0].Player, group[0].Match
	return strings.Join([]string{
		fmt.Sprintf("<b>%s</b> · <a href=\"%s\">profile</a>", escape(p.Label()), escape(ProfileURL(p))),
		outcome(m) + " · " + escape(Hero(m.HeroID, c)), f.kda(m),
		fmt.Sprintf("<b>Ended</b>: %s (%d min)", Datetime(m.EndTime, zone), int(m.Duration().Minutes())),
		fmt.Sprintf("<b>GPM/XPM</b>: %d / %d", m.GPM, m.XPM), damage(m), fmt.Sprintf("<b>Last hits</b>: %d", m.LastHits),
		"<b>Mode</b>: " + escape(Mode(m.GameMode, c)) + " · " + escape(Lobby(m.LobbyType, c)) + party(m),
		"<a href=\"" + matchURL(m.ID) + "\">Dotabuff</a>"}, "\n")
}
func (f Formatter) RecentMatch(group []Participant, c domain.Constants, zone string) string {
	if len(group) == 0 {
		return ""
	}
	m := group[0].Match
	labels := make([]string, len(group))
	for i, p := range group {
		labels[i] = escape(p.Player.Label())
	}
	parts := []string{"<b>" + strings.Join(labels, ", ") + "</b>", fmt.Sprintf("<b>Ended</b>: %s (%d min)", Datetime(m.EndTime, zone), int(m.Duration().Minutes())), "<b>Mode</b>: " + escape(Mode(m.GameMode, c)) + " · " + escape(Lobby(m.LobbyType, c))}
	for _, p := range group {
		m := p.Match
		parts = append(parts, fmt.Sprintf("<b>%s</b> · <a href=\"%s\">profile</a> · %s · %s%s", escape(p.Player.Label()), escape(ProfileURL(p.Player)), outcome(m), escape(Hero(m.HeroID, c)), party(m)),
			fmt.Sprintf("%s | <b>GPM/XPM</b>: %d / %d | <b>Last hits</b>: %d", f.kda(m), m.GPM, m.XPM, m.LastHits), damage(m))
	}
	return strings.Join(append(parts, "<a href=\""+matchURL(m.ID)+"\">Dotabuff</a>"), "\n")
}
func (f Formatter) Report(s domain.Summary, c domain.Constants) string {
	var heroes []string
	for _, h := range s.TopHeroes {
		heroes = append(heroes, fmt.Sprintf("%s x%d", Hero(h[0], c), h[1]))
	}
	top := strings.Join(heroes, ", ")
	if top == "" {
		top = "n/a"
	}
	return fmt.Sprintf("<b>%s</b> · %s\nMatches: %d | W/L: %d/%d | WR: %.2f%%\nK/D/A avg: %.2f/%.2f/%.2f\nGPM/XPM avg: %.2f/%.2f\nAvg duration: %.2f min\nAvg hero damage: %.2f\nBest/Worst streak: %d/%d\nTop heroes: %s", escape(s.Label), escape(string(s.Period)), s.Matches, s.Wins, s.Losses, s.Winrate, s.AvgKills, s.AvgDeaths, s.AvgAssists, s.AvgGPM, s.AvgXPM, s.AvgDuration, s.AvgHeroDamage, s.BestStreak, s.WorstStreak, escape(top))
}
func (f Formatter) ReportSections(summaries []domain.Summary, c domain.Constants) []string {
	out := make([]string, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, f.Report(s, c))
	}
	return out
}
func (f Formatter) Leaderboard(title string, summaries []domain.Summary) string {
	lines := []string{"<b>" + escape(title) + "</b>"}
	if len(summaries) == 0 {
		return lines[0] + "\nNo data."
	}
	for i, s := range summaries {
		lines = append(lines, fmt.Sprintf("%d. %s | matches %d | W %d | WR %.2f%%", i+1, escape(s.Label), s.Matches, s.Wins, s.Winrate))
	}
	return strings.Join(lines, "\n")
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
