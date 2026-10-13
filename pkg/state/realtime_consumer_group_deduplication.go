package state

import (
	"context"
	"crypto/sha256"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	dedupsqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

type RealtimeConsumerGroupDeduplicationFullError struct{ RetryAfterMilliseconds int64 }

func (e *RealtimeConsumerGroupDeduplicationFullError) Error() string {
	return "state: realtime event ID capacity reached"
}

func normalizeRealtimeGroupDeduplication(cfg *RealtimeConsumerGroupConfig) error {
	if err := validateRealtimeGroupDeadlineKey(cfg.DeduplicationMetadataKey); err != nil {
		return err
	}
	if cfg.DeduplicationMetadataKey == "" {
		if cfg.DeduplicationWindowSeconds != 0 {
			return ErrRealtimeConsumerGroupInvalid
		}
		return nil
	}
	if cfg.DeduplicationWindowSeconds == 0 {
		cfg.DeduplicationWindowSeconds = api.RealtimeConsumerGroupDeduplicationWindowDefaultSeconds
	}
	if cfg.DeduplicationWindowSeconds < api.RealtimeConsumerGroupDeduplicationWindowMinSeconds || cfg.DeduplicationWindowSeconds > api.RealtimeConsumerGroupDeduplicationWindowMaxSeconds {
		return ErrRealtimeConsumerGroupInvalid
	}
	return nil
}

// Assignment already holds the group lock. First matching admitted copy wins;
// existing snapshots (including retries, release and replay) bypass this function.
func skipRealtimeGroupDuplicate(ctx context.Context, tx pgx.Tx, g realtimeGroupRow, seq int64, metadata map[string]string) (bool, error) {
	value := metadata[g.deduplicationField]
	if g.deduplicationField == "" || value == "" {
		return false, nil
	}
	hash := sha256.Sum256([]byte(value))
	queries := dedupsqlc.New()
	if err := queries.PruneExpiredEventIDs(ctx, tx, g.id); err != nil {
		return false, err
	}
	found, err := queries.EventIDRemembered(ctx, tx, dedupsqlc.EventIDRememberedParams{GroupID: g.id, EventHash: hash[:]})
	if err != nil {
		return false, err
	}
	if found {
		return true, queries.SkipDuplicateEvent(ctx, tx, dedupsqlc.SkipDuplicateEventParams{GroupID: g.id, Sequence: seq})
	}
	capacity, err := queries.ReadEventIDCapacity(ctx, tx, g.id)
	if err != nil {
		return false, err
	}
	if capacity.KeyCount >= api.RealtimeConsumerGroupDeduplicationKeysMax {
		return false, &RealtimeConsumerGroupDeduplicationFullError{RetryAfterMilliseconds: capacity.RetryAfterMilliseconds}
	}
	return false, queries.RememberEventID(ctx, tx, dedupsqlc.RememberEventIDParams{GroupID: g.id, EventHash: hash[:], WindowSeconds: float64(g.deduplicationWindow)})
}

func loadRealtimeGroupDeduplicationStatuses(ctx context.Context, tx pgx.Tx, groups []RealtimeConsumerGroup) error {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]string, len(groups))
	indexes := make(map[string]int, len(groups))
	for i, g := range groups {
		ids[i] = g.GroupID
		indexes[g.GroupID] = i
		groups[i].DuplicateMessages = "0"
	}
	rows, err := dedupsqlc.New().ReadGroupDeduplicationStatuses(ctx, tx, ids)
	if err != nil {
		return err
	}
	for _, row := range rows {
		i, exists := indexes[row.GroupID]
		if !exists {
			continue
		}
		groups[i].DeduplicationMetadataKey = row.DeduplicationMetadataKey
		groups[i].DeduplicationWindowSeconds = int(row.DeduplicationWindowSeconds)
		groups[i].DuplicateMessages = row.DuplicateMessages
		groups[i].DeduplicationKeys = int(row.LiveKeys)
	}
	return nil
}
