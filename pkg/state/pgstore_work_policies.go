package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

const scheduleOccurrenceSelectCols = `id::text, account_id::text, coalesce(cron_id::text,''), coalesce(job_id::text,''),
       schedule_revision, scheduled_for, start_deadline_at, schedule_policy, status, reason,
       coalesce(blocking_occurrence_id::text,''), coalesce(invocation_id::text,''), coalesce(app_task_id::text,''),
       coalesce(job_run_id::text,''), started_at, finished_at, created_at, updated_at, work_decision, outcome_code`

func scanScheduleOccurrence(row interface{ Scan(...any) error }) (ScheduleOccurrence, error) {
	var o ScheduleOccurrence
	var policy, workDecision []byte
	var deadline, started, finished pgtype.Timestamptz
	if err := row.Scan(&o.ID, &o.AccountID, &o.CronID, &o.JobID, &o.ScheduleRevision,
		&o.ScheduledFor, &deadline, &policy, &o.Status, &o.Reason,
		&o.BlockingOccurrenceID, &o.InvocationID, &o.AppTaskID, &o.JobRunID,
		&started, &finished, &o.CreatedAt, &o.UpdatedAt, &workDecision, &o.OutcomeCode); err != nil {
		return ScheduleOccurrence{}, err
	}
	if deadline.Valid {
		o.StartDeadlineAt = &deadline.Time
	}
	if started.Valid {
		o.StartedAt = &started.Time
	}
	if finished.Valid {
		o.FinishedAt = &finished.Time
	}
	if err := json.Unmarshal(policy, &o.SchedulePolicy); err != nil {
		return ScheduleOccurrence{}, err
	}
	if len(workDecision) > 0 {
		var decision workpolicy.Decision
		if err := json.Unmarshal(workDecision, &decision); err != nil {
			return ScheduleOccurrence{}, err
		}
		o.WorkDecision = &decision
	}
	return o, nil
}

func (s *PgStore) ScheduleOccurrenceListByJob(ctx context.Context, jobID string, limit int, before string) ([]ScheduleOccurrence, error) {
	return s.listScheduleOccurrences(ctx, `job_id`, jobID, limit, before)
}

func (s *PgStore) ScheduleOccurrenceListByCron(ctx context.Context, cronID string, limit int, before string) ([]ScheduleOccurrence, error) {
	return s.listScheduleOccurrences(ctx, `cron_id`, cronID, limit, before)
}

func (s *PgStore) listScheduleOccurrences(ctx context.Context, column, id string, limit int, before string) ([]ScheduleOccurrence, error) {
	if column != "job_id" && column != "cron_id" {
		return nil, ErrInvalidArgument
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := `select ` + scheduleOccurrenceSelectCols + ` from schedule_occurrences where ` + column + `=$1::uuid order by scheduled_for desc,id desc limit $2`
	args := []any{id, limit}
	if before != "" {
		query = `select ` + scheduleOccurrenceSelectCols + ` from schedule_occurrences
		 where ` + column + `=$1::uuid and (scheduled_for,id) < (select scheduled_for,id from schedule_occurrences where id=$2::uuid and ` + column + `=$1::uuid)
		 order by scheduled_for desc,id desc limit $3`
		args = []any{id, before, limit}
	}
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list schedule occurrences: %w", err)
	}
	defer rows.Close()
	out := make([]ScheduleOccurrence, 0)
	for rows.Next() {
		o, err := scanScheduleOccurrence(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan schedule occurrence: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

var _ ScheduleOccurrenceHistoryStore = (*PgStore)(nil)
