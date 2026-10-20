package format

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
)

func TestDailyReportPreservesMetricsAndEscapesPlayerContent(t *testing.T) {
	start := time.Date(2026, 10, 19, 21, 0, 0, 0, time.UTC)
	s := domain.Summary{Label: `Carry <script> & "dog" 🐕`, Start: start, End: start.Add(24 * time.Hour), Matches: 3, Wins: 2, Losses: 1, Winrate: 200.0 / 3, AvgKills: 10.5, AvgDeaths: 2, AvgAssists: 7.5, AvgGPM: 600, AvgXPM: 700, AvgDuration: 35.5, AvgHeroDamage: 15000, BestStreak: 2, WorstStreak: 1, TopHeroes: [][2]int{{74, 2}, {999, 1}}}
	parts, err := (Formatter{}).DailyReportParts([]domain.Summary{s}, domain.Constants{Heroes: map[int]string{74: "Invoker <&>"}}, "Europe/Moscow")
	if err != nil || len(parts) != 1 {
		t.Fatal(parts, err)
	}
	p, err := DecodeReportPart(parts[0])
	if err != nil || p.Format != RichHTMLFormat {
		t.Fatal(p, err)
	}
	for _, want := range []string{"Дневной отчёт", "20.10.2026", "Europe/Moscow", "<table striped compact>", "<th>WR</th>", "<td>3</td><td>2–1</td><td>67%</td>", "<details><summary>", "Carry &lt;script&gt; &amp; &#34;dog&#34; 🐕", "10.5 / 2.0 / 7.5", "600 / 700", "15000", "35.5 мин", "2 побед / 1 поражений", "Invoker &lt;&amp;&gt; ×2, Hero #999 ×1"} {
		if !strings.Contains(p.HTML, want) {
			t.Errorf("missing %q in %s", want, p.HTML)
		}
	}
	if strings.Contains(p.HTML, "<script>") {
		t.Fatal("unescaped player label")
	}
}

func TestDailyReportSplitsWithoutBreakingTablesOrLosingPlayers(t *testing.T) {
	var summaries []domain.Summary
	for i := range 22 {
		start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
		summaries = append(summaries, domain.Summary{Label: fmt.Sprintf("player-%02d-%s", i, strings.Repeat("😀<&", 60)), Start: start, End: start.Add(24 * time.Hour), Matches: 1, Wins: 1, Winrate: 100})
	}
	parts, err := (Formatter{}).DailyReportParts(summaries, domain.Constants{}, "UTC")
	if err != nil || len(parts) < 2 {
		t.Fatal(len(parts), err)
	}
	var all strings.Builder
	for _, part := range parts {
		p, err := DecodeReportPart(part)
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := SplitHTML(p.HTML, TelegramTextLimit)
		if err != nil || len(chunks) != 1 || strings.Count(p.HTML, "<table ") != 1 || strings.Count(p.HTML, "</table>") != 1 || strings.Count(p.HTML, "<details>") != strings.Count(p.HTML, "</details>") {
			t.Fatal("broken rich report", p.HTML, err)
		}
		all.WriteString(p.HTML)
	}
	for _, s := range summaries {
		if strings.Count(all.String(), "<summary>"+escape(s.Label)+" · подробности</summary>") != 1 {
			t.Fatal("missing or duplicated player", s.Label)
		}
	}
}

func TestReportSnapshotCompatibilityAndEmptyDay(t *testing.T) {
	legacy := "<b>day report</b>\nold pending delivery"
	p, err := DecodeReportPart(legacy)
	if err != nil || p.Format != "" || p.HTML != legacy {
		t.Fatal(p, err)
	}
	for _, invalid := range []string{`{broken`, `{"format":"future-version","html":"<h2>Report</h2>"}`, `{"format":"telegram-rich-html-v1"}`} {
		if _, err := DecodeReportPart(invalid); err == nil {
			t.Fatal("invalid snapshot accepted", invalid)
		}
	}
	parts, err := (Formatter{}).DailyReportParts(nil, domain.Constants{}, "UTC")
	if err != nil || len(parts) != 0 {
		t.Fatal(parts, err)
	}
}
