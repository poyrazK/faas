package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func workflowScheduleOccurrenceFromSQL(row sqlc.WorkflowScheduleOccurrence) WorkflowScheduleOccurrence {
	return WorkflowScheduleOccurrence{
		ID: pgUUIDString(row.ID), AppID: pgUUIDString(row.AppID), PlatformTenantID: pgUUIDString(row.PlatformTenantID),
		WorkflowName: row.WorkflowName, DeploymentID: pgUUIDString(row.DeploymentID), ScheduledFor: timeFromPgtype(row.ScheduledFor),
		EvaluatedAt: timeFromPgtype(row.EvaluatedAt), Status: row.Status, RunID: pgUUIDString(row.RunID),
		DefinitionHash: row.DefinitionHash, ReplayRunID: pgUUIDString(row.ReplayRunID), ReplayedAt: timestamptzToTimePtr(row.ReplayedAt),
	}
}

func loadWorkflowScheduleReplayRows(ctx context.Context, db sqlc.DBTX, appID string, ids []string) ([]WorkflowScheduleOccurrence, error) {
	queries := sqlc.New()
	rows := make([]WorkflowScheduleOccurrence, 0, len(ids))
	for _, id := range ids {
		row, err := queries.GetWorkflowScheduleOccurrenceForReplay(ctx, db, sqlc.GetWorkflowScheduleOccurrenceForReplayParams{
			AppID: mustPgUUID(appID), ID: mustPgUUID(id),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			rows = append(rows, WorkflowScheduleOccurrence{ID: id})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("state: read workflow schedule replay occurrence: %w", err)
		}
		rows = append(rows, workflowScheduleOccurrenceFromSQL(row))
	}
	workflowScheduleReplayOrder(rows)
	return rows, nil
}

func (s *PgStore) PreviewWorkflowScheduleReplays(ctx context.Context, appID string, ids []string) ([]WorkflowScheduleReplayResult, error) {
	if err := validateWorkflowScheduleReplaySelection(appID, ids); err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("state: begin workflow schedule replay preview: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := loadWorkflowScheduleReplayRows(ctx, tx, appID, ids)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New()
	active, err := queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, fmt.Errorf("state: count workflow schedule replay quota: %w", err)
	}
	reserved := int64(0)
	reservedNamed := make(map[string]int64)
	results := make([]WorkflowScheduleReplayResult, 0, len(rows))
	for _, row := range rows {
		if row.AppID == "" {
			results = append(results, WorkflowScheduleReplayResult{OccurrenceID: row.ID, Outcome: WorkflowScheduleReplayOccurrenceNotFound})
			continue
		}
		initial := workflowScheduleReplayInitialOutcome(row)
		result := workflowScheduleReplayResult(row, initial)
		if initial != WorkflowScheduleReplayEligible {
			results = append(results, result)
			continue
		}
		target, plan, targetOutcome, err := lockWorkflowScheduleReplayTarget(ctx, tx, appID, row.PlatformTenantID)
		if err != nil {
			return nil, err
		}
		if targetOutcome != WorkflowScheduleReplayEligible {
			result.Outcome = targetOutcome
			results = append(results, result)
			continue
		}
		if pgUUIDString(target.DeploymentID) != row.DeploymentID {
			result.Outcome = WorkflowScheduleReplayDeploymentChanged
			results = append(results, result)
			continue
		}
		if !plan.WorkflowsAllowed() {
			result.Outcome = WorkflowScheduleReplayPlanUnavailable
			results = append(results, result)
			continue
		}
		var scheduleCursor *WorkflowScheduleCursor
		if row.PlatformTenantID != "" {
			stored, getErr := queries.GetTenantWorkflowScheduleCursor(ctx, tx, sqlc.GetTenantWorkflowScheduleCursorParams{
				AppID: mustPgUUID(appID), TenantID: mustPgUUID(row.PlatformTenantID), WorkflowName: row.WorkflowName,
			})
			if getErr == nil {
				cursor := tenantWorkflowScheduleCursorFromSQL(stored)
				scheduleCursor = &cursor
			} else if !errors.Is(getErr, pgx.ErrNoRows) {
				return nil, fmt.Errorf("state: read tenant schedule replay configuration: %w", getErr)
			}
		}
		definition, outcome, err := workflowScheduleReplayDefinition(row, target.Workflows, plan, scheduleCursor)
		if err != nil {
			return nil, err
		}
		if outcome != WorkflowScheduleReplayEligible {
			result.Outcome = outcome
			results = append(results, result)
			continue
		}
		var named int64
		if row.PlatformTenantID != "" {
			named, err = queries.CountActiveTenantWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveTenantWorkflowRunsForAdmissionParams{
				AppID: mustPgUUID(appID), TenantID: mustPgUUID(row.PlatformTenantID), WorkflowName: row.WorkflowName,
			})
		} else {
			named, err = queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{
				AppID: mustPgUUID(appID), WorkflowName: row.WorkflowName,
			})
		}
		if err != nil {
			return nil, fmt.Errorf("state: count workflow schedule replay overlap: %w", err)
		}
		admissionKey := row.PlatformTenantID + "/" + row.WorkflowName
		named += reservedNamed[admissionKey]
		if definition.Trigger.Overlap != "allow" && named > 0 {
			result.Outcome = WorkflowScheduleReplayOverlapActive
			results = append(results, result)
			continue
		}
		if active+reserved >= int64(plan.WorkflowMaxConcurrentRuns()) {
			result.Outcome = WorkflowScheduleReplayQuotaFull
			results = append(results, result)
			continue
		}
		reserved++
		reservedNamed[admissionKey]++
		results = append(results, result)
	}
	return results, nil
}

