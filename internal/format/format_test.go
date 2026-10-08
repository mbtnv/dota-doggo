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
		Players                     []domain.Player
		Matches                     []domain.Match
		Summary                     domain.Summary
		Notification, Group, Report string
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
	for _, tc := range []struct{ name, got, want string }{{"single", f.Notification(group[:1], c, "Europe/Moscow"), fixture.Notification}, {"shared", f.Notification(group, c, "Europe/Moscow"), fixture.Group}, {"report", f.Report(fixture.Summary, c), fixture.Report}} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", tc.got, tc.want)
			}
		})
	}
}
func TestUntrustedNamesAndURLsAreEscaped(t *testing.T) {
	alias := "<b>Example & 'name'</b>"
	url := "javascript:alert(1)"
	f := Formatter{}
	text := f.Notification([]Participant{{Player: domain.Player{AccountID: 123, Alias: &alias, ProfileURL: &url}, Match: domain.Match{HeroID: 999, GameMode: 1, LobbyType: 7}}}, domain.Constants{GameModes: map[int]string{1: "<bad>"}, LobbyTypes: map[int]string{7: "&lobby"}}, "Bad/Zone")
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
