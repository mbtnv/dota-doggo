package format

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"dota-doggo/internal/domain"
)

func TestFormatterReference(t *testing.T) {
	var fixture struct {
		Players []domain.Player
		Matches []domain.Match
		Summary domain.Summary
	}
	b, err := os.ReadFile("../../testdata/reference.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	c := domain.Constants{Heroes: map[int]string{74: "Invoker", 5: "Crystal Maiden"}, GameModes: map[int]string{22: "All Draft"}, LobbyTypes: map[int]string{7: "Ranked"}}
	f := Formatter{}
	group := []Participant{{fixture.Players[0], fixture.Matches[0]}, {fixture.Players[1], fixture.Matches[1]}}
	for _, tc := range []struct {
		name  string
		group []Participant
		want  []string
	}{
		{"single", group[:1], []string{"🟢 mid &amp; carry выиграл на Invoker", "5/5/10", "700 / 800", "Добито крипов · 320", "Урон героям · 23000", "Урон строениям · 5000", "Лечение · 0", "42%", "Пати из 2", "All Pick · Ranked", "10.03.2026", "18:00 MSK", "/matches/999"}},
		{"shared", group, []string{"🟢 mid &amp; carry выиграл<br>🔴 Second проиграл", "Crystal Maiden", "0/6/1", "300 / 400", "100%", "/players/987654321"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts, err := f.MatchParts(tc.group, c, "Europe/Moscow")
			if err != nil {
				t.Fatal(err)
			}
			text := decodedText(t, parts)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in %s", want, text)
				}
			}
		})
	}
	parts, err := f.ReportParts([]domain.Summary{fixture.Summary}, domain.Day, c, "Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	text := decodedText(t, parts)
	for _, want := range []string{"Дневной отчёт", "mid &amp; carry", "<td>2</td><td>1–1</td><td>50%</td>", "700 / 800", "65.0 мин", "23000", "Invoker ×2"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
}

func decodedText(t *testing.T, parts []string) string {
	t.Helper()
	var b strings.Builder
	for _, part := range parts {
		p, err := DecodeMessagePart(part)
		if err != nil || p.Format != RichHTMLFormat {
			t.Fatal(p, err)
		}
		b.WriteString(p.HTML)
	}
	return b.String()
}
func TestUntrustedNamesAndURLsAreEscaped(t *testing.T) {
	alias := "<b>Example & 'name'</b>"
	url := "javascript:alert(1)"
	f := Formatter{}
	parts, err := f.MatchParts([]Participant{{Player: domain.Player{AccountID: 123, Alias: &alias, ProfileURL: &url}, Match: domain.Match{HeroID: 999, GameMode: 1, LobbyType: 7}}}, domain.Constants{GameModes: map[int]string{1: "<bad>"}, LobbyTypes: map[int]string{7: "&lobby"}}, "Bad/Zone")
	if err != nil {
		t.Fatal(err)
	}
	text := decodedText(t, parts)
	if strings.Contains(text, "javascript:") || strings.Contains(text, "<bad>") || strings.Contains(text, "<b>Example") {
		t.Fatal(text)
	}
	for _, want := range []string{"&lt;b&gt;Example", "Hero #999", "&lt;bad&gt;", "&amp;lobby", "https://www.dotabuff.com/players/123"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}
func TestGroupingPreservesPlayerOrderAndDeduplicates(t *testing.T) {
	p := []domain.Player{{ID: 2}, {ID: 1}}
	m := []domain.Match{{PlayerID: 1, ID: 10}, {PlayerID: 2, ID: 10}, {PlayerID: 1, ID: 10}, {PlayerID: 3, ID: 11}, {PlayerID: 1, ID: 9}}
	g := GroupMatches(p, m)
	if len(g) != 2 || len(g[0]) != 2 || g[0][0].Player.ID != 2 || g[1][0].Match.ID != 9 {
		t.Fatalf("groups: %#v", g)
	}
}

func TestMode22IsAllPickWithAndWithoutConstants(t *testing.T) {
	for _, c := range []domain.Constants{{}, {GameModes: map[int]string{22: "All Draft"}}} {
		if got := Mode(22, c); got != "All Pick" {
			t.Fatal(got)
		}
	}
}
