package domain

import "sort"

// NewMatches deduplicates and orders by match ID, the cursor's ordering key.
// A nil cursor means initialization; callers save these without notifying.
func NewMatches(matches []Match, cursor *int64) []Match {
	seen := map[int64]bool{}
	var out []Match
	for _, m := range matches {
		if m.ID <= 0 || seen[m.ID] || (cursor != nil && m.ID <= *cursor) {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
