package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func previewInitialPolicyChangeTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app string, out *api.WorkflowRecoveryPolicyChangePreview, budget *recoveryPolicyBudget, last *time.Time) error {
	if !initialPolicyChangeEnabled(out.Proposed) {
		return nil
	}
	unresolved, err := q.WorkflowInitialAutomaticUnresolved(ctx, tx, mustPgUUID(app))
	if err != nil {
		return fmt.Errorf("read initial preview recovery slot: %w", err)
	}
	active, err := q.EventRecoveryActiveCount(ctx, tx, mustPgUUID(account))
	if err != nil {
		return fmt.Errorf("read initial preview account quota: %w", err)
	}
	retained, err := q.WorkflowInitialReviewRetainedCount(ctx, tx, sqlc.WorkflowInitialReviewRetainedCountParams{AppID: mustPgUUID(app), RetainedAfter: pgtypeFromTime(out.ObservedAt.Add(-api.WorkflowRecoveryHistoryRetention))})
	if err != nil {
		return fmt.Errorf("read initial preview review quota: %w", err)
	}
	pressure := initialPolicyChangePressure{retained: int(retained), active: int(active), unresolved: !unresolved.Valid || unresolved.Bool, hourly: out.HourlyExecutions, last: last}
	out.InitialFailures = newInitialPolicyChangePreview(pressure)
	rows, err := q.WorkflowInitialPolicyChangeFailures(ctx, tx, sqlc.WorkflowInitialPolicyChangeFailuresParams{AppID: mustPgUUID(app), RetainedAfter: pgtypeFromTime(out.ObservedAt.Add(-api.WorkflowRecoveryFailureRetention)), NowAt: pgtypeFromTime(out.ObservedAt), Workflows: out.Proposed.WorkflowNames, Statuses: out.Proposed.FailureStatuses, PageLimit: api.WorkflowRecoveryPolicyJobsPerPoll + 1})
	if err != nil {
		return fmt.Errorf("sample initial policy change failures: %w", err)
	}
	if len(rows) > api.WorkflowRecoveryPolicyJobsPerPoll {
		out.InitialFailures.Truncated = true
		rows = rows[:api.WorkflowRecoveryPolicyJobsPerPoll]
	}
	for _, row := range rows {
		f := api.WorkflowInitialFailure{ID: uuidString(row.ID), AppID: uuidString(row.AppID), RunID: uuidString(row.RunID), Workflow: row.Workflow, FailureStatus: row.FailureStatus, ObservedAt: timeFromPgtype(row.ObservedAt), CompletedAt: timestamptzToTimePtr(row.CompletedAt)}
		preview := api.WorkflowInitialRecoveryPreview{Failure: f, ObservedAt: out.ObservedAt, Blockers: []string{}}
		var pending bool
		if out.Proposed.Enabled && initialPolicyChangeMatches(out.Proposed, f) {
			preview, err = previewWorkflowInitialRecoveryTx(ctx, q, tx, account, app, f.ID, out.ObservedAt)
			if err != nil {
				return fmt.Errorf("inspect initial policy change candidate: %w", err)
			}
			pending, err = q.WorkflowInitialRecoveryPending(ctx, tx, sqlc.WorkflowInitialRecoveryPendingParams{AppID: mustPgUUID(app), RunID: f.RunID})
			if err != nil {
				return fmt.Errorf("inspect initial policy change duplicate: %w", err)
			}
		}
		addInitialPolicyChangeCandidate(out, preview, pending, budget, &pressure)
	}
	return nil
}
