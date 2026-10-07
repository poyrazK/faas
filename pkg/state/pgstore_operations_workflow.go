package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperationForWorkflowRun(ctx context.Context, runID string) (Operation, bool, error) {
	id, err := operationUUID(runID)
	if err != nil {
		return Operation{}, false, err
	}
	raw, err := sqlc.New().GetCustomerOperationForWorkflow(ctx, s.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	op, err := operationPGRecord(raw)
	return op, err == nil, err
}

func operationWorkflowDefinitionTx(ctx context.Context, tx pgx.Tx, def OperationDefinition, plan api.Plan) (api.WorkflowSpec, error) {
	raw, err := sqlc.New().CustomerOperationDeploymentWorkflows(ctx, tx, mustPgUUID(def.DeploymentID))
	if err != nil {
		return api.WorkflowSpec{}, mapErr(err)
	}
	return operationWorkflowDefinition(def, raw, plan)
}

func admitOperationWorkflowTx(ctx context.Context, tx pgx.Tx, op *Operation, inv Invocation, def OperationDefinition, plan api.Plan) error {
	q := sqlc.New()
	target, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(op.AppID))
	if err != nil {
		return mapErr(err)
	}
	if target.MaintenanceMode || target.AbuseHoldAt.Valid || target.AccountStatus != "active" && target.AccountStatus != "past_due" {
		return ErrWorkflowResumeUnavailable
	}
	spec, err := operationWorkflowDefinitionTx(ctx, tx, def, plan)
	if err != nil {
		return err
	}
	active, err := q.CountActiveWorkflowRunsForAdmission(ctx, tx, sqlc.CountActiveWorkflowRunsForAdmissionParams{AppID: mustPgUUID(op.AppID)})
	if err != nil {
		return err
	}
	if int(active) >= plan.WorkflowMaxConcurrentRuns() {
		return NewOperationLimitError("workflow_active_runs", int64(plan.WorkflowMaxConcurrentRuns()), active+1)
	}
	run, steps, err := prepareOperationWorkflow(op, inv, spec)
	if err != nil {
		return err
	}
	if err := q.InsertCustomerOperationWorkflowRun(ctx, tx, sqlc.InsertCustomerOperationWorkflowRunParams{
		ID: mustPgUUID(run.ID), AppID: mustPgUUID(run.AppID), PlatformTenantID: mustPgUUID(run.PlatformTenantID), OperationID: mustPgUUID(op.ID),
		WorkflowName: run.WorkflowName, Input: run.Input, DefinitionSnapshot: run.DefinitionSnapshot, CreatedAt: pgtype.Timestamptz{Time: run.ScheduledFor, Valid: true},
	}); err != nil {
		return err
	}
	for _, step := range steps {
		input := step.Input
		if len(input) == 0 {
			input = []byte(`{}`)
		}
		if err := q.InsertCustomerOperationWorkflowStep(ctx, tx, sqlc.InsertCustomerOperationWorkflowStepParams{RunID: mustPgUUID(run.ID), StepName: step.StepName, Input: input, CreatedAt: pgtype.Timestamptz{Time: step.CreatedAt, Valid: true}}); err != nil {
			return err
		}
	}
	return nil
}

