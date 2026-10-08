// Package bot handles Telegram commands independently of the polling transport.
package bot

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"dota-doggo/internal/domain"
)

func Command(text, username string) (command, arguments string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, tail, _ := strings.Cut(text, " ")
	// Telegram also permits whitespace other than a space between arguments.
	if i := strings.IndexAny(head, "\t\n\r"); i >= 0 {
		tail = head[i+1:] + " " + tail
		head = head[:i]
	}
	name, mention, hasMention := strings.Cut(head[1:], "@")
	if hasMention && !strings.EqualFold(mention, username) {
		return "", "", false
	}
	return strings.ToLower(name), strings.TrimSpace(tail), name != ""
}
func first(text string) (string, string) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	return fields[0], strings.TrimSpace(text[len(fields[0]):])
}
func AccountID(text string) (int64, error) {
	if strings.Contains(text, "://") {
		u, err := url.Parse(text)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return 0, errors.New("invalid player URL")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 || parts[len(parts)-2] != "players" {
			return 0, errors.New("expected /players/<account_id>")
		}
		text = parts[len(parts)-1]
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id <= 0 || id > 4294967295 {
		return 0, errors.New("expected a positive 32-bit account ID")
	}
	return id, nil
}
func choose(players []domain.Player, filter string) ([]domain.Player, string) {
	selected := domain.SelectPlayers(players, filter)
	if filter == "" {
		return selected, ""
	}
	if len(selected) == 0 {
		return nil, "Игрок не найден в этом topic."
	}
	if len(selected) > 1 {
		return nil, "Alias неоднозначен. Укажите account ID игрока."
	}
	return selected, ""
}
func ids(players []domain.Player) []int64 {
	out := make([]int64, len(players))
	for i, p := range players {
		out[i] = p.ID
	}
	return out
}
