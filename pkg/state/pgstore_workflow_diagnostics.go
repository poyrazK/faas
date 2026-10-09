package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func workflowRecoveryTargetFromSQLC(row sqlc.GetWorkflowRecoveryTargetRow) workflowRecoveryTarget {
	return workflowRecoveryTarget{plan: api.Plan(row.Plan), accountActive: row.AccountActive, appDeleted: row.AppDeleted,
		maintenance: row.MaintenanceMode, tenantRequired: row.PlatformTenantRequired, liveDeployment: row.LiveDeployment,
		pinnedDeployment: row.PinnedDeployment, tenantActive: row.TenantActive, activeRuns: int(row.ActiveRuns)}
}

func (s *PgStore) GetWorkflowRunDiagnostics(ctx context.Context, opts WorkflowDiagnosticsOptions) (api.WorkflowRunDiagnosticsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, api.WorkflowRunDiagnosticsReadTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.WorkflowRunDiagnosticsResponse{}, fmt.Errorf("begin workflow diagnostics: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.GetOwnedWorkflowDiagnosticsRun(ctx, tx, sqlc.GetOwnedWorkflowDiagnosticsRunParams{
		RunID: mustPgUUID(opts.RunID), AccountID: mustPgUUID(opts.AccountID), TenantID: mustPgUUID(opts.PlatformTenantID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.WorkflowRunDiagnosticsResponse{}, ErrWorkflowRunNotFound
	}
	if err != nil {
		return api.WorkflowRunDiagnosticsResponse{}, fmt.Errorf("read diagnostic run: %w", err)
	}
	run := workflowRunFromSQLC(row)
	steps, err := q.GetWorkflowDiagnosticsSteps(ctx, tx, row.ID)
	if err != nil {
		return api.WorkflowRunDiagnosticsResponse{}, fmt.Errorf("read diagnostic steps: %w", err)
	}
	stepMap := make(map[string]WorkflowStep, len(steps))
	for _, step := range steps {
		stepMap[step.StepName] = WorkflowStep{StepName: step.StepName, Status: step.Status, Attempt: int(step.Attempt), RetryBase: int(step.RetryBase),
			ForEachParent: workflowResumeTextPtr(step.ForeachParent), ForEachIndex: workflowResumeIntPtr(step.ForeachIndex), ForEachCount: workflowResumeIntPtr(step.ForeachCount),
			SkipReason: workflowResumeTextPtr(step.SkipReason), NextRetryAt: workflowResumeTimePtr(step.NextRetryAt), NextCheckAt: workflowResumeTimePtr(step.NextCheckAt),
		}
	}
	target, err := q.GetWorkflowRecoveryTarget(ctx, tx, sqlc.GetWorkflowRecoveryTargetParams{RunID: row.ID, StaleMs: int64(WorkflowRunStaleAfter / time.Millisecond)})
	if err != nil {
		return api.WorkflowRunDiagnosticsResponse{}, fmt.Errorf("read workflow recovery target: %w", err)
	}
	running, err := q.WorkflowResumeHasRunningAttempts(ctx, tx, row.ID)
	if err != nil {
		return api.WorkflowRunDiagnosticsResponse{}, fmt.Errorf("read recovery attempts: %w", err)
	}
	var spec api.WorkflowSpec
	var outboundErr error
	if json.Unmarshal(run.DefinitionSnapshot, &spec) == nil {
		outboundErr = validateWorkflowOutboundTx(ctx, tx, run.AppID, opts.AccountID, spec)
		if outboundErr != nil && !errors.Is(outboundErr, ErrAutomationInvalid) {
			return api.WorkflowRunDiagnosticsResponse{}, outboundErr
		}
	}
	counts := workflowDispatchOccupancy{
		apps:        map[string]int{run.AppID: int(target.AppRunning)},
		tenants:     map[workflowDispatchScope]int{{run.AppID, run.PlatformTenantID}: int(target.TenantRunning)},
		definitions: map[workflowDefinitionScope]int{{run.AppID, run.WorkflowName}: int(target.WorkflowActive)},
	}
	deadline := run.UpdatedAt.Add(WorkflowRunStaleAfter)
	if row.LeaseUntil.Valid {
		deadline = row.LeaseUntil.Time
	}
	result := buildWorkflowRunDiagnostics(*run, stepMap, deadline, target.ObservedAt.Time, counts, workflowRecoveryTargetFromSQLC(target), running, outboundErr)
	if err := tx.Commit(ctx); err != nil {
		return api.WorkflowRunDiagnosticsResponse{}, fmt.Errorf("finish workflow diagnostics: %w", err)
	}
	return result, nil
}
