package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

const realtimeScheduleColumns = `endpoint_id,channel,schedule_id,data,is_binary,metadata,deliver_at,version,status,sequence,last_error,created_at,updated_at,max_attempts,backoff_seconds,attempts,cycle_attempts,next_attempt_at,last_attempt_at,interval_seconds,max_occurrences,end_at,initial_deliver_at,occurrence,completed_occurrences,conditions,on_condition_failure,skipped_occurrences,skip_reason,schedule_group`

func scanRealtimeSchedulePG(scanner interface{ Scan(...any) error }) (ManagedRealtimeSchedule, error) {
	var row ManagedRealtimeSchedule
	err := scanner.Scan(&row.EndpointID, &row.Channel, &row.ID, &row.Data, &row.Binary, &row.Metadata, &row.DeliverAt, &row.Version, &row.Status, &row.Sequence, &row.LastError, &row.CreatedAt, &row.UpdatedAt, &row.MaxAttempts, &row.BackoffSeconds, &row.Attempts, &row.CycleAttempts, &row.NextAttemptAt, &row.LastAttemptAt, &row.IntervalSeconds, &row.MaxOccurrences, &row.EndAt, &row.InitialDeliverAt, &row.Occurrence, &row.CompletedOccurrences, &row.Conditions, &row.OnConditionFailure, &row.SkippedOccurrences, &row.SkipReason, &row.Group)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return row, err
}
func readRealtimeSchedulePG(ctx context.Context, tx pgx.Tx, ep, ch, id string, lock bool) (ManagedRealtimeSchedule, error) {
	query := `select ` + realtimeScheduleColumns + ` from managed_realtime_schedules where endpoint_id=$1 and channel=$2 and schedule_id=$3 and (status IN ('pending','paused') or updated_at>=clock_timestamp()-interval '24 hours')`
	if lock {
		query += ` for update`
	}
	return scanRealtimeSchedulePG(tx.QueryRow(ctx, query, ep, ch, id))
}
func (s *PgStore) PutManagedRealtimeSchedule(ctx context.Context, row ManagedRealtimeSchedule) (ManagedRealtimeSchedule, error) {
	normalizeRealtimeRetryPolicy(&row)
	row.DeliverAt = row.DeliverAt.UTC().Truncate(time.Microsecond)
	if err := validateRealtimeSchedule(row); err != nil {
		return row, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return row, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var endpoint string
	if err = tx.QueryRow(ctx, `select id from managed_realtime_endpoints where id=$1 for update`, row.EndpointID).Scan(&endpoint); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrNotFound
		}
		return row, err
	}
	if _, err = tx.Exec(ctx, `delete from managed_realtime_schedules where endpoint_id=$1 and status NOT IN ('pending','paused') and updated_at<clock_timestamp()-interval '24 hours'`, row.EndpointID); err != nil {
		return row, err
	}
	old, err := readRealtimeSchedulePG(ctx, tx, row.EndpointID, row.Channel, row.ID, true)
	if err == nil {
		if sameRealtimeScheduleCreation(old, row) {
			return old, tx.Commit(ctx)
		}
		return row, ErrConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return row, err
	}
	if !validRealtimeDeliverAt(row.DeliverAt, time.Now().UTC()) {
		return row, ErrManagedRealtimeHistoryInvalid
	}
	var count int
	if err = tx.QueryRow(ctx, `select count(*) from managed_realtime_schedules where endpoint_id=$1`, row.EndpointID).Scan(&count); err != nil {
		return row, err
	}
	if count >= ManagedRealtimeScheduleLimit {
		return row, ErrManagedRealtimeScheduleLimit
	}
	row, err = scanRealtimeSchedulePG(tx.QueryRow(ctx, `insert into managed_realtime_schedules(endpoint_id,channel,schedule_id,data,is_binary,metadata,deliver_at,max_attempts,backoff_seconds,interval_seconds,max_occurrences,end_at,initial_deliver_at,conditions,on_condition_failure,schedule_group) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$7,$13,$14,$15) returning `+realtimeScheduleColumns, row.EndpointID, row.Channel, row.ID, row.Data, row.Binary, metadataJSON(row.Metadata), row.DeliverAt, row.MaxAttempts, row.BackoffSeconds, row.IntervalSeconds, row.MaxOccurrences, row.EndAt, nullableScheduleConditions(row.Conditions), row.OnConditionFailure, row.Group))
	if err != nil {
		return row, err
	}
	if err = recordRealtimeScheduleHistoryPG(ctx, tx, row, "created"); err != nil {
		return row, err
	}
	return row, tx.Commit(ctx)
}
func (s *PgStore) ListManagedRealtimeSchedules(ctx context.Context, ep, ch string) ([]ManagedRealtimeSchedule, error) {
	if validateManagedRealtimeHistoryRequest(ep, ch) != nil {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	rows, err := s.pool.Query(ctx, `select `+realtimeScheduleColumns+` from managed_realtime_schedules where endpoint_id=$1 and channel=$2 and (status IN ('pending','paused') or updated_at>=clock_timestamp()-interval '24 hours') order by coalesce(next_attempt_at,deliver_at),schedule_id`, ep, ch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ManagedRealtimeSchedule, 0)
	for rows.Next() {
		row, err := scanRealtimeSchedulePG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *PgStore) UpdateManagedRealtimeSchedule(ctx context.Context, ep, ch, id string, version int64, deadline *time.Time) (ManagedRealtimeSchedule, error) {
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
	if deadline == nil && row.Status == "canceled" {
		return row, tx.Commit(ctx)
	}
	if (row.Status != "pending" && !(deadline == nil && row.Status == "paused")) || row.Version != version || row.Version >= managedRealtimeMaxEntityVersion-1 {
		return row, ErrConflict
	}
	if deadline != nil {
		if row.EndAt != nil && deadline.After(*row.EndAt) {
			return row, ErrManagedRealtimeHistoryInvalid
		}
		if !validRealtimeDeliverAt(*deadline, time.Now().UTC()) {
			return row, ErrManagedRealtimeHistoryInvalid
		}
		row.DeliverAt = deadline.UTC().Truncate(time.Microsecond)
	} else {
		row.Status = "canceled"
	}
	row, err = scanRealtimeSchedulePG(tx.QueryRow(ctx, `update managed_realtime_schedules set deliver_at=$4,status=$5,next_attempt_at=null,version=version+1,updated_at=clock_timestamp() where endpoint_id=$1 and channel=$2 and schedule_id=$3 returning `+realtimeScheduleColumns, ep, ch, id, row.DeliverAt, row.Status))
	if err != nil {
		return row, err
	}
	kind := "rescheduled"
	if deadline == nil {
		kind = "canceled"
	}
	if err = recordRealtimeScheduleHistoryPG(ctx, tx, row, kind); err != nil {
		return row, err
	}
	return row, tx.Commit(ctx)
}
func (s *PgStore) ListDueManagedRealtimeSchedules(ctx context.Context, limit int) ([]ManagedRealtimeSchedule, error) {
	if limit < 1 || limit > 256 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	// Terminal records are bounded by the per-endpoint capacity and pruned on creation.
	rows, err := s.pool.Query(ctx, `select `+realtimeScheduleColumns+` from managed_realtime_schedules where status='pending' and coalesce(next_attempt_at,deliver_at)<=clock_timestamp() order by coalesce(next_attempt_at,deliver_at),endpoint_id,channel,schedule_id limit $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ManagedRealtimeSchedule, 0)
	for rows.Next() {
		row, err := scanRealtimeSchedulePG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
func (s *PgStore) PublishManagedRealtimeSchedule(ctx context.Context, row ManagedRealtimeSchedule) (ManagedRealtimeChannelMessage, error) {
	return s.appendManagedRealtimeChannel(ctx, row.EndpointID, row.Channel, row.Data, row.Binary, "", scheduledRealtimeMetadata(row), nil, nil, &row)
}
func (s *PgStore) FailManagedRealtimeSchedule(ctx context.Context, candidate ManagedRealtimeSchedule, code string) error {
	if code == "" || len(code) > 128 || strings.ContainsAny(code, "\r\n") {
		return ErrManagedRealtimeHistoryInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := readRealtimeSchedulePG(ctx, tx, candidate.EndpointID, candidate.Channel, candidate.ID, true)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !scheduleMatches(row, candidate, time.Now().UTC()) {
		return nil
	}
	row = recordRealtimeScheduleFailure(row, code, time.Now().UTC())
	_, err = tx.Exec(ctx, `update managed_realtime_schedules set status=$4,last_error=$5,attempts=$6,cycle_attempts=$7,next_attempt_at=$8,last_attempt_at=$9,updated_at=$9,version=$10 where endpoint_id=$1 and channel=$2 and schedule_id=$3`, row.EndpointID, row.Channel, row.ID, row.Status, row.LastError, row.Attempts, row.CycleAttempts, row.NextAttemptAt, row.LastAttemptAt, row.Version)
	if err != nil {
		return err
	}
	if err = recordRealtimeScheduleHistoryPG(ctx, tx, row, "attempt_failed"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