func (s *PgStore) ReplayWorkflowScheduleOccurrences(ctx context.Context, appID string, ids []string) ([]WorkflowScheduleReplayResult, error) {
	if err := validateWorkflowScheduleReplaySelection(appID, ids); err != nil {
		return nil, err
	}
	rows, err := loadWorkflowScheduleReplayRows(ctx, s.pool, appID, ids)
	if err != nil {
		return nil, err
	}
	results := make([]WorkflowScheduleReplayResult, 0, len(rows))
	for _, selected := range rows {
		if selected.AppID == "" {
			results = append(results, WorkflowScheduleReplayResult{OccurrenceID: selected.ID, Outcome: WorkflowScheduleReplayOccurrenceNotFound})
			continue
		}
		result, err := s.replayWorkflowScheduleOccurrence(ctx, appID, selected.ID)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *PgStore) replayWorkflowScheduleOccurrence(ctx context.Context, appID, occurrenceID string) (WorkflowScheduleReplayResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: begin workflow schedule replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := sqlc.New()
	if err := queries.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: lock workflow schedule replay admission: %w", err)
	}
	stored, err := queries.GetWorkflowScheduleOccurrenceForReplay(ctx, tx, sqlc.GetWorkflowScheduleOccurrenceForReplayParams{
		AppID: mustPgUUID(appID), ID: mustPgUUID(occurrenceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleReplayResult{OccurrenceID: occurrenceID, Outcome: WorkflowScheduleReplayOccurrenceNotFound}, nil
	}
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: read workflow schedule replay occurrence: %w", err)
	}
	row := workflowScheduleOccurrenceFromSQL(stored)
	initial := workflowScheduleReplayInitialOutcome(row)
	result := workflowScheduleReplayResult(row, initial)
	if initial == WorkflowScheduleReplayAlreadyReplayed || initial != WorkflowScheduleReplayEligible {
		return result, nil
	}
	target, plan, targetOutcome, err := lockWorkflowScheduleReplayTarget(ctx, tx, appID, row.PlatformTenantID)
	if err != nil {
		return WorkflowScheduleReplayResult{}, err
	}
	if targetOutcome != WorkflowScheduleReplayEligible {
		result.Outcome = targetOutcome
		return result, nil
	}
	stored, err = queries.LockWorkflowScheduleOccurrenceForReplay(ctx, tx, sqlc.LockWorkflowScheduleOccurrenceForReplayParams{
		AppID: mustPgUUID(appID), ID: mustPgUUID(occurrenceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkflowScheduleReplayResult{OccurrenceID: occurrenceID, Outcome: WorkflowScheduleReplayOccurrenceNotFound}, nil
	}
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: lock workflow schedule replay occurrence: %w", err)
	}
	row = workflowScheduleOccurrenceFromSQL(stored)
	initial = workflowScheduleReplayInitialOutcome(row)
	result = workflowScheduleReplayResult(row, initial)
	if initial == WorkflowScheduleReplayAlreadyReplayed || initial != WorkflowScheduleReplayEligible {
		return result, nil
	}
	deploymentID := pgUUIDString(target.DeploymentID)
	if deploymentID != row.DeploymentID {
		result.Outcome = WorkflowScheduleReplayDeploymentChanged
		return result, nil
	}
	if !plan.WorkflowsAllowed() {
		result.Outcome = WorkflowScheduleReplayPlanUnavailable
		return result, nil
	}
	var scheduleCursor *WorkflowScheduleCursor
	if row.PlatformTenantID != "" {
		storedCursor, getErr := queries.GetTenantWorkflowScheduleCursor(ctx, tx, sqlc.GetTenantWorkflowScheduleCursorParams{
			AppID: mustPgUUID(appID), TenantID: mustPgUUID(row.PlatformTenantID), WorkflowName: row.WorkflowName,
		})
		if getErr == nil {
			cursor := tenantWorkflowScheduleCursorFromSQL(storedCursor)
			scheduleCursor = &cursor
		} else if !errors.Is(getErr, pgx.ErrNoRows) {
			return WorkflowScheduleReplayResult{}, fmt.Errorf("state: read tenant schedule replay configuration: %w", getErr)
		}
	}
	definition, outcome, err := workflowScheduleReplayDefinition(row, target.Workflows, plan, scheduleCursor)
	if err != nil {
		return WorkflowScheduleReplayResult{}, err
	}
	if outcome != WorkflowScheduleReplayEligible {
		result.Outcome = outcome
		return result, nil
	}
	active, err := queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(appID)})
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: count workflow schedule replay quota: %w", err)
	}
	var named int64
	if row.PlatformTenantID != "" {
		named, err = queries.CountActiveTenantWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveTenantWorkflowRunsForAdmissionParams{
			AppID: mustPgUUID(appID), TenantID: mustPgUUID(row.PlatformTenantID), WorkflowName: row.WorkflowName,
		})
	} else {
		named, err = queries.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{
			AppID: mustPgUUID(appID), WorkflowName: row.WorkflowName,
		})
	}
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: count workflow schedule replay overlap: %w", err)
	}
	if definition.Trigger.Overlap != "allow" && named > 0 {
		result.Outcome = WorkflowScheduleReplayOverlapActive
		return result, nil
	}
	if active >= int64(plan.WorkflowMaxConcurrentRuns()) {
		result.Outcome = WorkflowScheduleReplayQuotaFull
		return result, nil
	}
	definitionSnapshot, err := json.Marshal(definition)
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: encode replay workflow definition: %w", err)
	}
	now := time.Now().UTC()
	run := &WorkflowRun{AppID: appID, DeploymentID: deploymentID, PlatformTenantID: row.PlatformTenantID,
		WorkflowName: row.WorkflowName, DefinitionSnapshot: definitionSnapshot, Input: cloneWorkflowJSON(definition.Trigger.Input),
		ScheduledFor: row.ScheduledFor}
	if err := prepareWorkflowRun(run); err != nil {
		return WorkflowScheduleReplayResult{}, err
	}
	if err := insertWorkflowRun(ctx, tx, run); err != nil {
		return WorkflowScheduleReplayResult{}, err
	}
	marked, err := queries.SetWorkflowScheduleOccurrenceReplay(ctx, tx, sqlc.SetWorkflowScheduleOccurrenceReplayParams{
		ReplayRunID: mustPgUUID(run.ID), ReplayedAt: pgtype.Timestamptz{Time: now, Valid: true},
		AppID: mustPgUUID(appID), ID: mustPgUUID(row.ID),
	})
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: mark workflow schedule replay: %w", err)
	}
	if marked != 1 {
		return WorkflowScheduleReplayResult{}, ErrConflict
	}
	admittedAt := now.Truncate(time.Minute)
	if row.PlatformTenantID != "" {
		_, err = queries.UpdateTenantWorkflowScheduleLastAdmittedAt(ctx, tx, sqlc.UpdateTenantWorkflowScheduleLastAdmittedAtParams{
			LastAdmittedAt: pgtype.Timestamptz{Time: admittedAt, Valid: true}, AppID: mustPgUUID(appID),
			TenantID: mustPgUUID(row.PlatformTenantID), WorkflowName: row.WorkflowName,
		})
	} else {
		_, err = queries.UpdateWorkflowScheduleLastAdmittedAt(ctx, tx, sqlc.UpdateWorkflowScheduleLastAdmittedAtParams{
			LastAdmittedAt: pgtype.Timestamptz{Time: admittedAt, Valid: true}, AppID: mustPgUUID(appID), WorkflowName: row.WorkflowName,
		})
	}
	if err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: update workflow schedule replay fairness: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowScheduleReplayResult{}, fmt.Errorf("state: commit workflow schedule replay: %w", err)
	}
	result.Outcome, result.ReplayRunID = WorkflowScheduleReplayReplayed, run.ID
	return result, nil
}