func insertOperationWorkflowExecutionTx(ctx context.Context, tx pgx.Tx, op Operation, run WorkflowRun, now time.Time) error {
	steps, err := workflowControlSteps(ctx, tx, run.ID)
	if err != nil {
		return err
	}
	row := operationWorkflowExecution(op, run, steps, now)
	raw, err := json.Marshal(row)
	if err != nil {
		return err
	}
	q := sqlc.New()
	if rows, err := q.SetCustomerOperationWorkflowIdentity(ctx, tx, sqlc.SetCustomerOperationWorkflowIdentityParams{RunID: mustPgUUID(run.ID), OperationID: mustPgUUID(op.ID)}); err != nil {
		return err
	} else if rows != 1 {
		return ErrConflict
	}
	if rows, err := q.InsertCustomerOperationWorkflowExecution(ctx, tx, sqlc.InsertCustomerOperationWorkflowExecutionParams{OperationID: mustPgUUID(op.ID), Generation: int32(op.Generation), RunID: mustPgUUID(run.ID)}); err != nil {
		return err
	} else if rows != 1 {
		return ErrConflict
	}
	return q.InsertCustomerOperationWorkflowExecutionRecord(ctx, tx, sqlc.InsertCustomerOperationWorkflowExecutionRecordParams{
		OperationID: mustPgUUID(op.ID), Generation: int32(op.Generation), RunID: mustPgUUID(run.ID), ResumeCount: int32(run.ResumeCount), Record: raw, CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
}

// Call with the run locked before the operation. Every projection is committed
// with the execution fact that produced it; a crashed scheduler leaves no
// lost terminal notification or progress gap that depends on a future tick.
func syncOperationWorkflowTx(ctx context.Context, tx pgx.Tx, runID string) error {
	q := sqlc.New()
	raw, err := q.LockCustomerOperationForWorkflow(ctx, tx, mustPgUUID(runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return err
	}
	row, err := q.ReadCustomerOperationWorkflowRun(ctx, tx, mustPgUUID(runID))
	if err != nil {
		return err
	}
	run := workflowRunFromSQLC(row)
	definition, err := q.GetCustomerOperationDefinition(ctx, tx, sqlc.GetCustomerOperationDefinitionParams{ID: mustPgUUID(op.DefinitionID), AccountID: mustPgUUID(op.AccountID)})
	if err != nil {
		return err
	}
	def, err := operationPGDefinition(definition)
	if err != nil {
		return err
	}
	steps, err := workflowControlSteps(ctx, tx, runID)
	if err != nil {
		return err
	}
	events, err := operationWorkflowProjection(&op, def, *run, steps, time.Now().UTC())
	if err != nil {
		return err
	}
	events = append(events, publishWorkflowArtifacts(&op, *run, steps, false, time.Now().UTC())...)
	// Keep the generation's creation time stable through progress updates.
	created := op.CreatedAt
	if run.ResumeCount > 0 {
		receipts, err := q.ListWorkflowResumes(ctx, tx, mustPgUUID(runID))
		if err != nil {
			return err
		}
		for _, receipt := range receipts {
			if int(receipt.ResumeNumber) == run.ResumeCount {
				created = receipt.CreatedAt.Time
			}
		}
	}
	execution, err := json.Marshal(operationWorkflowExecution(op, *run, steps, created))
	if err != nil {
		return err
	}
	if err := q.UpdateCustomerOperationWorkflowExecution(ctx, tx, sqlc.UpdateCustomerOperationWorkflowExecutionParams{OperationID: mustPgUUID(op.ID), Generation: int32(op.Generation), Record: execution}); err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	if op.State.Terminal() {
		if err := operationCompletionTx(ctx, tx, &op, def); err != nil {
			return err
		}
	}
	for _, event := range events {
		if err := operationSaveTx(ctx, tx, op, event); err != nil {
			return err
		}
	}
	return nil
}

func interruptOperationWorkflowTx(ctx context.Context, tx sqlc.DBTX, runID string) error {
	q := sqlc.New()
	id := mustPgUUID(runID)
	if err := q.InterruptCustomerOperationWorkflowSteps(ctx, tx, id); err != nil {
		return err
	}
	return q.CloseCustomerOperationWorkflowAttempts(ctx, tx, id)
}

func (s *PgStore) cancelOperationWorkflow(ctx context.Context, snapshot Operation, generation int) (Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	runID := mustPgUUID(snapshot.WorkflowRunID)
	if _, err := q.LockWorkflowRecovery(ctx, tx, runID); err != nil {
		return Operation{}, mapErr(err)
	}
	raw, err := q.LockCustomerOperationForWorkflow(ctx, tx, runID)
	if err != nil {
		return Operation{}, mapErr(err)
	}
	op, err := operationPGRecord(raw)
	if err != nil {
		return Operation{}, err
	}
	now := time.Now().UTC()
	if err := validateOperationCancellation(op, snapshot.AccountID, snapshot.PlatformTenantID, generation, now); err != nil {
		return Operation{}, err
	}
	if op.State.Terminal() || op.CancellationRequested {
		return op, nil
	}
	row, err := q.ReadCustomerOperationWorkflowRun(ctx, tx, runID)
	if err != nil || int(row.ResumeCount)+1 != op.Generation {
		return Operation{}, ErrOperationStaleAttempt
	}
	op.CancellationRequested = true
	event := operationWorkflowEvent(&op, *workflowRunFromSQLC(row), "cancellation_requested", map[string]any{"cancellation_requested": true}, now)
	if err := operationSaveTx(ctx, tx, op, event); err != nil {
		return Operation{}, err
	}
	if err := interruptOperationWorkflowTx(ctx, tx, snapshot.WorkflowRunID); err != nil {
		return Operation{}, err
	}
	if err := q.SkipCancelledCustomerOperationWorkflowSteps(ctx, tx, runID); err != nil {
		return Operation{}, err
	}
	if err := q.CancelCustomerOperationWorkflow(ctx, tx, runID); err != nil {
		return Operation{}, err
	}
	if err := syncOperationWorkflowTx(ctx, tx, snapshot.WorkflowRunID); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Operation{}, err
	}
	return s.OperationByID(ctx, op.AccountID, op.PlatformTenantID, op.ID)
}
