package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ResumeWorkflowRun(ctx context.Context, opts WorkflowResumeOptions) (*WorkflowRun, *WorkflowResume, int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("begin workflow resume: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, opts.AppID); err != nil {
		return nil, nil, 0, err
	}
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(opts.AppID))
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(target.AccountID) != opts.AccountID {
		return nil, nil, 0, ErrWorkflowRunNotFound
	}
	if err != nil {
		return nil, nil, 0, err
	}
	plan := api.Plan(target.Plan)
	if !plan.WorkflowsAllowed() || (target.AccountStatus != "active" && target.AccountStatus != "past_due") || target.AbuseHoldAt.Valid || target.AppStatus == string(AppDeleted) || target.MaintenanceMode {
		return nil, nil, 0, ErrWorkflowResumeUnavailable
	}
	if _, err := q.LockWorkflowResumeTarget(ctx, tx, mustPgUUID(opts.AppID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, 0, ErrWorkflowResumeUnavailable
		}
		return nil, nil, 0, err
	}
	row, err := q.LockWorkflowResumeRun(ctx, tx, sqlc.LockWorkflowResumeRunParams{RunID: mustPgUUID(opts.RunID), AppID: mustPgUUID(opts.AppID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, 0, ErrWorkflowRunNotFound
	}
	if err != nil {
		return nil, nil, 0, err
	}
	platformTenantID := pgUUIDString(row.PlatformTenantID)
	if opts.PlatformTenantID != "" && platformTenantID != opts.PlatformTenantID {
		return nil, nil, 0, ErrWorkflowRunNotFound
	}
	if target.PlatformTenantRequired && platformTenantID == "" {
		return nil, nil, 0, ErrWorkflowResumeUnavailable
	}
	run := WorkflowRun{ID: opts.RunID, AppID: opts.AppID, PlatformTenantID: pgUUIDString(row.PlatformTenantID), WorkflowName: row.WorkflowName, Status: row.Status, CurrentStep: workflowResumeTextPtr(row.CurrentStep), Input: row.Input, Output: row.Output, DefinitionSnapshot: row.DefinitionSnapshot, ScheduledFor: row.ScheduledFor.Time, StartedAt: workflowResumeTimePtr(row.StartedAt), FinishedAt: workflowResumeTimePtr(row.FinishedAt), LastError: workflowResumeTextPtr(row.LastError), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time, ResumeCount: int(row.ResumeCount), CancelledAt: workflowResumeTimePtr(row.CancelledAt)}
	rows, err := q.WorkflowResumeSteps(ctx, tx, mustPgUUID(run.ID))
	if err != nil {
		return nil, nil, 0, err
	}
	steps := make(map[string]WorkflowStep, len(rows))
	for _, step := range rows {
		steps[step.StepName] = WorkflowStep{RunID: run.ID, StepName: step.StepName, Status: step.Status, Attempt: int(step.Attempt), Input: step.Input, Output: step.Output, SkipReason: workflowResumeTextPtr(step.SkipReason), ForEachParent: workflowResumeTextPtr(step.ForeachParent), ForEachIndex: workflowResumeIntPtr(step.ForeachIndex), ForEachCount: workflowResumeIntPtr(step.ForeachCount), RetryBase: int(step.RetryBase)}
	}
	spec, names, err := workflowResumePlan(run, steps, opts.ExpectedResumeCount, plan)
	if err != nil {
		return nil, nil, 0, err
	}
	running, err := q.WorkflowResumeHasRunningAttempts(ctx, tx, mustPgUUID(run.ID))
	if err != nil {
		return nil, nil, 0, err
	}
	if running {
		return nil, nil, 0, ErrWorkflowResumeUnsafe
	}
	if err := validateWorkflowOutboundTx(ctx, tx, opts.AppID, opts.AccountID, spec); err != nil {
		if errors.Is(err, ErrAutomationInvalid) {
			return nil, nil, 0, ErrWorkflowResumeUnavailable
		}
		return nil, nil, 0, err
	}
	active, err := q.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(opts.AppID)})
	if err != nil {
		return nil, nil, 0, err
	}
	if int(active) >= plan.WorkflowMaxConcurrentRuns() {
		return nil, nil, int(active), ErrWorkflowRunQuotaExceeded
	}
	record := WorkflowResume{RunID: run.ID, ResumeNumber: run.ResumeCount + 1, AccountID: opts.AccountID, PreviousStatus: run.Status, PreviousError: run.LastError, ResumedSteps: names}
	for _, name := range names {
		if err := q.ResetWorkflowResumeStep(ctx, tx, sqlc.ResetWorkflowResumeStepParams{RunID: mustPgUUID(run.ID), StepName: name}); err != nil {
			return nil, nil, 0, err
		}
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return nil, nil, 0, err
	}
	created, err := q.InsertWorkflowResume(ctx, tx, sqlc.InsertWorkflowResumeParams{RunID: mustPgUUID(run.ID), ResumeNumber: int32(record.ResumeNumber), AccountID: mustPgUUID(opts.AccountID), PreviousStatus: run.Status, PreviousError: pgtype.Text{String: row.LastError.String, Valid: row.LastError.Valid}, ResumedSteps: encoded})
	if err != nil {
		return nil, nil, 0, err
	}
	record.CreatedAt = created.Time
	if err := q.EnqueueWorkflowResume(ctx, tx, mustPgUUID(run.ID)); err != nil {
		return nil, nil, 0, err
	}
	queued, err := q.LockWorkflowResumeRun(ctx, tx, sqlc.LockWorkflowResumeRunParams{RunID: mustPgUUID(run.ID), AppID: mustPgUUID(run.AppID)})
	if err != nil {
		return nil, nil, 0, err
	}
	run.Status, run.ResumeCount, run.ScheduledFor, run.UpdatedAt = WorkflowRunStatusPending, record.ResumeNumber, queued.ScheduledFor.Time, queued.UpdatedAt.Time
	run.FinishedAt, run.LastError, run.Output, run.CurrentStep = nil, nil, nil, nil
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, 0, fmt.Errorf("commit workflow resume: %w", err)
	}
	return &run, &record, int(active), nil
}
