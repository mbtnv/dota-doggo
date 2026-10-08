package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"dota-doggo/internal/domain"
)

type ConstantsAPI interface {
	Constants(context.Context, string) (map[string]json.RawMessage, error)
}
type ConstantsRepository interface {
	ConstantsUpdatedAt(context.Context, string) (*time.Time, error)
	SaveConstants(context.Context, []domain.ConstantEntry) error
	Constants(context.Context) (domain.Constants, error)
}
type ConstantsCache struct {
	Repo     ConstantsRepository
	API      ConstantsAPI
	Interval time.Duration
	Now      func() time.Time
}

// Sync updates resources independently: a failed heroes request must not discard
// cached lobby names or prevent other resources from being refreshed.
func (c ConstantsCache) Sync(ctx context.Context) error {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	interval := c.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	var failures []error
	for _, resource := range []string{"heroes", "game_mode", "lobby_type"} {
		if ctx.Err() != nil {
			return errors.Join(append(failures, ctx.Err())...)
		}
		at, err := c.Repo.ConstantsUpdatedAt(ctx, resource)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s cache: %w", resource, err))
			continue
		}
		if at != nil && now.Sub(*at) < interval {
			continue
		}
		payload, err := c.API.Constants(ctx, resource)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s refresh: %w", resource, err))
			continue
		}
		entries := ParseConstants(resource, payload)
		if len(entries) == 0 {
			failures = append(failures, fmt.Errorf("%s: no valid constants received", resource))
			continue
		}
		if err := c.Repo.SaveConstants(ctx, entries); err != nil {
			failures = append(failures, fmt.Errorf("%s persist: %w", resource, err))
		}
	}
	return errors.Join(failures...)
}
func ParseConstants(resource string, payload map[string]json.RawMessage) []domain.ConstantEntry {
	var entries []domain.ConstantEntry
	for code, raw := range payload {
		id, err := strconv.Atoi(code)
		if err != nil || id < 0 {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			continue
		}
		get := func(key string) string { var text string; _ = json.Unmarshal(fields[key], &text); return text }
		name := ""
		if resource == "heroes" {
			name = get("localized_name")
		}
		if name == "" {
			name = get("name")
			if name == "" {
				name = get("localized_name")
			}
			name = humanize(name)
		}
		if name == "" {
			name = "Unknown"
		}
		if resource == "game_mode" && id == 22 {
			name = "All Pick"
		}
		entries = append(entries, domain.ConstantEntry{Resource: resource, Code: id, Name: name, RawPayload: append(json.RawMessage(nil), raw...)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Code < entries[j].Code })
	return entries
}
func humanize(name string) string {
	name = strings.TrimPrefix(strings.TrimPrefix(name, "game_mode_"), "lobby_type_")
	parts := strings.Fields(strings.ReplaceAll(name, "_", " "))
	for i, s := range parts {
		runes := []rune(strings.ToLower(s))
		if len(runes) > 0 {
			runes[0] = unicode.ToUpper(runes[0])
		}
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
