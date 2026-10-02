package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// CreateScheduledCronInvocationOccurrence atomically advances an HTTP Cron's
// cursor, records the policy decision, and queues its synthetic invocation.
// The Cron row lock serializes scheduler replicas and policy edits.
func (s *PgStore) CreateScheduledCronInvocationOccurrence(ctx context.Context, cronID string, expectedLastFiredAt *time.Time, evaluatedAt time.Time, options CronScheduledOccurrenceOptions, invocation Invocation) (Invocation, ScheduleOccurrence, bool, error) {
	if cronID == "" || options.ScheduleRevision <= 0 {
		return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
	}
	evaluatedAt = evaluatedAt.UTC()
	scheduledFor := options.ScheduledFor.UTC()
	if options.ScheduledFor.IsZero() {
		scheduledFor = evaluatedAt
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: begin scheduled HTTP cron occurrence: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cron Cron
	var accountID string
	var enabled, skipIfRunning bool
	var suspendedReason string
	var lastFiredAt pgtype.Timestamptz
	var schedulePolicyRaw, failureRulesRaw []byte
	err = tx.QueryRow(ctx, `
		select c.app_id::text, a.account_id::text, c.path, c.command,
		       c.enabled, c.skip_if_running, c.suspended_reason,
	       c.last_fired_at, c.schedule_revision, c.schedule_policy,
	       c.failure_rules
		  from crons c join apps a on a.id = c.app_id
		 where c.id = $1::uuid and a.status <> 'deleted'
		 for update of c`, cronID).Scan(
		&cron.AppID, &accountID, &cron.Path, &cron.Command,
		&enabled, &skipIfRunning, &suspendedReason, &lastFiredAt,
		&cron.ScheduleRevision, &schedulePolicyRaw, &failureRulesRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ScheduleOccurrence{}, false, nil
	}
	if err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: lock scheduled HTTP cron %s: %w", cronID, err)
	}
	cron.ID, cron.Enabled, cron.SkipIfRunning, cron.SuspendedReason = cronID, enabled, skipIfRunning, suspendedReason
	if !enabled || suspendedReason != "" || len(cron.Command) != 0 ||
		!sameTimePointer(timestamptzToTimePtr(lastFiredAt), expectedLastFiredAt) ||
		cron.ScheduleRevision != options.ScheduleRevision ||
		(expectedLastFiredAt != nil && !scheduledFor.After(*expectedLastFiredAt)) {
		return Invocation{}, ScheduleOccurrence{}, false, nil
	}
	if len(schedulePolicyRaw) > 0 {
		var policy workpolicy.SchedulePolicy
		if err := json.Unmarshal(schedulePolicyRaw, &policy); err != nil {
			return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: decode HTTP cron schedule policy: %w", err)
		}
		cron.SchedulePolicy = &policy
	}
	if len(failureRulesRaw) > 0 {
		var failureRules workpolicy.FailureRules
		if err := json.Unmarshal(failureRulesRaw, &failureRules); err != nil {
			return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: decode HTTP cron failure rules: %w", err)
		}
		cron.FailureRules = &failureRules
	}
	policy := effectiveCronSchedulePolicy(cron)
	deadline := policy.Deadline(scheduledFor)
	status, reason, blocker := "queued", "", ""
	if options.Disposition != "" {
		if options.Disposition != "coalesced" && options.Disposition != "missed_deadline" {
			return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
		}
		status, reason = options.Disposition, options.Reason
	} else if workpolicy.DeadlineMissed(deadline, evaluatedAt) {
		status, reason = "missed_deadline", "start deadline expired before the scheduler could dispatch the occurrence"
	}

	var replacedPending bool
	var replacedOccurrenceIDs []string
	if status == "queued" && policy.Overlap != "allow" {
		rows, queryErr := tx.Query(ctx, `
			select id::text, state, coalesce(occurrence_id::text, ''), received_at is not null
			  from invocations
			 where cron_id = $1::uuid and source = 'cron'
			   and state in ('pending','dispatching')
			 order by created_at, id
			 for update`, cronID)
		if queryErr != nil {
			return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: list active HTTP cron invocations: %w", queryErr)
		}
		type activeInvocation struct {
			id, state, occurrenceID string
			hasStarted              bool
		}
		active := make([]activeInvocation, 0, 2)
		for rows.Next() {
			var row activeInvocation
			if scanErr := rows.Scan(&row.id, &row.state, &row.occurrenceID, &row.hasStarted); scanErr != nil {
				rows.Close()
				return Invocation{}, ScheduleOccurrence{}, false, scanErr
			}
			active = append(active, row)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return Invocation{}, ScheduleOccurrence{}, false, rowsErr
		}
		rows.Close()
		if len(active) > 0 {
			if policy.Overlap == "skip" {
				status, reason = "skipped_overlap", "an earlier invocation for this cron is still active"
				blocker = active[0].occurrenceID
			} else {
				for _, row := range active {
					// A pending scheduled invocation has not started and can be
					// replaced safely. A dispatching invocation or a manual fire
					// cannot be stopped with confirmed cancellation evidence.
					if row.state != "pending" || row.occurrenceID == "" || row.hasStarted {
						return Invocation{}, ScheduleOccurrence{}, false, nil
					}
					replacedOccurrenceIDs = append(replacedOccurrenceIDs, row.occurrenceID)
				}
				replacedPending = len(replacedOccurrenceIDs) > 0
			}
		}
	}

	if _, err := tx.Exec(ctx, `update crons set last_fired_at = $2 where id = $1::uuid`, cronID, scheduledFor); err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: advance scheduled HTTP cron cursor: %w", err)
	}
	var occurrenceID string
	if err := tx.QueryRow(ctx, `insert into schedule_occurrences (
		 account_id, cron_id, schedule_revision, scheduled_for, start_deadline_at,
		 schedule_policy, status, reason, blocking_occurrence_id, created_at, updated_at)
		values ($1::uuid,$2::uuid,$3,$4,$5,$6::jsonb,$7,$8,nullif($9,'')::uuid,$10,$10)
		returning id::text`, accountID, cronID, cron.ScheduleRevision, scheduledFor, deadline,
		policyJSON(policy), status, reason, blocker, evaluatedAt).Scan(&occurrenceID); err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: record scheduled HTTP cron occurrence: %w", mapErr(err))
	}
	occurrence := ScheduleOccurrence{
		ID: occurrenceID, AccountID: accountID, CronID: cronID,
		ScheduleRevision: cron.ScheduleRevision, ScheduledFor: scheduledFor,
		StartDeadlineAt: cloneTimePtr(deadline), SchedulePolicy: *workpolicy.Clone(policy),
		Status: status, Reason: reason, BlockingOccurrenceID: blocker,
		CreatedAt: evaluatedAt, UpdatedAt: evaluatedAt,
	}
	if replacedPending {
		for _, replacedOccurrenceID := range replacedOccurrenceIDs {
			if _, err := tx.Exec(ctx, `
				update invocations
				   set state = 'cancelled', completed_at = $2,
				       quota_reserved = false, last_error = 'replaced by a newer scheduled occurrence'
				 where occurrence_id = $1::uuid and cron_id = $3::uuid and source = 'cron'
				   and state = 'pending'`, replacedOccurrenceID, evaluatedAt, cronID); err != nil {
				return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: replace pending HTTP cron invocation: %w", err)
			}
			if _, err := tx.Exec(ctx, `update schedule_occurrences
				set reason = 'cancelled before dispatch by a newer scheduled occurrence', updated_at = $2
				where id = $1::uuid`, replacedOccurrenceID, evaluatedAt); err != nil {
				return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: explain replaced HTTP cron occurrence: %w", err)
			}
		}
	}
	if status != "queued" {
		if err := tx.Commit(ctx); err != nil {
			return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: commit skipped HTTP cron occurrence: %w", err)
		}
		return Invocation{}, occurrence, false, nil
	}
	if invocation.AppID != "" && invocation.AppID != cron.AppID {
		return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
	}
	if invocation.AccountID != "" && invocation.AccountID != accountID {
		return Invocation{}, ScheduleOccurrence{}, false, ErrInvalidArgument
	}
	cronIDCopy := cronID
	invocation.AppID = cron.AppID
	invocation.AccountID = accountID
	invocation.Source = InvocationCron
	invocation.CronID = &cronIDCopy
	invocation.FailureRules = workpolicy.Clone(cron.FailureRules)
	invocation.OccurrenceID = occurrenceID
	invocation.StartDeadlineAt = cloneTimePtr(deadline)
	invocation.DueAt = scheduledFor
	if invocation.ScheduledAt == nil {
		invocation.ScheduledAt = cloneTimePtr(&scheduledFor)
	}
	enqueued, err := enqueueInvocationRow(ctx, tx, invocation)
	if err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: enqueue scheduled HTTP cron invocation: %w", err)
	}
	if _, err := tx.Exec(ctx, `update schedule_occurrences set invocation_id=$2::uuid where id=$1::uuid`, occurrenceID, enqueued.ID); err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: link HTTP cron occurrence to invocation: %w", err)
	}
	occurrence.InvocationID = enqueued.ID
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, ScheduleOccurrence{}, false, fmt.Errorf("state: commit scheduled HTTP cron invocation: %w", err)
	}
	return enqueued, occurrence, true, nil
}

