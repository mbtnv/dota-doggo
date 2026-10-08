package format

import (
	"errors"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

const TelegramTextLimit = 4096

var tagName = regexp.MustCompile(`^</?([a-zA-Z][a-zA-Z0-9-]*)(?:\s[^<>]*)?>$`)

type token struct {
	raw, name string
	units     int
	closing   bool
}

// tokenize preserves entities as indivisible tokens and counts decoded UTF-16
// units conservatively, so emoji cannot overflow Telegram's text limit.
func tokenize(text string) ([]token, error) {
	var out []token
	for len(text) > 0 {
		if text[0] == '<' {
			end := -1
			var quote byte
			for i := 1; i < len(text); i++ {
				c := text[i]
				if quote != 0 {
					if c == quote {
						quote = 0
					}
					continue
				}
				if c == '\'' || c == '"' {
					quote = c
					continue
				}
				if c == '>' {
					end = i
					break
				}
			}
			if end < 0 {
				return nil, errors.New("unterminated HTML tag")
			}
			raw := text[:end+1]
			names := tagName.FindStringSubmatch(raw)
			if len(names) == 0 {
				return nil, errors.New("invalid HTML tag")
			}
			out = append(out, token{raw: raw, name: strings.ToLower(names[1]), closing: strings.HasPrefix(raw, "</")})
			text = text[end+1:]
			continue
		}
		if text[0] == '&' {
			end := strings.IndexByte(text, ';')
			if end < 0 || end > 32 {
				return nil, errors.New("invalid HTML entity")
			}
			raw := text[:end+1]
			decoded := html.UnescapeString(raw)
			if decoded == raw {
				return nil, errors.New("unknown HTML entity")
			}
			units := 0
			for _, r := range decoded {
				units++
				if r > 0xffff {
					units++
				}
			}
			out = append(out, token{raw: raw, units: units})
			text = text[end+1:]
			continue
		}
		r, size := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && size == 1 {
			return nil, errors.New("invalid UTF-8")
		}
		units := 1
		if r > 0xffff {
			units = 2
		}
		out = append(out, token{raw: text[:size], units: units})
		text = text[size:]
	}
	return out, nil
}

func HTMLLength(text string) (int, error) {
	tokens, err := tokenize(text)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range tokens {
		n += t.units
	}
	return n, nil
}

func SplitHTML(text string, limit int) ([]string, error) {
	if limit < 1 {
		return nil, errors.New("text limit must be positive")
	}
	tokens, err := tokenize(text)
	if err != nil {
		return nil, err
	}
	var chunks []string
	var stack []token
	var b strings.Builder
	length := 0
	flush := func() {
		if length == 0 {
			return
		}
		for i := len(stack) - 1; i >= 0; i-- {
			b.WriteString("</" + stack[i].name + ">")
		}
		chunks = append(chunks, b.String())
		b.Reset()
		length = 0
		for _, t := range stack {
			b.WriteString(t.raw)
		}
	}
	for _, t := range tokens {
		if t.name != "" {
			if t.closing {
				if len(stack) == 0 || stack[len(stack)-1].name != t.name {
					return nil, errors.New("unbalanced HTML")
				}
				stack = stack[:len(stack)-1]
			} else {
				stack = append(stack, t)
			}
			b.WriteString(t.raw)
			continue
		}
		if t.units > limit {
			return nil, errors.New("text limit cannot fit a Unicode character")
		}
		if length+t.units > limit {
			flush()
		}
		b.WriteString(t.raw)
		length += t.units
	}
	if len(stack) > 0 {
		return nil, errors.New("unclosed HTML tag")
	}
	flush()
	return chunks, nil
}

// SplitSections repeats the header and keeps complete games/reports together
// whenever possible. Oversized headers/sections are split without dropping data.
func SplitSections(header string, sections []string, limit int) ([]string, error) {
	if limit < 1 {
		return nil, errors.New("text limit must be positive")
	}
	h, err := HTMLLength(header)
	if err != nil {
		return nil, err
	}
	if _, err := SplitHTML(header, limit); err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		return SplitHTML(header, limit)
	}
	if h+2 >= limit {
		return SplitHTML(header+"\n\n"+strings.Join(sections, "\n\n"), limit)
	}
	var out []string
	current := header
	length := h
	flush := func() {
		if length > h {
			out = append(out, current)
		}
		current = header
		length = h
	}
	for _, section := range sections {
		n, err := HTMLLength(section)
		if err != nil {
			return nil, err
		}
		if _, err := SplitHTML(section, limit); err != nil {
			return nil, err
		}
		if length+2+n <= limit {
			current += "\n\n" + section
			length += 2 + n
			continue
		}
		flush()
		if h+2+n <= limit {
			current += "\n\n" + section
			length += 2 + n
			continue
		}
		parts, err := SplitHTML(section, limit-h-2)
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			out = append(out, header+"\n\n"+part)
		}
	}
	flush()
	return out, nil
}
