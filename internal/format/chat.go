package format

import (
	"fmt"
	"strconv"

	"dota-doggo/internal/domain"
)

func (f Formatter) MatchParts(group []Participant, c domain.Constants, zone string) ([]string, error) {
	if len(group) == 0 {
		return []string{}, nil
	}
	m := group[0].Match
	header := fmt.Sprintf("<h2>🎮 Матч #%d</h2><p>%s · %d мин</p><p>%s · %s</p>", m.ID, escape(Datetime(m.EndTime, zone)), int(m.Duration().Minutes()), escape(Mode(m.GameMode, c)), escape(Lobby(m.LobbyType, c)))
	footer := `<p><a href="` + matchURL(m.ID) + `">Открыть матч в Dotabuff</a></p>`
	var entries []richEntry
	for _, p := range group {
		m := p.Match
		result := "🔴 Поражение"
		if m.IsWin() {
			result = "🟢 Победа"
		}
		row := fmt.Sprintf(`<td><a href="%s">%s</a></td><td>%s</td><td>%s</td><td>%d/%d/%d</td>`, escape(ProfileURL(p.Player)), escape(p.Player.Label()), escape(Hero(m.HeroID, c)), result, m.Kills, m.Deaths, m.Assists)
		details := fmt.Sprintf("<details><summary>%s · подробности</summary><p>%s · %s%s</p><p>%s</p><p><b>GPM/XPM</b>: %d / %d · <b>Last hits</b>: %d</p><p>%s</p></details>", escape(p.Player.Label()), result, escape(Hero(m.HeroID, c)), party(m), f.kda(m), m.GPM, m.XPM, m.LastHits, damage(m))
		entries = append(entries, richEntry{row: row, details: details})
	}
	return richTableParts(header, "<th>Игрок</th><th>Герой</th><th>Итог</th><th>K/D/A</th>", entries, footer)
}

func PlayerParts(players []domain.Player) ([]string, error) {
	var entries []richEntry
	for _, p := range players {
		entries = append(entries, richEntry{row: fmt.Sprintf(`<td><a href="%s">%s</a></td><td><code>%d</code></td>`, escape(ProfileURL(p)), escape(p.Label()), p.AccountID)})
	}
	header := fmt.Sprintf("<h2>👥 Игроки</h2><p>Отслеживается: %d</p>", len(players))
	return richTableParts(header, "<th>Игрок</th><th>Account ID</th>", entries, "<footer>Добавление: /track · Удаление: /untrack</footer>")
}

func LeaderParts(summaries []domain.Summary, period domain.Period, zone string) ([]string, error) {
	title := map[domain.Period]string{domain.Day: "Лидеры дня", domain.Week: "Лидеры недели", domain.Month: "Лидеры месяца"}[period]
	header := "<h2>🏆 " + title + "</h2>"
	if len(summaries) > 0 {
		s := summaries[0]
		header += "<p>" + escape(Datetime(s.Start, zone)) + " — " + escape(Datetime(s.End, zone)) + "</p>"
	}
	var entries []richEntry
	for i, s := range summaries {
		rank := strconv.Itoa(i + 1)
		if i < 3 {
			rank = []string{"🥇", "🥈", "🥉"}[i]
		}
		entries = append(entries, richEntry{row: fmt.Sprintf("<td>%s %s</td><td>%d</td><td>%d–%d</td><td>%.2f%%</td>", rank, escape(s.Label), s.Matches, s.Wins, s.Losses, s.Winrate)})
	}
	return richTableParts(header, "<th>Игрок</th><th>Игры</th><th>В–П</th><th>WR</th>", entries, "<footer>По винрейту, затем по победам и числу матчей.</footer>")
}
