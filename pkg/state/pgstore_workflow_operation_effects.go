package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ManagedWorkflowStepCommitter = (*PgStore)(nil)

func (s *PgStore) CommitManagedWorkflowStep(ctx context.Context, input ManagedWorkflowStepCommit) error {
	commit, err := prepareManagedWorkflowStepCommit(input)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin managed workflow step commit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	q := sqlc.New()

	// Preserve the workflow-wide lock order used by cancel, callback, and wait
	// transitions: run first, then step and attempt.
	run, err := q.ReadManagedWorkflowRunForUpdate(ctx, tx, commit.RunID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkflowRunNotFound
	}
	if err != nil {
		return fmt.Errorf("pgstore: lock managed workflow run: %w", err)
	}
	if run.Status != WorkflowRunStatusRunning {
		return ErrWorkflowNotRunning
	}
	step, err := q.ReadManagedWorkflowStepForUpdate(ctx, tx, sqlc.ReadManagedWorkflowStepForUpdateParams{
		RunID: commit.RunID, StepName: commit.StepName,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkflowStepNotFound
	}
	if err != nil {
		return fmt.Errorf("pgstore: lock managed workflow step: %w", err)
	}
	if step.Status != WorkflowStepStatusRunning || step.Attempt != int32(commit.Attempt) {
		return workflowEffectConflict("workflow step attempt is no longer active")
	}

	if len(commit.Effects) > 0 {
		appScope, err := q.ManagedWorkflowEffectAppScope(ctx, tx, run.AppID)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && appScope.AccountStatus != string(AccountActive)) {
			return ErrOperationEffectDestination
		}
		if err != nil {
			return fmt.Errorf("pgstore: validate workflow effect app scope: %w", err)
		}
		generation := int64(commit.Attempt)
		for _, effect := range commit.Effects {
			hookID, err := q.ResolveExclusiveWebhookEffectTarget(ctx, tx, sqlc.ResolveExclusiveWebhookEffectTargetParams{
				WebhookID: effect.WebhookID, AccountID: appScope.AccountID, TenantID: "", AppID: run.AppID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrOperationEffectDestination
			}
			if err != nil {
				return fmt.Errorf("pgstore: resolve workflow effect webhook: %w", err)
			}
			body, err := workflowOperationEffectBody(commit.OperationID, run.AppID, generation, effect)
			if err != nil {
				return fmt.Errorf("pgstore: encode workflow effect: %w", err)
			}
			id := workflowOperationEffectID(commit.OperationID, effect.Name)
			if err := q.EnqueueExclusiveWebhookEffect(ctx, tx, sqlc.EnqueueExclusiveWebhookEffectParams{
				ID: id, WebhookID: hookID, AppID: run.AppID, AccountID: appScope.AccountID, Payload: body,
			}); err != nil {
				return fmt.Errorf("pgstore: enqueue workflow effect: %w", err)
			}
			if err := q.InsertWorkflowOperationEffect(ctx, tx, sqlc.InsertWorkflowOperationEffectParams{
				ID: id, AccountID: appScope.AccountID, AppID: run.AppID, RunID: commit.RunID,
				StepName: commit.StepName, OperationID: commit.OperationID, Generation: generation,
				Name: effect.Name, Payload: effect.Payload, WebhookID: hookID, EventType: effect.Type,
			}); err != nil {
				return fmt.Errorf("pgstore: record workflow effect: %w", err)
			}
		}
	}

	stepRows, err := q.CompleteManagedWorkflowStep(ctx, tx, sqlc.CompleteManagedWorkflowStepParams{
		Output: commit.Output, RunID: commit.RunID, StepName: commit.StepName, Attempt: int32(commit.Attempt),
	})
	if err != nil {
		return fmt.Errorf("pgstore: complete managed workflow step: %w", err)
	}
	if stepRows != 1 {
		return workflowEffectConflict("workflow step changed during managed completion")
	}
	attemptRows, err := q.CompleteManagedWorkflowAttempt(ctx, tx, sqlc.CompleteManagedWorkflowAttemptParams{
		HttpStatus: int32(commit.HTTPStatus), RunID: commit.RunID, StepName: commit.StepName, Attempt: int32(commit.Attempt),
	})
	if err != nil {
		return fmt.Errorf("pgstore: complete managed workflow attempt: %w", err)
	}
	if attemptRows != 1 {
		return ErrWorkflowAttemptNotFound
	}
	runRows, err := q.CompleteManagedWorkflowRun(ctx, tx, sqlc.CompleteManagedWorkflowRunParams{
		RunID: commit.RunID, StepName: commit.StepName,
	})
	if err != nil {
		return fmt.Errorf("pgstore: update managed workflow run: %w", err)
	}
	if runRows != 1 {
		return ErrWorkflowNotRunning
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pgstore: commit managed workflow step: %w", err)
	}
	return nil
}
