package state

import (
	"context"
	"time"
)

type ManagedRealtimeScheduleRetryStore interface {
	RetryManagedRealtimeSchedule(context.Context, string, string, string, int64, *time.Time) (ManagedRealtimeSchedule, error)
}

func prepareRealtimeScheduleRetry(row ManagedRealtimeSchedule, version int64, deadline *time.Time, now time.Time) (ManagedRealtimeSchedule, error) {
	if row.Status != "failed" || row.Version != version || row.Version >= managedRealtimeMaxEntityVersion-1 || row.Attempts >= managedRealtimeMaxEntityVersion {
		return row, ErrConflict
	}
	next := now
	if deadline != nil {
		next = deadline.UTC().Truncate(time.Microsecond)
		if !validRealtimeDeliverAt(next, now) {
			return row, ErrManagedRealtimeHistoryInvalid
		}
	}
	next = next.Truncate(time.Microsecond)
	row.Status = "pending"
	row.NextAttemptAt = &next
	row.CycleAttempts = 0
	row.Version++
	row.UpdatedAt = now
	return row, nil
}
func (m *MemStore) RetryManagedRealtimeSchedule(ctx context.Context, ep, ch, id string, version int64, deadline *time.Time) (ManagedRealtimeSchedule, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || !reducerKeyValid(id) || version < 1 {
		return ManagedRealtimeSchedule{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeSchedule{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	m.trimRealtimeSchedulesLocked(now)
	key := managedRealtimeScheduleKey{ep, ch, id}
	row, ok := m.managedRealtimeSchedules[key]
	if !ok {
		return row, ErrNotFound
	}
	row, err := prepareRealtimeScheduleRetry(row, version, deadline, now)
	if err != nil {
		return row, err
	}
	m.managedRealtimeSchedules[key] = row
	m.recordRealtimeScheduleHistoryLocked(row, "manual_retry")
	return cloneRealtimeSchedule(row), nil
}
func (s *PgStore) RetryManagedRealtimeSchedule(ctx context.Context, ep, ch, id string, version int64, deadline *time.Time) (ManagedRealtimeSchedule, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || !reducerKeyValid(id) || version < 1 {
		return ManagedRealtimeSchedule{}, ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ManagedRealtimeSchedule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := readRealtimeSchedulePG(ctx, tx, ep, ch, id, true)
	if err != nil {
		return row, err
	}
	row, err = prepareRealtimeScheduleRetry(row, version, deadline, time.Now().UTC())
	if err != nil {
		return row, err
	}
	row, err = scanRealtimeSchedulePG(tx.QueryRow(ctx, `update managed_realtime_schedules set status='pending',next_attempt_at=$4,cycle_attempts=0,version=$5,updated_at=$6 where endpoint_id=$1 and channel=$2 and schedule_id=$3 returning `+realtimeScheduleColumns, ep, ch, id, row.NextAttemptAt, row.Version, row.UpdatedAt))
	if err != nil {
		return row, err
	}
	if err = recordRealtimeScheduleHistoryPG(ctx, tx, row, "manual_retry"); err != nil {
		return row, err
	}
	return row, tx.Commit(ctx)
}
