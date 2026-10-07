package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListWorkflowScheduleCandidates(ctx context.Context, owner, after string, limit int) ([]WorkflowScheduleCandidate, error) {
	rows, err := sqlc.New().ListWorkflowScheduleCandidates(ctx, s.pool, sqlc.ListWorkflowScheduleCandidatesParams{
		AfterAppID: mustPgUUID(after), OwnerNodeID: mustPgUUID(owner), BatchLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list workflow schedules: %w", err)
	}
	result := make([]WorkflowScheduleCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, WorkflowScheduleCandidate{AppID: uuidFromPgtype(row.AppID).String(),
			DeploymentID: uuidFromPgtype(row.DeploymentID).String(), Workflows: cloneWorkflowJSON(row.Workflows)})
	}
	return result, nil
}

func workflowScheduleCursorFromSQL(row sqlc.WorkflowScheduleCursor) WorkflowScheduleCursor {
	result := WorkflowScheduleCursor{AppID: uuidFromPgtype(row.AppID).String(), WorkflowName: row.WorkflowName,
		TriggerSnapshot: cloneWorkflowJSON(row.TriggerSnapshot), LastEvaluatedAt: timeFromPgtype(row.LastEvaluatedAt),
		ScheduledFor: timestamptzToTimePtr(row.ScheduledFor), Status: row.Status}
	if row.DeploymentID.Valid {
		result.DeploymentID = uuidFromPgtype(row.DeploymentID).String()
	}
	if row.LastRunID.Valid {
		result.LastRunID = uuidFromPgtype(row.LastRunID).String()
	}
	return result
}

func (s *PgStore) ListWorkflowScheduleCursors(ctx context.Context, appID string) ([]WorkflowScheduleCursor, error) {
	rows, err := sqlc.New().ListWorkflowScheduleCursors(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, fmt.Errorf("state: list workflow schedule cursors: %w", err)
	}
	result := make([]WorkflowScheduleCursor, 0, len(rows))
	for _, row := range rows {
		result = append(result, workflowScheduleCursorFromSQL(row))
	}
	return result, nil
}

func (s *PgStore) AdmitScheduledWorkflow(ctx context.Context, appID, deploymentID, name string, now time.Time) (WorkflowScheduleCursor, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: begin scheduled workflow: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New()
	if err := queries.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: lock scheduled workflow: %w", err)
	}
	target, err := queries.LockWorkflowScheduleTarget(ctx, tx, mustPgUUID(appID))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleCursor{}, false, nil
	}
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: resolve scheduled workflow target: %w", err)
	}
	plan := api.Plan(target.Plan)
	if uuidFromPgtype(target.DeploymentID).String() != deploymentID || !plan.WorkflowsAllowed() {
		return WorkflowScheduleCursor{}, false, nil
	}
	definition, err := scheduledWorkflowDefinition(target.Workflows, name, plan)
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	if err := queries.PruneWorkflowScheduleCursors(ctx, tx, sqlc.PruneWorkflowScheduleCursorsParams{AppID: mustPgUUID(appID), Workflows: target.Workflows}); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: prune retired workflow schedules: %w", err)
	}
	if definition == nil {
		if err := tx.Commit(ctx); err != nil {
			return WorkflowScheduleCursor{}, false, err
		}
		return WorkflowScheduleCursor{}, false, nil
	}
	previous, err := queries.GetWorkflowScheduleCursor(ctx, tx, sqlc.GetWorkflowScheduleCursorParams{AppID: mustPgUUID(appID), WorkflowName: name})
	var cursor *WorkflowScheduleCursor
	if err == nil {
		stored := workflowScheduleCursorFromSQL(previous)
		cursor = &stored
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: get workflow schedule cursor: %w", err)
	}
	active, err := queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID)})
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	named, err := queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID), WorkflowName: name})
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	next, run, err := evaluateWorkflowSchedule(appID, "", deploymentID, *definition, cursor, now, int(active), int(named), plan.WorkflowMaxConcurrentRuns())
	if err != nil || next == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	if run != nil {
		_, err = queries.InsertScheduledWorkflowRun(ctx, tx, sqlc.InsertScheduledWorkflowRunParams{
			ID: mustPgUUID(run.ID), AppID: mustPgUUID(appID), WorkflowName: name, Input: run.Input,
			DefinitionSnapshot: run.DefinitionSnapshot, ScheduledFor: pgtype.Timestamptz{Time: run.ScheduledFor, Valid: true},
		})
		if err != nil {
			return WorkflowScheduleCursor{}, false, fmt.Errorf("state: insert scheduled workflow run: %w", err)
		}
	}
	_, err = queries.UpsertWorkflowScheduleCursor(ctx, tx, sqlc.UpsertWorkflowScheduleCursorParams{
		AppID: mustPgUUID(appID), WorkflowName: name, DeploymentID: mustPgUUID(deploymentID),
		TriggerSnapshot: next.TriggerSnapshot, LastEvaluatedAt: pgtype.Timestamptz{Time: next.LastEvaluatedAt, Valid: true},
		ScheduledFor: nullableTimestamptzPtr(next.ScheduledFor), Status: next.Status, LastRunID: mustPgUUID(next.LastRunID),
	})
	if err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: record scheduled workflow outcome: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowScheduleCursor{}, false, fmt.Errorf("state: commit scheduled workflow: %w", err)
	}
	return *next, true, nil
}
