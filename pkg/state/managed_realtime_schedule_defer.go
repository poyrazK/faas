package state

import (
	"context"
	"time"
)

// ManagedRealtimeScheduleDeferStore postpones a due schedule without counting
// an attempt. apid's schedule runner uses it when the endpoint's account is
// over its realtime write rate, so that account's schedules leave the due
// list to other accounts' (ADR-1323, amendment of 2026-11-02). A schedule
// that changed since it was listed, or is no longer due, is left alone.
type ManagedRealtimeScheduleDeferStore interface {
	DeferManagedRealtimeSchedule(ctx context.Context, row ManagedRealtimeSchedule, until time.Time) error
}

func (m *MemStore) DeferManagedRealtimeSchedule(ctx context.Context, candidate ManagedRealtimeSchedule, until time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := scheduleKey(candidate)
	row, ok := m.managedRealtimeSchedules[key]
	now := time.Now().UTC()
	if !ok || row.Status != "pending" || row.Version != candidate.Version || realtimeScheduleDueAt(row).After(now) {
		return nil
	}
	next := until.UTC().Truncate(time.Microsecond)
	row.NextAttemptAt = &next
	m.managedRealtimeSchedules[key] = row
	return nil
}

func (s *PgStore) DeferManagedRealtimeSchedule(ctx context.Context, candidate ManagedRealtimeSchedule, until time.Time) error {
	_, err := s.pool.Exec(ctx, `update managed_realtime_schedules set next_attempt_at=$5 where endpoint_id=$1 and channel=$2 and schedule_id=$3 and version=$4 and status='pending' and coalesce(next_attempt_at,deliver_at)<=clock_timestamp()`,
		candidate.EndpointID, candidate.Channel, candidate.ID, candidate.Version, until.UTC().Truncate(time.Microsecond))
	return err
}
