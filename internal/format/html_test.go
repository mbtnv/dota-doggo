package format

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

var tags = regexp.MustCompile(`<[^>]+>`)

func plain(text string) string { return html.UnescapeString(tags.ReplaceAllString(text, "")) }
func TestSplitHTMLPreservesUnicodeEntitiesAndTags(t *testing.T) {
	text := "<b>Заголовок 🐶</b>\n<a href=\"https://example.com/?x=1&amp;y=2\"><i>" + strings.Repeat("🐶&amp;&#x1F600; Кириллица\n", 10) + "</i></a>"
	chunks, err := SplitHTML(text, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatal("expected split")
	}
	var joined strings.Builder
	for _, chunk := range chunks {
		n, err := HTMLLength(chunk)
		if err != nil || n > 40 {
			t.Fatalf("oversized: %d %v %s", n, err, chunk)
		}
		if _, err := SplitHTML(chunk, 40); err != nil {
			t.Fatalf("unbalanced chunk: %v", err)
		}
		joined.WriteString(plain(chunk))
	}
	if joined.String() != plain(text) {
		t.Fatal("content lost or duplicated")
	}
}
func TestSplitSectionsKeepsGamesWhole(t *testing.T) {
	header := "<b>Last matches</b>"
	sections := []string{"<b>Alpha</b>\n" + strings.Repeat("A", 18), "<b>Beta</b>\n" + strings.Repeat("B", 18), "<b>Gamma</b>\n" + strings.Repeat("C", 18)}
	chunks, err := SplitSections(header, sections, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks: %#v", chunks)
	}
	for i, s := range sections {
		if !strings.Contains(chunks[i], s) || !strings.HasPrefix(chunks[i], header+"\n\n") {
			t.Fatal(chunks)
		}
	}
}
func TestLongHeaderDoesNotDropSections(t *testing.T) {
	header := "<b>" + strings.Repeat("H", 80) + "</b>"
	section := "<b>END</b>"
	chunks, err := SplitSections(header, []string{section}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if plain(strings.Join(chunks, "")) != plain(header+"\n\n"+section) {
		t.Fatal("section lost")
	}
}
func TestRejectsMalformedHTMLAndImpossibleLimits(t *testing.T) {
	for _, tc := range []struct {
		text  string
		limit int
	}{{"x", 0}, {"<b>x", 10}, {"</b>x", 10}, {"<b><i>x</b></i>", 10}, {"&bad;", 10}, {"🐶", 1}, {string([]byte{0xff}), 10}} {
		if _, err := SplitHTML(tc.text, tc.limit); err == nil {
			t.Fatalf("accepted %#v", tc)
		}
	}
}
func FuzzSplitHTML(f *testing.F) {
	f.Add("🐶 & привет", 32)
	f.Fuzz(func(t *testing.T, text string, limit int) {
		if limit < 2 || limit > 1000 {
			return
		}
		escaped := "<b>" + html.EscapeString(text) + "</b>"
		chunks, err := SplitHTML(escaped, limit)
		if err != nil {
			return
		}
		var combined string
		for _, chunk := range chunks {
			n, err := HTMLLength(chunk)
			if err != nil || n > limit {
				t.Fatalf("invalid chunk: %d %v", n, err)
			}
			combined += plain(chunk)
		}
		if combined != text {
			t.Fatal("text changed")
		}
	})
}
