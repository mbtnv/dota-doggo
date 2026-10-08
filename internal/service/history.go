package service

import (
	"context"
	"errors"
	"fmt"

	"dota-doggo/internal/domain"
	"dota-doggo/internal/opendota"
)

type HistoryAPI interface {
	History(context.Context, int64, int, int, int) ([]opendota.MatchData, error)
	MatchPlayers(context.Context, int64) ([]opendota.MatchData, error)
}
type HistoryRepository interface {
	SaveMatches(context.Context, []domain.Match) (int, error)
	AdvanceCursor(context.Context, int64, int64, int64) error
}
type HistoryResult struct{ Fetched, Inserted, Failed int }
type History struct {
	API  HistoryAPI
	Repo HistoryRepository
	// Commit saves a batch and advances its cursor atomically in PostgreSQL.
	Commit func(context.Context, int64, int64, []domain.Match, int64) (int, error)
}

// Sync keeps successful matches when individual detail requests fail. A failed
// history page returns an error with the accumulated counts; a repeated page
// stops with an error rather than looping indefinitely on a broken API.
func (h History) Sync(ctx context.Context, topicID int64, player domain.Player, days int, progress func(HistoryResult)) (HistoryResult, error) {
	var result HistoryResult
	if days < 1 || days > 365 {
		return result, errors.New("invalid history days")
	}
	seen := map[int64]bool{}
	var cursor int64
	const pageSize = 100
	for offset := 0; ; offset += pageSize {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		page, err := h.API.History(ctx, player.AccountID, days, pageSize, offset)
		if err != nil {
			return result, fmt.Errorf("history page %d: %w", offset/pageSize+1, err)
		}
		fresh := 0
		for _, brief := range page {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if brief.MatchID == nil || *brief.MatchID <= 0 {
				result.Failed++
				continue
			}
			id := *brief.MatchID
			if seen[id] {
				continue
			}
			seen[id] = true
			fresh++
			result.Fetched++
			// Always attempt full details so an existing sparse row can be enriched.
			players, detailErr := h.API.MatchPlayers(ctx, id)
			data, found := opendota.MatchData{}, false
			if detailErr == nil && brief.PlayerSlot != nil {
				data, found = opendota.SelectMatchPlayer(players, player.AccountID, *brief.PlayerSlot)
			}
			if detailErr == nil && brief.PlayerSlot == nil {
				data, found = opendota.SelectMatchPlayer(players, player.AccountID, -1)
			}
			if !found {
				data = brief
			}
			match, snapshotErr := data.Snapshot(player.ID)
			if snapshotErr != nil || match.ID != id {
				result.Failed++
				continue
			}
			if detailErr != nil || !found {
				result.Failed++
			} // usable brief saved, enrichment still failed
			cursor = max(cursor, id)
			var inserted int
			if h.Commit != nil {
				inserted, err = h.Commit(ctx, topicID, player.ID, []domain.Match{match}, cursor)
			} else {
				inserted, err = h.Repo.SaveMatches(ctx, []domain.Match{match})
				if err == nil {
					err = h.Repo.AdvanceCursor(ctx, topicID, player.ID, cursor)
				}
			}
			if err != nil {
				return result, err
			}
			result.Inserted += inserted
		}
		if progress != nil {
			progress(result)
		}
		if len(page) < pageSize {
			return result, nil
		}
		if fresh == 0 {
			return result, errors.New("OpenDota history repeated a page")
		}
	}
}