// ExpireUnstartedScheduledCronInvocations terminalizes only pending scheduled
// invocations that have not crossed their first-start deadline. Claim paths
// independently reject late starts, closing the sweep/claim race.
func (s *PgStore) ExpireUnstartedScheduledCronInvocations(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 64
	}
	now = now.UTC()
	var expired int
	err := s.pool.QueryRow(ctx, `
		with overdue as (
			select id from invocations
			 where state = 'pending' and source = 'cron'
			   and occurrence_id is not null and start_deadline_at is not null
			   and start_deadline_at < $1 and received_at is null
			 order by start_deadline_at, id
			 for update skip locked
			 limit $2
		), failed as (
			update invocations i
			   set state = 'failed', outcome = 'timeout', completed_at = $1,
			       quota_reserved = false,
			       last_error = 'scheduled occurrence missed its start deadline'
			  from overdue o
			 where i.id = o.id and i.state = 'pending' and i.received_at is null
			 returning i.id
		)
		select count(*) from failed`, now, limit).Scan(&expired)
	if err != nil {
		return 0, fmt.Errorf("state: expire unstarted scheduled HTTP cron invocations: %w", err)
	}
	return expired, nil
}

var _ ScheduledCronInvocationStore = (*PgStore)(nil)
var _ ScheduledInvocationDeadlineStore = (*PgStore)(nil)
