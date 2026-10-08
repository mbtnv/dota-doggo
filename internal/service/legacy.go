package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"dota-doggo/internal/domain"
)

type LegacyPlayer struct {
	AccountID   int64
	Name        string
	LastMatchID int64
}
type LegacyTopic struct {
	ChatID   int64
	ThreadID *int64
	Title    *string
	Timezone string
}
type LegacyRepository interface {
	EnsureTopic(context.Context, int64, *int64, *string, string) (domain.Topic, error)
	LockTopic(context.Context, int64) (domain.Topic, error)
	GetOrCreatePlayer(context.Context, int64, string, *string) (int64, error)
	AddPlayer(context.Context, int64, int64, *string, *int64) (bool, error)
	AdvanceCursor(context.Context, int64, int64, int64) error
}

func ParseLegacy(r io.Reader) ([]LegacyPlayer, error) {
	const limit = 16 << 20
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("legacy JSON exceeds 16 MiB")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("legacy JSON is not UTF-8")
	}
	var rows []struct {
		ID          json.RawMessage `json:"id"`
		Name        *string         `json:"name"`
		LastMatchID json.RawMessage `json:"last_match_id"`
	}
	if err := json.Unmarshal(data, &rows); err != nil || rows == nil {
		return nil, errors.New("legacy JSON must be an array of players")
	}
	number := func(raw json.RawMessage) (int64, error) {
		text := string(raw)
		if len(raw) > 0 && raw[0] == '"' {
			if err := json.Unmarshal(raw, &text); err != nil {
				return 0, err
			}
		}
		return strconv.ParseInt(text, 10, 64)
	}
	var players []LegacyPlayer
	seen := map[int64]bool{}
	for i, row := range rows {
		id, err := number(row.ID)
		if err != nil || id < 1 || id > 4294967295 {
			return nil, fmt.Errorf("legacy row %d: invalid account ID", i+1)
		}
		last, err := number(row.LastMatchID)
		if err != nil || last < 0 {
			return nil, fmt.Errorf("legacy row %d: invalid last_match_id", i+1)
		}
		if row.Name == nil || strings.TrimSpace(*row.Name) == "" || strings.ContainsRune(*row.Name, 0) || utf8.RuneCountInString(*row.Name) > 255 {
			return nil, fmt.Errorf("legacy row %d: name must contain 1–255 characters", i+1)
		}
		if seen[id] {
			return nil, fmt.Errorf("legacy row %d: duplicate account ID", i+1)
		}
		seen[id] = true
		players = append(players, LegacyPlayer{id, *row.Name, last})
	}
	// Consistent insertion order also prevents competing imports from locking
	// the same new players in opposite orders.
	sort.Slice(players, func(i, j int) bool { return players[i].AccountID < players[j].AccountID })
	return players, nil
}

// ImportLegacy runs inside the caller's transaction: the complete file either
// commits or rolls back. Existing settings, aliases, cursors and names survive.
func ImportLegacy(ctx context.Context, repo LegacyRepository, topic LegacyTopic, players []LegacyPlayer) (int, error) {
	t, err := repo.EnsureTopic(ctx, topic.ChatID, topic.ThreadID, topic.Title, topic.Timezone)
	if err != nil {
		return 0, err
	}
	if _, err := repo.LockTopic(ctx, t.ID); err != nil {
		return 0, err
	}
	inserted := 0
	for _, p := range players {
		url := fmt.Sprintf("https://www.dotabuff.com/players/%d", p.AccountID)
		id, err := repo.GetOrCreatePlayer(ctx, p.AccountID, p.Name, &url)
		if err != nil {
			return 0, err
		}
		added, err := repo.AddPlayer(ctx, t.ID, id, nil, nil)
		if err != nil {
			return 0, err
		}
		if added {
			if err := repo.AdvanceCursor(ctx, t.ID, id, p.LastMatchID); err != nil {
				return 0, err
			}
			inserted++
		}
	}
	return inserted, nil
}