func lockWorkflowScheduleReplayTarget(ctx context.Context, tx pgx.Tx, appID, tenantID string) (sqlc.LockWorkflowScheduleTargetRow, api.Plan, string, error) {
	queries := sqlc.New()
	if tenantID == "" {
		target, err := queries.LockWorkflowScheduleTarget(ctx, tx, mustPgUUID(appID))
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.LockWorkflowScheduleTargetRow{}, "", WorkflowScheduleReplayTargetUnavailable, nil
		}
		if err != nil {
			return sqlc.LockWorkflowScheduleTargetRow{}, "", "", fmt.Errorf("state: resolve workflow schedule replay target: %w", err)
		}
		return target, api.Plan(target.Plan), WorkflowScheduleReplayEligible, nil
	}
	tenantTarget, err := queries.LockTenantWorkflowScheduleTarget(ctx, tx, sqlc.LockTenantWorkflowScheduleTargetParams{
		AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.LockWorkflowScheduleTargetRow{}, "", WorkflowScheduleReplayTenantUnavailable, nil
	}
	if err != nil {
		return sqlc.LockWorkflowScheduleTargetRow{}, "", "", fmt.Errorf("state: resolve tenant workflow schedule replay target: %w", err)
	}
	_, consumerErr := queries.LockTenantWorkflowScheduleConsumerLink(ctx, tx, sqlc.LockTenantWorkflowScheduleConsumerLinkParams{
		AccountID: tenantTarget.AccountID, AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID),
	})
	if consumerErr != nil && !errors.Is(consumerErr, pgx.ErrNoRows) {
		return sqlc.LockWorkflowScheduleTargetRow{}, "", "", fmt.Errorf("state: lock tenant consumer replay link: %w", consumerErr)
	}
	if errors.Is(consumerErr, pgx.ErrNoRows) {
		_, surfaceErr := queries.LockTenantWorkflowScheduleSurfaceLink(ctx, tx, sqlc.LockTenantWorkflowScheduleSurfaceLinkParams{
			AccountID: tenantTarget.AccountID, AppID: mustPgUUID(appID), TenantID: mustPgUUID(tenantID),
		})
		if errors.Is(surfaceErr, pgx.ErrNoRows) {
			return sqlc.LockWorkflowScheduleTargetRow{}, "", WorkflowScheduleReplayTenantUnavailable, nil
		}
		if surfaceErr != nil {
			return sqlc.LockWorkflowScheduleTargetRow{}, "", "", fmt.Errorf("state: lock tenant surface replay link: %w", surfaceErr)
		}
	}
	target := sqlc.LockWorkflowScheduleTargetRow{AccountID: tenantTarget.AccountID, DeploymentID: tenantTarget.DeploymentID,
		Workflows: tenantTarget.Workflows, Plan: tenantTarget.Plan}
	return target, api.Plan(tenantTarget.Plan), WorkflowScheduleReplayEligible, nil
}

var _ WorkflowScheduleReplayStore = (*PgStore)(nil)
