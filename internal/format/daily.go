package format

import (
	"fmt"
	"strings"
	"time"

	"dota-doggo/internal/domain"
)

// DailyReportParts retains the daily entry point while all report periods share
// the same layout and persisted message format.
func (f Formatter) DailyReportParts(summaries []domain.Summary, c domain.Constants, zone string) ([]string, error) {
	return f.ReportParts(summaries, domain.Day, c, zone)
}

func (f Formatter) ReportParts(summaries []domain.Summary, period domain.Period, c domain.Constants, zone string) ([]string, error) {
	if len(summaries) == 0 {
		return []string{}, nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	start, end := summaries[0].Start, summaries[0].End
	title := map[domain.Period]string{domain.Day: "Дневной отчёт", domain.Week: "Недельный отчёт", domain.Month: "Месячный отчёт"}[period]
	if title == "" {
		return nil, fmt.Errorf("unknown report period")
	}
	date := start.In(loc).Format("02.01.2006")
	if period != domain.Day {
		date += " — " + end.In(loc).AddDate(0, 0, -1).Format("02.01.2006")
	}
	header := fmt.Sprintf("<h2>🐾 %s</h2><p><b>%s</b> · %s</p><p>Игроков: %d · Результаты по игрокам</p>", title, date, escape(zone), len(summaries))
	footer := fmt.Sprintf("<footer>Период: %s — %s. Матчи учитываются по времени окончания.</footer>", escape(Datetime(start, zone)), escape(Datetime(end, zone)))
	var entries []richEntry
	for _, s := range summaries {
		entries = append(entries, richEntry{row: fmt.Sprintf("<td>%s</td><td>%d</td><td>%d–%d</td><td>%.0f%%</td>", escape(s.Label), s.Matches, s.Wins, s.Losses, s.Winrate), details: f.summaryDetails(s, c)})
	}
	return richTableParts(header, "<th>Игрок</th><th>Игры</th><th>В–П</th><th>WR</th>", entries, footer)
}

func (f Formatter) summaryDetails(s domain.Summary, c domain.Constants) string {
	var b strings.Builder
	var heroes []string
	for _, hero := range s.TopHeroes {
		heroes = append(heroes, fmt.Sprintf("%s ×%d", Hero(hero[0], c), hero[1]))
	}
	top := strings.Join(heroes, ", ")
	if top == "" {
		top = "нет данных"
	}
	fmt.Fprintf(&b, "<details><summary>%s · подробности</summary><p><b>Матчи:</b> %d · Победы: %d · Поражения: %d · Винрейт: %.2f%%</p><p><b>Средние K / D / A:</b> %.1f / %.1f / %.1f</p><p><b>GPM / XPM:</b> %.0f / %.0f · <b>Урон героям:</b> %.0f</p><p><b>Средняя длительность:</b> %.1f мин</p><p><b>Лучшие серии:</b> %d побед / %d поражений</p><p><b>Герои:</b> %s</p></details>", escape(s.Label), s.Matches, s.Wins, s.Losses, s.Winrate, s.AvgKills, s.AvgDeaths, s.AvgAssists, s.AvgGPM, s.AvgXPM, s.AvgHeroDamage, s.AvgDuration, s.BestStreak, s.WorstStreak, escape(top))
	return b.String()
}
