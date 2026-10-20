package format

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dota-doggo/internal/domain"
)

// ReportPart is the persisted representation of a rich report part. Legacy
// deliveries remain plain HTML strings; keeping the format in each snapshot
// lets a restarted worker resume them without reformatting or a schema change.
type ReportPart struct {
	Format string `json:"format"`
	HTML   string `json:"html"`
}

const RichHTMLFormat = "telegram-rich-html-v1"

func DecodeReportPart(part string) (ReportPart, error) {
	if !strings.HasPrefix(part, "{") {
		return ReportPart{HTML: part}, nil
	}
	var p ReportPart
	if err := json.Unmarshal([]byte(part), &p); err != nil {
		return p, errors.New("invalid report snapshot")
	}
	if p.Format != RichHTMLFormat || p.HTML == "" {
		return p, errors.New("unsupported report snapshot format")
	}
	return p, nil
}

// DailyReportParts keeps complete table rows and player details together. The
// conservative regular-message text budget also keeps reports readable on a
// phone; rich tables must never be split with the regular HTML tag splitter.
func (f Formatter) DailyReportParts(summaries []domain.Summary, c domain.Constants, zone string) ([]string, error) {
	if len(summaries) == 0 {
		return []string{}, nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	start, end := summaries[0].Start, summaries[0].End
	header := fmt.Sprintf("<h2>🐾 Дневной отчёт</h2><p><b>%s</b> · %s</p><p>Играли: %d · Результаты по игрокам</p>", start.In(loc).Format("02.01.2006"), escape(zone), len(summaries))
	footer := fmt.Sprintf("<footer>Период: %s — %s. Матчи учитываются по времени окончания.</footer>", escape(Datetime(start, zone)), escape(Datetime(end, zone)))
	var out []string
	var batch []domain.Summary
	flush := func() error {
		html := header + f.dailyPlayers(batch, c) + footer
		part, err := json.Marshal(ReportPart{Format: RichHTMLFormat, HTML: html})
		if err != nil {
			return err
		}
		out = append(out, string(part))
		return nil
	}
	for _, s := range summaries {
		candidate := append(append([]domain.Summary(nil), batch...), s)
		n, err := HTMLLength(header + f.dailyPlayers(candidate, c) + footer)
		if err != nil {
			return nil, err
		}
		if len(batch) > 0 && (len(candidate) > 8 || n > TelegramTextLimit) {
			if err := flush(); err != nil {
				return nil, err
			}
			batch = nil
		}
		batch = append(batch, s)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

func (f Formatter) dailyPlayers(summaries []domain.Summary, c domain.Constants) string {
	var b strings.Builder
	b.WriteString(`<table striped compact><tr><th>Игрок</th><th>Игры</th><th>В–П</th><th>WR</th></tr>`)
	for _, s := range summaries {
		fmt.Fprintf(&b, "<tr><td>%s</td><td>%d</td><td>%d–%d</td><td>%.0f%%</td></tr>", escape(s.Label), s.Matches, s.Wins, s.Losses, s.Winrate)
	}
	b.WriteString("</table>")
	for _, s := range summaries {
		var heroes []string
		for _, hero := range s.TopHeroes {
			heroes = append(heroes, fmt.Sprintf("%s ×%d", Hero(hero[0], c), hero[1]))
		}
		top := strings.Join(heroes, ", ")
		if top == "" {
			top = "нет данных"
		}
		fmt.Fprintf(&b, "<details><summary>%s · подробности</summary><p><b>Матчи:</b> %d · Победы: %d · Поражения: %d · Винрейт: %.2f%%</p><p><b>Средние K / D / A:</b> %.1f / %.1f / %.1f</p><p><b>GPM / XPM:</b> %.0f / %.0f · <b>Урон героям:</b> %.0f</p><p><b>Средняя длительность:</b> %.1f мин</p><p><b>Лучшие серии:</b> %d побед / %d поражений</p><p><b>Герои:</b> %s</p></details>", escape(s.Label), s.Matches, s.Wins, s.Losses, s.Winrate, s.AvgKills, s.AvgDeaths, s.AvgAssists, s.AvgGPM, s.AvgXPM, s.AvgHeroDamage, s.AvgDuration, s.BestStreak, s.WorstStreak, escape(top))
	}
	return b.String()
}
