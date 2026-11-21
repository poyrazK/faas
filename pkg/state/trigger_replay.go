package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Stored replay uses the same trigger claim fence and outcome transitions as
// broker delivery. The marker survives restart and every retry until terminal.
type ExternalTriggerReplayStore interface {
	ListDueExternalTriggerReplays(context.Context, string, int) ([]sqlc.TriggerRecord, error)
}

func validateExternalTriggerReplay(record sqlc.TriggerRecord) error {
	if record.State == "claimed" {
		return ErrConflict
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(record.Metadata, &metadata) == nil && metadata["payload_omitted_bytes"] != nil {
		return ErrDeadLetterNotReplayable
	}
	return nil
}

func (s *PgStore) ListDueExternalTriggerReplays(ctx context.Context, trigger string, limit int) ([]sqlc.TriggerRecord, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.triggerQueries().ListDueExternalTriggerReplays(ctx, s.pool, sqlc.ListDueExternalTriggerReplaysParams{TriggerID: mustPgUUID(trigger), BatchLimit: int32(limit)})
}

func (m *MemStore) ListDueExternalTriggerReplays(ctx context.Context, trigger string, limit int) ([]sqlc.TriggerRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.triggers[trigger]
	if limit <= 0 || t.Kind != "kafka" && t.Kind != "nats" && t.Kind != "redis_streams" {
		return nil, nil
	}
	var records []sqlc.TriggerRecord
	now := time.Now()
	for _, r := range m.records {
		due := (r.State == "pending" || r.State == "retry") && !r.NextFireAt.Time.After(now)
		// A lost dispatcher's claim lapses back to due (the broker will not
		// redeliver an operator replay).
		lapsed := r.State == "claimed" && r.ClaimExpiresAt.Valid && !r.ClaimExpiresAt.Time.After(now)
		if r.TriggerID.String() == trigger && r.OperatorReplay && (due || lapsed) {
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].NextFireAt.Time.Equal(records[j].NextFireAt.Time) {
			return records[i].ID.String() < records[j].ID.String()
		}
		return records[i].NextFireAt.Time.Before(records[j].NextFireAt.Time)
	})
	return records[:min(len(records), limit)], nil
}
