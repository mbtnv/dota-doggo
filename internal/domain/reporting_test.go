package domain

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string, into any) {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarPeriodsAgainstReference(t *testing.T) {
	var cases []struct {
		Timezone   string
		Now        time.Time
		Period     Period
		Previous   bool
		Start, End time.Time
	}
	readFixture(t, "periods.json", &cases)
	for _, tc := range cases {
		t.Run(tc.Timezone+"/"+string(tc.Period)+"/"+tc.Now.Format("2006-01-02")+"/"+tc.Start.Format(time.RFC3339), func(t *testing.T) {
			start, end, err := tc.Period.Bounds(tc.Now, tc.Timezone, tc.Previous)
			if err != nil || !start.Equal(tc.Start) || !end.Equal(tc.End) {
				t.Fatalf("got %v..%v (%v), want %v..%v", start, end, err, tc.Start, tc.End)
			}
		})
	}
}
func TestSummaryAgainstReference(t *testing.T) {
	var f struct {
		Players        []Player
		SummaryMatches []Match `json:"summary_matches"`
		Summary        Summary
	}
	readFixture(t, "reference.json", &f)
	got := Summarize(f.Players[0], f.Summary.Period, f.Summary.Start, f.Summary.End, f.SummaryMatches)
	if !reflect.DeepEqual(got, f.Summary) {
		t.Fatalf("got %#v\nwant %#v", got, f.Summary)
	}
}
func TestStatisticsAgainstReference(t *testing.T) {
	var f struct {
		Statistics []struct{ Kills, Deaths, Assists, Percent int }
	}
	readFixture(t, "reference.json", &f)
	for _, tc := range f.Statistics {
		got := CalculateStatistics(Match{Kills: tc.Kills, Deaths: tc.Deaths, Assists: tc.Assists})
		if len(got) != 1 || got[0].RawValue != float64(tc.Percent) {
			t.Fatalf("case %#v: %#v", tc, got)
		}
	}
}
func TestSummaryFiltersSortsAndDoesNotMutateInput(t *testing.T) {
	at := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	matches := []Match{
		{PlayerID: 1, ID: 3, StartTime: at, EndTime: at.Add(time.Minute), HeroID: 74, RadiantWin: true},
		{PlayerID: 1, ID: 2, StartTime: at, EndTime: at, HeroID: 5, RadiantWin: false},
		{PlayerID: 1, ID: 1, StartTime: at, EndTime: at, HeroID: 74, RadiantWin: true},
		{PlayerID: 2, ID: 4, EndTime: at, RadiantWin: true},
		{PlayerID: 1, ID: 5, EndTime: at.Add(time.Hour), RadiantWin: true},
	}
	before := append([]Match(nil), matches...)
	s := Summarize(Player{ID: 1, DisplayName: "Example"}, Day, at, at.Add(time.Hour), matches)
	if s.Matches != 3 || s.Wins != 2 || s.BestStreak != 1 || s.WorstStreak != 1 || s.TopHeroes[0] != [2]int{74, 2} {
		t.Fatalf("incorrect summary: %#v", s)
	}
	if !reflect.DeepEqual(before, matches) {
		t.Fatal("mutated input")
	}
	empty := Summarize(Player{ID: 1}, Day, at, at, nil)
	if empty.Matches != 0 || empty.Winrate != 0 || len(empty.TopHeroes) != 0 {
		t.Fatal(empty)
	}
}
func TestInvalidPeriodAndTimezone(t *testing.T) {
	for _, tc := range []struct {
		period Period
		zone   string
	}{{"year", "UTC"}, {Day, "Bad/Zone"}} {
		if _, _, err := tc.period.Bounds(time.Now(), tc.zone, false); err == nil {
			t.Fatalf("accepted %#v", tc)
		}
	}
}
func TestSelectPlayersPreservesAmbiguityForCaller(t *testing.T) {
	alias := "mid"
	players := []Player{{ID: 1, AccountID: 123, Alias: &alias}, {ID: 2, AccountID: 456, Alias: &alias}}
	if len(SelectPlayers(players, "mid")) != 2 || len(SelectPlayers(players, "123")) != 1 || len(SelectPlayers(players, "missing")) != 0 || len(SelectPlayers(players, "")) != 2 {
		t.Fatal("incorrect filtering")
	}
}

type customCalculator struct{}

func (customCalculator) Calculate(m Match) (Statistic, bool) {
	return Statistic{Key: "hits", RawValue: float64(m.LastHits)}, true
}
func TestAccountIDTakesPrecedenceOverNumericAlias(t *testing.T) {
	alias := "123"
	players := []Player{{ID: 1, AccountID: 123}, {ID: 2, AccountID: 456, Alias: &alias}}
	selected := SelectPlayers(players, "123")
	if len(selected) != 1 || selected[0].ID != 1 {
		t.Fatal(selected)
	}
}

func TestCustomCalculator(t *testing.T) {
	s := CalculateStatistics(Match{LastHits: 320}, customCalculator{})
	if len(s) != 1 || s[0].Key != "hits" || s[0].RawValue != 320 {
		t.Fatal(s)
	}
}
