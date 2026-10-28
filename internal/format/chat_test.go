package format

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"dota-doggo/internal/domain"
)

func TestTextCardsPreserveMultilineNamesFormattingAndCommandDetection(t *testing.T) {
	parts, err := TextParts("🕒 Настройки <&>", "Игрок: <b>First\nSecond &amp; 🐕</b>\nИспользование: /track 123")
	if err != nil || len(parts) != 1 {
		t.Fatal(parts, err)
	}
	p, err := DecodeMessagePart(parts[0])
	if err != nil || !p.DetectEntities {
		t.Fatal(p, err)
	}
	for _, want := range []string{"<h2>🕒 Настройки &lt;&amp;&gt;</h2>", "<p>Игрок: <b>First</b></p><p><b>Second &amp; 🐕</b></p>", "<p>Использование: /track 123</p>"} {
		if !strings.Contains(p.HTML, want) {
			t.Errorf("missing %q: %s", want, p.HTML)
		}
	}
	if _, err := TextParts("broken", "<b>unclosed"); err == nil {
		t.Fatal("invalid text accepted")
	}
}

func TestTextCardsBoundParagraphCountAndPreserveLongUnicode(t *testing.T) {
	text := "<b>" + strings.Repeat("😀&amp;\n", 1200) + "</b>"
	parts, err := TextParts("Диагностика", text)
	if err != nil || len(parts) < 2 {
		t.Fatal(len(parts), err)
	}
	var joined strings.Builder
	for _, part := range parts {
		p, err := DecodeMessagePart(part)
		if err != nil {
			t.Fatal(err)
		}
		chunks, err := SplitHTML(p.HTML, TelegramTextLimit)
		if err != nil || len(chunks) != 1 || strings.Count(p.HTML, "<p>") > 102 {
			t.Fatal("oversized or unbalanced card", err)
		}
		joined.WriteString(p.HTML)
	}
	if strings.Count(joined.String(), "😀&amp;") != 1200 {
		t.Fatal("lost Unicode text")
	}
}

func TestPlayerCardsPaginateAndProtectNamesAndProfileLinks(t *testing.T) {
	var players []domain.Player
	bad := "javascript:alert(1)"
	for i := range 40 {
		players = append(players, domain.Player{ID: int64(i + 1), AccountID: int64(i + 100), DisplayName: fmt.Sprintf("Player <%02d> & 🐕", i), ProfileURL: &bad})
	}
	parts, err := PlayerParts(players)
	if err != nil || len(parts) < 2 {
		t.Fatal(parts, err)
	}
	text := decodedText(t, parts)
	if strings.Contains(text, "javascript:") || strings.Contains(text, "Player <") {
		t.Fatal("untrusted profile or label", text)
	}
	for _, p := range players {
		if strings.Count(text, fmt.Sprintf("/players/%d", p.AccountID)) != 1 || !strings.Contains(text, escape(p.Label())) {
			t.Fatal("player missing or duplicated", p)
		}
	}
}

func TestEveryReportPeriodAndLeaderboardUseRichTables(t *testing.T) {
	f := Formatter{}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, period := range []domain.Period{domain.Day, domain.Week, domain.Month} {
		start, end, err := period.Bounds(now, "Europe/Moscow", true)
		if err != nil {
			t.Fatal(err)
		}
		s := domain.Summary{Label: "Winner <&>", Period: period, Start: start, End: end, Matches: 4, Wins: 3, Losses: 1, Winrate: 75}
		parts, err := f.ReportParts([]domain.Summary{s}, period, domain.Constants{}, "Europe/Moscow")
		if err != nil {
			t.Fatal(err)
		}
		text := decodedText(t, parts)
		if !strings.Contains(text, "<td>4</td><td>3–1</td><td>75%</td>") || !strings.Contains(text, "Winner &lt;&amp;&gt;") || !strings.Contains(text, "<details>") {
			t.Fatal(text)
		}
		leaders, err := LeaderParts([]domain.Summary{s}, period, "Europe/Moscow")
		if err != nil {
			t.Fatal(err)
		}
		text = decodedText(t, leaders)
		if !strings.Contains(text, "🥇 Winner &lt;&amp;&gt;") || !strings.Contains(text, "75.00%") {
			t.Fatal(text)
		}
	}
}

func TestRichSnapshotsKeepOldDailyFormatAndEnforceLimit(t *testing.T) {
	old := `{"format":"telegram-rich-html-v1","html":"<h2>Old daily report</h2>"}`
	p, err := DecodeMessagePart(old)
	if err != nil || p.DetectEntities || p.HTML != "<h2>Old daily report</h2>" {
		t.Fatal(p, err)
	}
	if _, err := EncodeRichPart("<p>" + strings.Repeat("x", TelegramRichTextLimit+1) + "</p>"); err == nil {
		t.Fatal("oversized rich text accepted")
	}
}
