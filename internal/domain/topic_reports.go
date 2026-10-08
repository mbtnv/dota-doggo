package domain

import (
	"sort"
	"time"
)

func TopicSummaries(players []Player, period Period, start, end time.Time, matches []Match, excludeEmpty bool) []Summary {
	byPlayer := make(map[int64][]Match)
	for _, m := range matches {
		byPlayer[m.PlayerID] = append(byPlayer[m.PlayerID], m)
	}
	var out []Summary
	for _, p := range players {
		s := Summarize(p, period, start, end, byPlayer[p.ID])
		if !excludeEmpty || s.Matches > 0 {
			out = append(out, s)
		}
	}
	return out
}

func Leaders(summaries []Summary, limit int) []Summary {
	if limit < 1 {
		return nil
	}
	out := append([]Summary(nil), summaries...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Winrate != b.Winrate {
			return a.Winrate > b.Winrate
		}
		if a.Wins != b.Wins {
			return a.Wins > b.Wins
		}
		if a.Matches != b.Matches {
			return a.Matches > b.Matches
		}
		return a.PlayerID < b.PlayerID
	})
	return out[:min(limit, len(out))]
}
