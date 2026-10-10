package state

import (
	"bytes"
	"context"
	"strconv"
	"time"
)

type ManagedRealtimeRecurrenceStore interface {
	SetManagedRealtimeSchedulePaused(context.Context, string, string, string, int64, bool) (ManagedRealtimeSchedule, error)
}

// Fixed-delay recurrence avoids replaying a burst of missed occurrences after an outage.
func completeRealtimeSchedule(row ManagedRealtimeSchedule, sequence int64, now time.Time) (ManagedRealtimeSchedule, ManagedRealtimeSchedule) {
	return finishRealtimeScheduleOccurrence(row, sequence, now, "")
}
func finishRealtimeScheduleOccurrence(row ManagedRealtimeSchedule, sequence int64, now time.Time, skipReason string) (ManagedRealtimeSchedule, ManagedRealtimeSchedule) {
	row.Status = "published"
	if skipReason == "" {
		row.Sequence = sequence
	} else {
		row.Status = "skipped"
		row.SkippedOccurrences++
		row.SkipReason = skipReason
		row.LastError = "realtime_schedule_condition_failed"
	}
	row.Attempts++
	row.CycleAttempts++
	row.Version++
	row.LastAttemptAt = &now
	row.UpdatedAt = now
	row.NextAttemptAt = nil
	if row.IntervalSeconds > 0 && skipReason == "" {
		row.CompletedOccurrences++
	}
	eventRow := row
	if row.IntervalSeconds > 0 && row.Occurrence < managedRealtimeMaxEntityVersion && row.Version < managedRealtimeMaxEntityVersion-1 && row.Attempts < managedRealtimeMaxEntityVersion && (row.MaxOccurrences == 0 || row.CompletedOccurrences+row.SkippedOccurrences < row.MaxOccurrences) {
		next := now.Add(time.Duration(row.IntervalSeconds) * time.Second).Truncate(time.Microsecond)
		if row.EndAt == nil || !next.After(*row.EndAt) {
			row.Status = "pending"
			row.Occurrence++
			row.DeliverAt = next
			row.CycleAttempts = 0
		}
	}
	return row, eventRow
}
func prepareRealtimePause(row ManagedRealtimeSchedule, version int64, pause bool, now time.Time) (ManagedRealtimeSchedule, error) {
	if row.IntervalSeconds == 0 || row.Version != version || row.Version >= managedRealtimeMaxEntityVersion-1 || (pause && row.Status != "pending") || (!pause && row.Status != "paused") {
		return row, ErrConflict
	}
	if pause {
		row.Status = "paused"
	} else {
		if row.EndAt != nil && now.After(*row.EndAt) {
			return row, ErrConflict
		}
		row.Status = "pending"
	}
	row.Version++
	row.UpdatedAt = now
	return row, nil
}
func (m *MemStore) SetManagedRealtimeSchedulePaused(ctx context.Context, ep, ch, id string, version int64, pause bool) (ManagedRealtimeSchedule, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || !reducerKeyValid(id) || version < 1 {
		return ManagedRealtimeSchedule{}, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeSchedule{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trimRealtimeSchedulesLocked(time.Now().UTC())
	key := managedRealtimeScheduleKey{ep, ch, id}
	row, ok := m.managedRealtimeSchedules[key]
	if !ok {
		return row, ErrNotFound
	}
	row, err := prepareRealtimePause(row, version, pause, time.Now().UTC())
	if err != nil {
		return row, err
	}
	m.managedRealtimeSchedules[key] = row
	kind := "paused"
	if !pause {
		kind = "resumed"
	}
	m.recordRealtimeScheduleHistoryLocked(row, kind)
	return cloneRealtimeSchedule(row), nil
}
func (s *PgStore) SetManagedRealtimeSchedulePaused(ctx context.Context, ep, ch, id string, version int64, pause bool) (ManagedRealtimeSchedule, error) {
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
	row, err = prepareRealtimePause(row, version, pause, time.Now().UTC())
	if err != nil {
		return row, err
	}
	_, err = tx.Exec(ctx, `update managed_realtime_schedules set status=$4,version=$5,updated_at=$6 where endpoint_id=$1 and channel=$2 and schedule_id=$3`, ep, ch, id, row.Status, row.Version, row.UpdatedAt)
	if err != nil {
		return row, err
	}
	kind := "paused"
	if !pause {
		kind = "resumed"
	}
	if err = recordRealtimeScheduleHistoryPG(ctx, tx, row, kind); err != nil {
		return row, err
	}
	return row, tx.Commit(ctx)
}

func sameRealtimeScheduleCreation(old, row ManagedRealtimeSchedule) bool {
	deadline := old.DeliverAt
	if old.IntervalSeconds > 0 {
		deadline = old.InitialDeliverAt
	}
	sameEnd := old.EndAt == nil && row.EndAt == nil || old.EndAt != nil && row.EndAt != nil && old.EndAt.Equal(*row.EndAt)
	return old.Group == row.Group && bytes.Equal(old.Conditions, row.Conditions) && old.OnConditionFailure == row.OnConditionFailure && old.IntervalSeconds == row.IntervalSeconds && old.MaxOccurrences == row.MaxOccurrences && sameEnd && old.MaxAttempts == row.MaxAttempts && old.BackoffSeconds == row.BackoffSeconds && deadline.Equal(row.DeliverAt) && old.Binary == row.Binary && bytes.Equal(old.Data, row.Data) && equalRealtimeMetadata(old.Metadata, row.Metadata)
}

func scheduledRealtimeMetadata(row ManagedRealtimeSchedule) map[string]string {
	metadata := cloneRealtimeMetadata(row.Metadata)
	if row.IntervalSeconds > 0 {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata["schedule_id"] = row.ID
		metadata["schedule_occurrence"] = strconv.FormatInt(row.Occurrence, 10)
	}
	return metadata
}
