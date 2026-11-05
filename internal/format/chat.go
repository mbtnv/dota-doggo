package format

import (
	"fmt"
	"strconv"

	"dota-doggo/internal/domain"
)

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
