package domain

import (
	"testing"
	"time"
)

func TestTopicSummariesAndLeaderboard(t *testing.T) {
	at := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	players := []Player{{ID: 1}, {ID: 2}, {ID: 3}}
	matches := []Match{{PlayerID: 1, ID: 1, StartTime: at, EndTime: at, RadiantWin: true}, {PlayerID: 2, ID: 2, StartTime: at, EndTime: at, RadiantWin: false}}
	all := TopicSummaries(players, Week, at, at.AddDate(0, 0, 7), matches, false)
	daily := TopicSummaries(players, Day, at, at.AddDate(0, 0, 1), matches, true)
	if len(all) != 3 || len(daily) != 2 {
		t.Fatalf("all %#v daily %#v", all, daily)
	}
	leaders := Leaders([]Summary{{PlayerID: 3, Winrate: 50, Wins: 1, Matches: 2}, {PlayerID: 2, Winrate: 50, Wins: 2, Matches: 4}, {PlayerID: 1, Winrate: 100, Wins: 1, Matches: 1}}, 2)
	if len(leaders) != 2 || leaders[0].PlayerID != 1 || leaders[1].PlayerID != 2 {
		t.Fatal(leaders)
	}
}
