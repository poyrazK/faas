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

func insertWorkflowScheduleOccurrence(ctx context.Context, tx pgx.Tx, cursor *WorkflowScheduleCursor, now time.Time) error {
	row := workflowScheduleOccurrence(cursor, now)
	if row == nil {
		return nil
	}
	err := sqlc.New().InsertWorkflowScheduleOccurrence(ctx, tx, sqlc.InsertWorkflowScheduleOccurrenceParams{
		ID: mustPgUUID(row.ID), AppID: mustPgUUID(row.AppID), TenantID: mustPgUUID(row.PlatformTenantID),
		WorkflowName: row.WorkflowName, DeploymentID: mustPgUUID(row.DeploymentID),
		ScheduledFor: pgtype.Timestamptz{Time: row.ScheduledFor, Valid: true},
		EvaluatedAt:  pgtype.Timestamptz{Time: row.EvaluatedAt, Valid: true}, Status: row.Status, RunID: mustPgUUID(row.RunID),
	})
	if err != nil {
		return fmt.Errorf("state: record workflow schedule occurrence: %w", err)
	}
	return nil
}

func (s *PgStore) ListWorkflowScheduleOccurrences(ctx context.Context, appID, tenantID, before string, limit int) ([]WorkflowScheduleOccurrence, error) {
	if limit <= 0 || limit > api.WorkflowScheduleHistoryPageMax+1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListWorkflowScheduleOccurrences(ctx, s.pool, sqlc.ListWorkflowScheduleOccurrencesParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID), BeforeID: mustPgUUID(before), PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list workflow schedule occurrences: %w", err)
	}
	result := make([]WorkflowScheduleOccurrence, 0, len(rows))
	for _, row := range rows {
		result = append(result, WorkflowScheduleOccurrence{
			ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), PlatformTenantID: pgUUIDString(row.PlatformTenantID),
			WorkflowName: row.WorkflowName, DeploymentID: pgUUIDString(row.DeploymentID), ScheduledFor: timeFromPgtype(row.ScheduledFor),
			EvaluatedAt: timeFromPgtype(row.EvaluatedAt), Status: row.Status, RunID: pgUUIDString(row.RunID),
		})
	}
	return result, nil
}

func (s *PgStore) PruneWorkflowScheduleOccurrences(ctx context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 || limit > api.WorkflowScheduleHistoryPruneBatch {
		return 0, ErrInvalidArgument
	}
	n, err := sqlc.New().PruneWorkflowScheduleOccurrences(ctx, s.pool, sqlc.PruneWorkflowScheduleOccurrencesParams{
		BeforeAt: pgtype.Timestamptz{Time: before, Valid: true}, BatchLimit: int32(limit),
	})
	if err != nil {
		return 0, fmt.Errorf("state: prune workflow schedule occurrences: %w", err)
	}
	return int(n), nil
}

func (s *PgStore) ListFairTenantWorkflowScheduleCandidates(ctx context.Context, owner string, minute time.Time, limit int) ([]WorkflowScheduleCandidate, error) {
	if limit <= 0 || limit > api.WorkflowScheduleBatch {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListFairTenantWorkflowScheduleCandidates(ctx, s.pool, sqlc.ListFairTenantWorkflowScheduleCandidatesParams{
		OwnerNodeID: mustPgUUID(owner), EvaluationMinute: pgtype.Timestamptz{Time: minute, Valid: true}, BatchLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list fair tenant workflow schedules: %w", err)
	}
	result := make([]WorkflowScheduleCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, WorkflowScheduleCandidate{AppID: pgUUIDString(row.AppID),
			PlatformTenantID: pgUUIDString(row.PlatformTenantID), DeploymentID: pgUUIDString(row.DeploymentID), Workflows: cloneWorkflowJSON(row.Workflows)})
	}
	return result, nil
}
