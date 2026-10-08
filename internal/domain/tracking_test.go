package domain

import "testing"

func TestNewMatchesFiltersCursorDeduplicatesAndOrders(t *testing.T) {
	matches := []Match{{ID: 103}, {ID: 100}, {ID: 102}, {ID: 101}, {ID: 102}, {ID: 0}}
	cursor := int64(100)
	got := NewMatches(matches, &cursor)
	if len(got) != 3 || got[0].ID != 101 || got[1].ID != 102 || got[2].ID != 103 || matches[0].ID != 103 {
		t.Fatal(got, matches)
	}
	if all := NewMatches(matches, nil); len(all) != 4 || all[0].ID != 100 {
		t.Fatal(all)
	}
}
