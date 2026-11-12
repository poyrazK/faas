package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ StoredWorkflowHealthStore = (*PgStore)(nil)

func (s *PgStore) GetStoredWorkflowHealth(ctx context.Context, appID, name string, after, before time.Time) (StoredWorkflowHealth, error) {
	if appID == "" || name == "" || after.After(before) || before.Sub(after) > api.StoredWorkflowHealthMaxRange {
		return StoredWorkflowHealth{}, ErrWorkflowInvalidCreatedRange
	}
	ctx, cancel := context.WithTimeout(ctx, api.StoredWorkflowHealthReadTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return StoredWorkflowHealth{}, fmt.Errorf("pgstore: begin stored workflow health snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	params := sqlc.GetStoredWorkflowHealthSummaryParams{
		AppID: mustPgUUID(appID), WorkflowName: name,
		CreatedAfter:  pgtype.Timestamptz{Time: after, Valid: true},
		CreatedBefore: pgtype.Timestamptz{Time: before, Valid: true},
	}
	q := sqlc.New()
	row, err := q.GetStoredWorkflowHealthSummary(ctx, tx, params)
	if err != nil {
		return StoredWorkflowHealth{}, fmt.Errorf("pgstore: summarize stored workflow health: %w", err)
	}
	health := emptyStoredWorkflowHealth()
	health.RunCount = row.RunCount
	health.StatusCounts[WorkflowRunStatusPending] = row.PendingRuns
	health.StatusCounts[WorkflowRunStatusRunning] = row.RunningRuns
	health.StatusCounts[WorkflowRunStatusAwaitingEvent] = row.AwaitingEventRuns
	health.StatusCounts[WorkflowRunStatusSucceeded] = row.SucceededRuns
	health.StatusCounts[WorkflowRunStatusFailed] = row.FailedRuns
	health.StatusCounts[WorkflowRunStatusDead] = row.DeadRuns
	health.CompletedRunCount = row.SucceededRuns + row.FailedRuns + row.DeadRuns
	queue, err := q.GetStoredWorkflowQueueHealth(ctx, tx, sqlc.GetStoredWorkflowQueueHealthParams{
		AppID: params.AppID, WorkflowName: name, StaleMs: int64(WorkflowRunStaleAfter / time.Millisecond),
		AppLimit: api.WorkflowDispatchMaxPerApp, TenantLimit: api.WorkflowDispatchMaxPerTenant,
	})
	if err != nil {
		return StoredWorkflowHealth{}, fmt.Errorf("pgstore: diagnose stored workflow queue: %w", err)
	}
	health.ActiveRunCount, health.QueuedRunCount = queue.ActiveRunCount, queue.QueuedRunCount
	health.Queue = storedWorkflowQueueHealthFromSQLC(queue)
	if row.DurationSamples > 0 {
		p50, p95 := row.P50DurationMs, row.P95DurationMs
		health.P50DurationMS, health.P95DurationMS = &p50, &p95
	}
	if err := s.loadStoredWorkflowHealthRecentRuns(ctx, tx, q, params, &health); err != nil {
		return StoredWorkflowHealth{}, err
	}
	if err := s.loadStoredWorkflowHealthFailedSteps(ctx, tx, q, params, &health); err != nil {
		return StoredWorkflowHealth{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return StoredWorkflowHealth{}, fmt.Errorf("pgstore: finish stored workflow health snapshot: %w", err)
	}
	return health, nil
}

func (s *PgStore) loadStoredWorkflowHealthRecentRuns(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, params sqlc.GetStoredWorkflowHealthSummaryParams, health *StoredWorkflowHealth) error {
	rows, err := q.ListStoredWorkflowHealthRecentRuns(ctx, db, sqlc.ListStoredWorkflowHealthRecentRunsParams(params))
	if err != nil {
		return fmt.Errorf("pgstore: load stored workflow health recent runs: %w", err)
	}
	for _, row := range rows {
		summary := &StoredWorkflowRunSummary{ID: pgUUIDString(row.ID), Status: row.Status, CreatedAt: row.CreatedAt.Time}
		if row.FinishedAt.Valid {
			finished := row.FinishedAt.Time
			summary.FinishedAt = &finished
		}
		switch row.Kind {
		case "latest":
			health.LastRun = summary
		case "success":
			health.LastSuccess = summary
		case "failure":
			health.LastFailure = summary
		}
	}
	return nil
}

func (s *PgStore) loadStoredWorkflowHealthFailedSteps(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, params sqlc.GetStoredWorkflowHealthSummaryParams, health *StoredWorkflowHealth) error {
	rows, err := q.ListStoredWorkflowHealthFailedSteps(ctx, db, sqlc.ListStoredWorkflowHealthFailedStepsParams{
		AppID: params.AppID, WorkflowName: params.WorkflowName, CreatedAfter: params.CreatedAfter,
		CreatedBefore: params.CreatedBefore, MaxSteps: api.StoredWorkflowHealthMaxFailureSteps,
	})
	if err != nil {
		return fmt.Errorf("pgstore: load stored workflow health failed steps: %w", err)
	}
	for _, row := range rows {
		health.FailedSteps = append(health.FailedSteps, StoredWorkflowStepFailure{
			StepName: row.StepName, FailedRunCount: row.FailedRunCount, LastFailedAt: row.LastFailedAt.Time,
		})
	}
	return nil
}

func storedWorkflowQueueHealthFromSQLC(row sqlc.GetStoredWorkflowQueueHealthRow) *api.StoredWorkflowQueueHealth {
	result := emptyStoredWorkflowQueueHealth(row.ObservedAt.Time)
	result.WaitingRunCount, result.DueRunCount, result.StaleRunCount = row.WaitingRunCount, row.DueRunCount, row.StaleRunCount
	result.OldestDueAgeSeconds = row.OldestDueAgeSeconds
	result.AppRunningCount = row.AppRunningCount
	result.AppAtCapacity = row.AppRunningCount >= int64(result.AppDispatchLimit)
	result.ReasonCounts[api.StoredWorkflowQueueReady] = row.Ready
	result.ReasonCounts[api.StoredWorkflowQueueScheduled] = row.Scheduled
	result.ReasonCounts[api.StoredWorkflowQueueRetryBackoff] = row.RetryBackoff
	result.ReasonCounts[api.StoredWorkflowQueueParkedWait] = row.ParkedWait
	result.ReasonCounts[api.StoredWorkflowQueueAppCapacity] = row.AppCapacity
	result.ReasonCounts[api.StoredWorkflowQueueTenantCapacity] = row.TenantCapacity
	result.ReasonCounts[api.StoredWorkflowQueueWorkflowCapacity] = row.WorkflowCapacity
	return result
}
