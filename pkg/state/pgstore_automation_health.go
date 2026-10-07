package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ WorkflowAutomationHealthStore = (*PgStore)(nil)

func (s *PgStore) GetWorkflowAutomationHealth(ctx context.Context, appID, name string, after, before time.Time) (WorkflowAutomationHealth, error) {
	if appID == "" || name == "" || after.After(before) || before.Sub(after) > api.WorkflowAutomationHealthMaxRange {
		return WorkflowAutomationHealth{}, ErrWorkflowInvalidCreatedRange
	}
	params := sqlc.GetWorkflowAutomationHealthSummaryParams{
		AppID: mustPgUUID(appID), WorkflowName: name,
		CreatedAfter:  pgtype.Timestamptz{Time: after, Valid: true},
		CreatedBefore: pgtype.Timestamptz{Time: before, Valid: true},
	}
	q := sqlc.New()
	row, err := q.GetWorkflowAutomationHealthSummary(ctx, s.pool, params)
	if err != nil {
		return WorkflowAutomationHealth{}, fmt.Errorf("pgstore: summarize automation health: %w", err)
	}
	health := emptyWorkflowAutomationHealth()
	health.RunCount = row.RunCount
	health.StatusCounts[WorkflowRunStatusPending] = row.PendingRuns
	health.StatusCounts[WorkflowRunStatusRunning] = row.RunningRuns
	health.StatusCounts[WorkflowRunStatusAwaitingEvent] = row.AwaitingEventRuns
	health.StatusCounts[WorkflowRunStatusSucceeded] = row.SucceededRuns
	health.StatusCounts[WorkflowRunStatusFailed] = row.FailedRuns
	health.StatusCounts[WorkflowRunStatusDead] = row.DeadRuns
	health.CompletedRunCount = row.SucceededRuns + row.FailedRuns + row.DeadRuns
	if err := s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE status IN ('running', 'awaiting_event') OR (status = 'pending' AND started_at IS NOT NULL)),
			count(*) FILTER (WHERE status = 'pending' AND started_at IS NULL)
		FROM workflow_runs
		WHERE app_id = $1 AND workflow_name = $2
	`, mustPgUUID(appID), name).Scan(&health.ActiveRunCount, &health.QueuedRunCount); err != nil {
		return WorkflowAutomationHealth{}, fmt.Errorf("pgstore: count automation queue: %w", err)
	}
	if row.DurationSamples > 0 {
		p50, p95 := row.P50DurationMs, row.P95DurationMs
		health.P50DurationMS, health.P95DurationMS = &p50, &p95
	}
	if err := s.loadAutomationHealthRecentRuns(ctx, q, params, &health); err != nil {
		return WorkflowAutomationHealth{}, err
	}
	if err := s.loadAutomationHealthFailedSteps(ctx, q, params, &health); err != nil {
		return WorkflowAutomationHealth{}, err
	}
	return health, nil
}

func (s *PgStore) loadAutomationHealthRecentRuns(ctx context.Context, q *sqlc.Queries, params sqlc.GetWorkflowAutomationHealthSummaryParams, health *WorkflowAutomationHealth) error {
	rows, err := q.ListWorkflowAutomationHealthRecentRuns(ctx, s.pool, sqlc.ListWorkflowAutomationHealthRecentRunsParams(params))
	if err != nil {
		return fmt.Errorf("pgstore: load automation health recent runs: %w", err)
	}
	for _, row := range rows {
		summary := &WorkflowAutomationRunSummary{ID: pgUUIDString(row.ID), Status: row.Status, CreatedAt: row.CreatedAt.Time}
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

func (s *PgStore) loadAutomationHealthFailedSteps(ctx context.Context, q *sqlc.Queries, params sqlc.GetWorkflowAutomationHealthSummaryParams, health *WorkflowAutomationHealth) error {
	rows, err := q.ListWorkflowAutomationHealthFailedSteps(ctx, s.pool, sqlc.ListWorkflowAutomationHealthFailedStepsParams{
		AppID: params.AppID, WorkflowName: params.WorkflowName, CreatedAfter: params.CreatedAfter,
		CreatedBefore: params.CreatedBefore, MaxSteps: api.WorkflowAutomationHealthMaxFailureSteps,
	})
	if err != nil {
		return fmt.Errorf("pgstore: load automation health failed steps: %w", err)
	}
	for _, row := range rows {
		health.FailedSteps = append(health.FailedSteps, WorkflowAutomationStepFailure{
			StepName: row.StepName, FailedRunCount: row.FailedRunCount, LastFailedAt: row.LastFailedAt.Time,
		})
	}
	return nil
}
