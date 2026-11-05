package format

import (
	"encoding/json"
	"errors"
	"strings"
)

// MessagePart is the persisted representation of a rich message part. Legacy
// deliveries remain plain HTML strings; keeping the format in each snapshot
// lets a restarted worker resume them without reformatting or a schema change.
type MessagePart struct {
	Format         string `json:"format"`
	HTML           string `json:"html"`
	DetectEntities bool   `json:"detect_entities,omitempty"`
}

const RichHTMLFormat = "telegram-rich-html-v1"

// Telegram counts rich text in characters; the UTF-16 counter used by SplitHTML
// is conservative for emoji and is shared with regular-message validation.
const TelegramRichTextLimit = 32768

func DecodeMessagePart(part string) (MessagePart, error) {
	if !strings.HasPrefix(part, "{") {
		return MessagePart{HTML: part}, nil
	}
	var p MessagePart
	if err := json.Unmarshal([]byte(part), &p); err != nil {
		return p, errors.New("invalid message snapshot")
	}
	if p.Format != RichHTMLFormat || p.HTML == "" {
		return p, errors.New("unsupported message snapshot format")
	}
	return p, nil
}

// EncodeRichPart keeps the original discriminator so daily snapshots created
// before rich formatting was extended to other messages remain readable.
func EncodeRichPart(html string) (string, error) {
	return encodeRichPart(html, false)
}

func encodeRichPart(html string, detectEntities bool) (string, error) {
	chunks, err := SplitHTML(html, TelegramRichTextLimit)
	if err != nil {
		return "", err
	}
	if len(chunks) != 1 {
		return "", errors.New("empty or oversized rich message")
	}
	p, err := json.Marshal(MessagePart{Format: RichHTMLFormat, HTML: html, DetectEntities: detectEntities})
	return string(p), err
}

// TextHTML promotes balanced inline HTML to paragraphs, preserving line breaks
// even inside names or links. Each paragraph closes and reopens inline tags.
func TextHTML(text string) (string, error) {
	if _, err := SplitHTML(text, max(1, len(text))); err != nil {
		return "", err
	}
	tokens, err := tokenize(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var stack []token
	b.WriteString("<p>")
	for _, t := range tokens {
		if t.name != "" {
			if t.closing {
				stack = stack[:len(stack)-1]
			} else {
				stack = append(stack, t)
			}
		} else if t.raw == "\n" {
			for i := len(stack) - 1; i >= 0; i-- {
				b.WriteString("</" + stack[i].name + ">")
			}
			b.WriteString("</p><p>")
			for _, opening := range stack {
				b.WriteString(opening.raw)
			}
			continue
		}
		b.WriteString(t.raw)
	}
	b.WriteString("</p>")
	return b.String(), nil
}

func TextParts(title, text string) ([]string, error) {
	header := "<h2>" + escape(title) + "</h2>"
	n, err := HTMLLength(header)
	if err != nil {
		return nil, err
	}
	chunks, err := splitHTML(text, max(1, TelegramTextLimit-n), 100)
	if err != nil {
		return nil, err
	}
	var parts []string
	for _, chunk := range chunks {
		body, err := TextHTML(chunk)
		if err != nil {
			return nil, err
		}
		// Command help and usage hints retain tappable /commands. Other rich
		// cards keep auto-detection disabled for player-supplied labels.
		part, err := encodeRichPart(header+body, true)
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

type richEntry struct{ row, details string }

// richTableParts paginates by complete entries, never by characters inside a
// table or a disclosure block. Headers and context are repeated on each page.
func richTableParts(header, tableHead string, entries []richEntry, footer string) ([]string, error) {
	if len(entries) == 0 {
		return []string{}, nil
	}
	render := func(batch []richEntry) string {
		var b strings.Builder
		b.WriteString(header)
		b.WriteString("<table striped compact><tr>" + tableHead + "</tr>")
		for _, entry := range batch {
			b.WriteString("<tr>" + entry.row + "</tr>")
		}
		b.WriteString("</table>")
		for _, entry := range batch {
			b.WriteString(entry.details)
		}
		b.WriteString(footer)
		return b.String()
	}
	pageSize := 20
	for _, entry := range entries {
		if entry.details != "" {
			pageSize = 8
			break
		}
	}
	return paginateRich(entries, pageSize, render)
}

// paginateRich keeps each entry whole. A single large entry may exceed the
// readability target, but EncodeRichPart still enforces the rich message limit.
func paginateRich[T any](entries []T, pageSize int, render func([]T) string) ([]string, error) {
	parts := []string{}
	for len(entries) > 0 {
		end := 1
		for end < min(pageSize, len(entries)) {
			length, err := HTMLLength(render(entries[:end+1]))
			if err != nil {
				return nil, err
			}
			if length > TelegramTextLimit {
				break
			}
			end++
		}
		part, err := EncodeRichPart(render(entries[:end]))
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
		entries = entries[end:]
	}
	return parts, nil
}
