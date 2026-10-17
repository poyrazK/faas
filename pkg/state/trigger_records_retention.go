package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TriggerRecordReaper deletes one bounded batch of expired trigger_records:
// rows past an explicit result_retention_until, and unpinned terminal rows
// older than api.TriggerRecordRetention (api.TriggerDeadLetterRetention for
// dead letters). Their trigger_dead_letter rows cascade.
type TriggerRecordReaper interface {
	DeleteExpiredTriggerRecords(ctx context.Context, now time.Time, limit int) (int, error)
}

func (s *PgStore) DeleteExpiredTriggerRecords(ctx context.Context, now time.Time, limit int) (int, error) {
	n, err := s.triggerQueries().DeleteExpiredTriggerRecords(ctx, s.pool, sqlc.DeleteExpiredTriggerRecordsParams{
		Now:              pgtypeFromTime(now),
		Batch:            int32(limit),
		TerminalBefore:   pgtypeFromTime(now.Add(-api.TriggerRecordRetention)),
		DeadLetterBefore: pgtypeFromTime(now.Add(-api.TriggerDeadLetterRetention)),
	})
	if err != nil {
		return 0, fmt.Errorf("state: delete expired trigger records: %w", err)
	}
	return int(n), nil
}

func (m *MemStore) DeleteExpiredTriggerRecords(_ context.Context, now time.Time, limit int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for id, r := range m.records {
		if deleted >= limit {
			break
		}
		if !memTriggerRecordExpired(r, now) {
			continue
		}
		delete(m.records, id)
		deleted++
	}
	return deleted, nil
}

func memTriggerRecordExpired(r sqlc.TriggerRecord, now time.Time) bool {
	switch r.State {
	case "succeeded", "superseded", "cancelled", "expired", "dead_letter":
	default:
		return false
	}
	if r.ResultRetentionUntil.Valid {
		return !r.ResultRetentionUntil.Time.After(now)
	}
	if r.WorkPolicyName.Valid {
		return false
	}
	age := r.ReceivedAt.Time
	if r.LastDispatchedAt.Valid {
		age = r.LastDispatchedAt.Time
	}
	horizon := api.TriggerRecordRetention
	if r.State == "dead_letter" {
		horizon = api.TriggerDeadLetterRetention
	}
	return age.Before(now.Add(-horizon))
}
