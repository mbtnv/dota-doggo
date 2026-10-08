package service

import (
	"strings"
	"testing"
)

func TestLegacyParserKeepsIntegerPrecisionAndSortsPlayers(t *testing.T) {
	players, err := ParseLegacy(strings.NewReader(`[{"id":"456","name":"Second","last_match_id":"9007199254740993"},{"id":123,"name":"First","last_match_id":0}]`))
	if err != nil || len(players) != 2 || players[0].AccountID != 123 || players[1].LastMatchID != 9007199254740993 {
		t.Fatal(players, err)
	}
}
func TestLegacyParserRejectsInvalidCompleteFile(t *testing.T) {
	for _, text := range []string{`null`, `{}`, `[] {}`, `[{"id":1,"name":"Name"}]`, `[{"id":1,"name":"Name","last_match_id":-1}]`, `[{"id":4294967296,"name":"Name","last_match_id":1}]`, `[{"id":1.5,"name":"Name","last_match_id":1}]`, `[{"id":1,"name":" ","last_match_id":1}]`, `[{"id":1,"name":"Name","last_match_id":1},{"id":"1","name":"Duplicate","last_match_id":2}]`, `[{"id":1,"name":"` + strings.Repeat("a", 256) + `","last_match_id":1}]`} {
		if players, err := ParseLegacy(strings.NewReader(text)); err == nil || players != nil {
			t.Fatal(text, players, err)
		}
	}
}
