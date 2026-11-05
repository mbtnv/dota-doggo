package format

import (
	"fmt"
	"strings"

	"dota-doggo/internal/domain"
)

type matchPlayer struct {
	name, hero, stats, details string
	won                        bool
}

func (f Formatter) MatchParts(group []Participant, c domain.Constants, zone string) ([]string, error) {
	if len(group) == 0 {
		return []string{}, nil
	}
	m := group[0].Match
	end := localTime(m.EndTime, zone)
	mode := escape(Mode(m.GameMode, c))
	if m.LobbyType != 0 {
		mode += " · " + escape(Lobby(m.LobbyType, c))
	}
	context := fmt.Sprintf("<p>%s · %d мин · конец в %s</p>", mode, int(m.Duration().Minutes()), escape(end.Format("15:04 MST")))
	footer := fmt.Sprintf(`<p><a href="%s">Матч в Dotabuff</a> · %s</p>`, matchURL(m.ID), end.Format("02.01.2006"))

	players := make([]matchPlayer, 0, len(group))
	for _, p := range group {
		m := p.Match
		stats := []string{fmt.Sprintf("<b>%d/%d/%d</b> K/D/A", m.Kills, m.Deaths, m.Assists)}
		for _, s := range domain.CalculateStatistics(m, f.Calculators...) {
			stats = append(stats, "<b>"+escape(s.Value)+"</b> "+escape(s.Label))
		}
		details := fmt.Sprintf("<p>GPM/XPM · %d / %d<br>Добито крипов · %d<br>Урон героям · %d<br>Урон строениям · %d<br>Лечение · %d</p>", m.GPM, m.XPM, m.LastHits, m.HeroDamage, m.TowerDamage, m.HeroHealing)
		details += fmt.Sprintf(`<p><a href="%s">Профиль игрока</a>%s</p>`, escape(ProfileURL(p.Player)), party(m))
		players = append(players, matchPlayer{
			name: escape(p.Player.Label()), hero: escape(Hero(m.HeroID, c)),
			stats: strings.Join(stats, " · "), details: details, won: m.IsWin(),
		})
	}

	return paginateRich(players, 10, func(page []matchPlayer) string {
		if len(page) == 1 {
			p := page[0]
			return "<p><b>" + matchResult([]string{p.name}, p.won) + " на " + p.hero + "</b></p><p>" + p.stats + "</p>" + context +
				"<details><summary>Статистика игрока</summary>" + p.details + "</details>" + footer
		}
		// Keep the /last filter's player first even when the teams were opponents.
		firstWon := page[0].won
		var first, opponents []string
		var body strings.Builder
		for _, p := range page {
			if p.won == firstWon {
				first = append(first, p.name)
			} else {
				opponents = append(opponents, p.name)
			}
			fmt.Fprintf(&body, "<details><summary><b>%s</b> · %s<br>%s</summary>%s</details>", p.name, p.hero, p.stats, p.details)
		}
		title := matchResult(first, firstWon)
		if len(opponents) > 0 {
			title += "<br>" + matchResult(opponents, !firstWon)
		}
		return "<p><b>" + title + "</b></p>" + context + body.String() + footer
	})
}

func matchResult(names []string, won bool) string {
	icon, verb := "🔴 ", "проиграл"
	if won {
		icon, verb = "🟢 ", "выиграл"
	}
	if len(names) == 1 {
		return icon + names[0] + " " + verb
	}
	last := len(names) - 1
	return icon + strings.Join(names[:last], ", ") + " и " + names[last] + " " + verb + "и"
}
