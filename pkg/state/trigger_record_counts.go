package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TriggerRecordStateCounts is how many of a trigger's records are in each
// state, each count stopping at the limit asked for.
type TriggerRecordStateCounts struct {
	Pending    int64
	Claimed    int64
	Succeeded  int64
	Retry      int64
	DeadLetter int64
}

// TriggerRecordStateCounter counts a trigger's records per state
// (ADR-20261101104512). It is optional, like TriggerConsumerHealthStore, so
// narrow test stores need not implement it.
type TriggerRecordStateCounter interface {
	CountTriggerRecordsByState(ctx context.Context, triggerID string, limit int64) (TriggerRecordStateCounts, error)
}

// CountTriggerRecordsByState counts the trigger's records per state, each
// count stopping at limit.
func (s *PgStore) CountTriggerRecordsByState(ctx context.Context, triggerID string, limit int64) (TriggerRecordStateCounts, error) {
	row, err := sqlc.New().CountTriggerRecordsByState(ctx, s.pool, sqlc.CountTriggerRecordsByStateParams{
		TriggerID:  mustPgUUID(triggerID),
		CountLimit: limit,
	})
	if err != nil {
		return TriggerRecordStateCounts{}, fmt.Errorf("count trigger records by state: %w", err)
	}
	return TriggerRecordStateCounts{
		Pending: row.Pending, Claimed: row.Claimed, Succeeded: row.Succeeded,
		Retry: row.Retry, DeadLetter: row.DeadLetter,
	}, nil
}

// CountTriggerRecordsByState counts the trigger's records per state, each
// count stopping at limit.
func (m *MemStore) CountTriggerRecordsByState(_ context.Context, triggerID string, limit int64) (TriggerRecordStateCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out TriggerRecordStateCounts
	for _, r := range m.records {
		if r.TriggerID.String() != triggerID {
			continue
		}
		var count *int64
		switch r.State {
		case "pending":
			count = &out.Pending
		case "claimed":
			count = &out.Claimed
		case "succeeded":
			count = &out.Succeeded
		case "retry":
			count = &out.Retry
		case "dead_letter":
			count = &out.DeadLetter
		}
		if count != nil && *count < limit {
			*count++
		}
	}
	return out, nil
}
