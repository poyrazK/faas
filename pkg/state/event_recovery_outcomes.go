package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func recoveryExecution(now time.Time, state string, attempts int, completed *time.Time, attemptOutcome string, attemptNumber int, finished *time.Time) api.EventRecoveryExecution {
	out := api.EventRecoveryExecution{ObservedAt: now, State: "unknown", Source: "unavailable"}
	if state != "" {
		out.Source = "invocation"
		out.Attempts = attempts
		switch InvocationState(state) {
		case InvocationPending:
			out.State = "queued"
			if attempts > 0 {
				out.State = "retrying"
			}
		case InvocationDispatching:
			out.State = "running"
		case InvocationCompleted:
			out.State = "succeeded"
		case InvocationFailed:
			out.State = "failed"
		case InvocationDeadLetter:
			out.State = "dead_lettered"
		case InvocationExpired:
			out.State = "expired"
		case InvocationCancelled:
			out.State = "cancelled"
		case InvocationSuperseded:
			out.State = "superseded"
		}
		out.CompletedAt = cloneEventReceiptTime(completed)
		return out
	}
	if attemptOutcome == "" {
		return out
	}
	out.Source = "attempt_history"
	out.Attempts = attemptNumber
	// A retry or a dispatch attempt alone cannot prove the final execution state.
	if finished == nil || finished.After(now) {
		return out
	}
	switch attemptOutcome {
	case "succeeded", "failed", "cancelled":
		out.State = attemptOutcome
	case "dead_letter":
		out.State = "dead_lettered"
	}
	if out.State != "unknown" {
		out.CompletedAt = cloneEventReceiptTime(finished)
	}
	return out
}
func addRecoveryExecution(summary *api.EventRecoveryExecutionSummary, out api.EventRecoveryExecution) {
	summary.TrackedCount++
	switch out.State {
	case "queued":
		summary.Queued++
	case "running":
		summary.Running++
	case "retrying":
		summary.Retrying++
	case "succeeded":
		summary.Succeeded++
	case "failed":
		summary.Failed++
	case "dead_lettered":
		summary.DeadLettered++
	case "expired":
		summary.Expired++
	case "cancelled":
		summary.Cancelled++
	case "superseded":
		summary.Superseded++
	default:
		summary.Unknown++
	}
}
func recoveryExecutionObservation(row sqlc.EventRecoveryExecutionObservationsRow, now time.Time) api.EventRecoveryExecution {
	out := recoveryExecution(now, row.InvocationState, int(row.InvocationAttempts), timestamptzToTimePtr(row.InvocationCompletedAt), row.AttemptOutcome, int(row.AttemptNumber), timestamptzToTimePtr(row.AttemptFinishedAt))
	if row.InvocationOutcome == string(OutcomeUncertain) {
		out.State = "unknown"
		out.CompletedAt = nil
	}
	return out
}
func observeRecoveryExecutions(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, job string, after, through int64, now time.Time) (map[int64]api.EventRecoveryExecution, error) {
	rows, err := q.EventRecoveryExecutionObservations(ctx, db, sqlc.EventRecoveryExecutionObservationsParams{JobID: mustPgUUID(job), AccountID: mustPgUUID(account), AfterPosition: after, ThroughPosition: through, NowAt: pgtypeFromTime(now)})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]api.EventRecoveryExecution, len(rows))
	for _, row := range rows {
		out[row.Position] = recoveryExecutionObservation(row, now)
	}
	return out, nil
}
func getEventRecovery(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, job string) (api.EventRecoveryJob, error) {
	out, err := getEventRecoveryMetadata(ctx, q, db, account, job)
	if err != nil || out.Selection.Mode != "execution" {
		return out, err
	}
	now := time.Now().UTC()
	observations, err := observeRecoveryExecutions(ctx, q, db, account, job, 0, api.EventRecoveryRecipientsMax, now)
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	summary := api.EventRecoveryExecutionSummary{ObservedAt: now}
	for _, execution := range observations {
		addRecoveryExecution(&summary, execution)
	}
	out.Execution = &summary
	return out, nil
}
func (s *PgStore) GetEventRecovery(ctx context.Context, account, job string) (api.EventRecoveryJob, error) {
	if err := eventRecoveryIDs(account, job); err != nil {
		return api.EventRecoveryJob{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EventRecoveryJob{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := getEventRecovery(ctx, sqlc.New(), tx, account, job)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

type recoveryAttemptKey struct {
	ID         string
	Generation int64
}

func (m *MemStore) recoveryAttemptIndexLocked(account, app string, now time.Time) map[recoveryAttemptKey]InvocationAttempt {
	out := map[recoveryAttemptKey]InvocationAttempt{}
	for _, h := range m.invocationAttemptHistory {
		if !sameMemUUID(h.accountID, account) || !sameMemUUID(h.appID, app) || !h.RetainUntil.After(now) || h.StartedAt.After(now) {
			continue
		}
		key := recoveryAttemptKey{h.InvocationID, h.ReplayGeneration}
		if old, ok := out[key]; !ok || h.Attempt > old.Attempt {
			out[key] = h.InvocationAttempt
		}
	}
	return out
}
func (m *MemStore) eventRecoveryObservedItemWithAttemptsLocked(job *memEventRecoveryJob, item memEventRecoveryItem, now time.Time, attempts map[recoveryAttemptKey]InvocationAttempt) api.EventRecoveryItem {
	out := item.EventRecoveryItem
	out.Execution = nil
	if item.ReplayGeneration != nil {
		generation := *item.ReplayGeneration
		out.ReplayGeneration = &generation
	}
	if job.Job.Selection.Mode != "execution" || item.State != "queued" {
		return out
	}
	execution := recoveryExecution(now, "", 0, nil, "", 0, nil)
	if item.ReplayInvocationID != "" && item.ReplayGeneration != nil {
		inv, ok := m.invocations[item.ReplayInvocationID]
		if ok && sameMemUUID(inv.AccountID, job.AccountID) && sameMemUUID(inv.AppID, job.Job.AppID) && inv.ReplayGeneration == *item.ReplayGeneration && inv.CreatedAt.Equal(item.ReplayCreatedAt) {
			execution = recoveryExecution(now, string(inv.State), inv.Attempts, inv.CompletedAt, "", 0, nil)
			if inv.Outcome != nil && *inv.Outcome == OutcomeUncertain {
				execution.State = "unknown"
				execution.CompletedAt = nil
			}
		} else if h, ok := attempts[recoveryAttemptKey{item.ReplayInvocationID, *item.ReplayGeneration}]; ok && !h.StartedAt.Before(item.ReplayCreatedAt) {
			execution = recoveryExecution(now, "", 0, nil, h.Outcome, h.Attempt, h.FinishedAt)
		}
	}
	out.Execution = &execution
	return out
}
func (m *MemStore) eventRecoveryObservedResponseLocked(job *memEventRecoveryJob, now time.Time) api.EventRecoveryJob {
	out := memEventRecoveryResponse(job)
	if out.Selection.Mode != "execution" {
		return out
	}
	summary := api.EventRecoveryExecutionSummary{ObservedAt: now}
	attempts := m.recoveryAttemptIndexLocked(job.AccountID, job.Job.AppID, now)
	for _, item := range job.Items {
		observed := m.eventRecoveryObservedItemWithAttemptsLocked(job, item, now, attempts)
		if observed.Execution != nil {
			addRecoveryExecution(&summary, *observed.Execution)
		}
	}
	out.Execution = &summary
	return out
}
