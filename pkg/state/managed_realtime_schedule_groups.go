package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Group actions require the exact version set of every pending/paused member.
// Terminal receipts are excluded; all mutations and history entries commit together.
type ManagedRealtimeScheduleGroupStore interface {
	ApplyManagedRealtimeScheduleGroup(context.Context, string, string, string, string, map[string]int64) ([]ManagedRealtimeSchedule, error)
}

func validateRealtimeScheduleGroup(ep, ch, group, action string, versions map[string]int64) error {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil || !reducerKeyValid(group) || (action != "pause" && action != "resume" && action != "cancel") || len(versions) > ManagedRealtimeScheduleLimit {
		return ErrManagedRealtimeHistoryInvalid
	}
	for id, version := range versions {
		if !reducerKeyValid(id) || version < 1 || version > managedRealtimeMaxEntityVersion {
			return ErrManagedRealtimeHistoryInvalid
		}
	}
	return nil
}
func prepareRealtimeScheduleGroup(rows []ManagedRealtimeSchedule, action string, versions map[string]int64, now time.Time) ([]ManagedRealtimeSchedule, []string, error) {
	if len(rows) != len(versions) {
		return nil, nil, ErrConflict
	}
	out := make([]ManagedRealtimeSchedule, len(rows))
	kinds := make([]string, len(rows))
	for i, row := range rows {
		if versions[row.ID] != row.Version {
			return nil, nil, ErrConflict
		}
		target, kind := "paused", "paused"
		if action == "resume" {
			target, kind = "pending", "resumed"
		}
		if action == "cancel" {
			target, kind = "canceled", "canceled"
		}
		if row.Status != target {
			if row.Version >= managedRealtimeMaxEntityVersion-1 || (action == "resume" && row.EndAt != nil && now.After(*row.EndAt)) {
				return nil, nil, ErrConflict
			}
			row.Status = target
			row.Version++
			row.UpdatedAt = now
			if action == "cancel" {
				row.NextAttemptAt = nil
			}
			kinds[i] = kind
		}
		out[i] = cloneRealtimeSchedule(row)
	}
	return out, kinds, nil
}
func (m *MemStore) ApplyManagedRealtimeScheduleGroup(ctx context.Context, ep, ch, group, action string, versions map[string]int64) ([]ManagedRealtimeSchedule, error) {
	if err := validateRealtimeScheduleGroup(ep, ch, group, action, versions); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[ep]; !ok {
		return nil, ErrNotFound
	}
	rows := make([]ManagedRealtimeSchedule, 0)
	for key, row := range m.managedRealtimeSchedules {
		if key.endpointID == ep && key.channel == ch && row.Group == group && (row.Status == "pending" || row.Status == "paused") {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	out, kinds, err := prepareRealtimeScheduleGroup(rows, action, versions, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	for i, row := range out {
		if kinds[i] != "" {
			m.managedRealtimeSchedules[scheduleKey(row)] = row
			m.recordRealtimeScheduleHistoryLocked(row, kinds[i])
		}
	}
	return out, nil
}
func (s *PgStore) ApplyManagedRealtimeScheduleGroup(ctx context.Context, ep, ch, group, action string, versions map[string]int64) ([]ManagedRealtimeSchedule, error) {
	if err := validateRealtimeScheduleGroup(ep, ch, group, action, versions); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize group membership with schedule creation before locking members.
	var endpoint string
	if err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, ep).Scan(&endpoint); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return nil, err
	}
	result, err := tx.Query(ctx, `select `+realtimeScheduleColumns+` from managed_realtime_schedules where endpoint_id=$1 and channel=$2 and schedule_group=$3 and status in ('pending','paused') order by schedule_id for update`, ep, ch, group)
	if err != nil {
		return nil, err
	}
	rows := make([]ManagedRealtimeSchedule, 0)
	for result.Next() {
		row, scanErr := scanRealtimeSchedulePG(result)
		if scanErr != nil {
			result.Close()
			return nil, scanErr
		}
		rows = append(rows, row)
	}
	err = result.Err()
	result.Close()
	if err != nil {
		return nil, err
	}
	out, kinds, err := prepareRealtimeScheduleGroup(rows, action, versions, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	for i, row := range out {
		if kinds[i] == "" {
			continue
		}
		_, err = tx.Exec(ctx, `update managed_realtime_schedules set status=$4,version=$5,updated_at=$6,next_attempt_at=$7 where endpoint_id=$1 and channel=$2 and schedule_id=$3`, ep, ch, row.ID, row.Status, row.Version, row.UpdatedAt, row.NextAttemptAt)
		if err != nil {
			return nil, err
		}
		if err = recordRealtimeScheduleHistoryPG(ctx, tx, row, kinds[i]); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
