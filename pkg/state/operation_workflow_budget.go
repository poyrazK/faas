// adr: 20261016110803
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// workflowOperationUsage sums a run's finished HTTP step attempts. Waits do
// no work, so they never count against the execution budget.
func workflowOperationUsage(snapshot json.RawMessage, attempts []WorkflowStepAttempt) (api.OperationUsage, error) {
	var spec api.WorkflowSpec
	if err := json.Unmarshal(snapshot, &spec); err != nil {
		return api.OperationUsage{}, err
	}
	actions := map[string]bool{}
	for _, step := range spec.Steps {
		actions[step.Name] = operations.WorkflowStepKind(step) == "http"
	}
	usage := api.OperationUsage{}
	for _, attempt := range attempts {
		if !actions[attempt.StepName] || attempt.FinishedAt == nil {
			continue
		}
		usage.ExecutionMS += max(attempt.FinishedAt.Sub(attempt.StartedAt).Milliseconds(), 0)
		usage.Attempts++
	}
	return usage, nil
}

// workflowOperationRemaining is the runtime the current attempt may use: the
// budget less the attempts already finished, or nil without a budget.
func workflowOperationRemaining(maxExecutionSeconds int, usage api.OperationUsage) *time.Duration {
	if maxExecutionSeconds <= 0 {
		return nil
	}
	remaining := time.Duration(int64(maxExecutionSeconds)*1000-usage.ExecutionMS) * time.Millisecond
	return &remaining
}

func workflowOperationUsageTx(ctx context.Context, db sqlc.DBTX, run WorkflowRun) (api.OperationUsage, error) {
	rows, err := sqlc.New().ListFinishedWorkflowRunAttempts(ctx, db, mustPgUUID(run.ID))
	if err != nil {
		return api.OperationUsage{}, err
	}
	attempts := make([]WorkflowStepAttempt, 0, len(rows))
	for _, row := range rows {
		finished := row.FinishedAt.Time
		attempts = append(attempts, WorkflowStepAttempt{StepName: row.StepName, Attempt: int(row.Attempt), StartedAt: row.StartedAt.Time, FinishedAt: &finished})
	}
	return workflowOperationUsage(run.DefinitionSnapshot, attempts)
}

func (m *MemStore) workflowOperationUsageLocked(run WorkflowRun) (api.OperationUsage, error) {
	attempts := []WorkflowStepAttempt{}
	for key, attempt := range m.workflowStepAttempts {
		if key.runID == run.ID {
			attempts = append(attempts, attempt)
		}
	}
	return workflowOperationUsage(run.DefinitionSnapshot, attempts)
}

// refreshWorkflowOperationUsage keeps an Operation with limits reporting the
// usage its recovery check reads. It reports whether the usage changed.
func refreshWorkflowOperationUsage(op *Operation, usage api.OperationUsage) bool {
	if op.Usage == nil || *op.Usage == usage {
		return false
	}
	op.Usage = &usage
	return true
}

// saveOperationRecordTx stores a changed Operation record that has no event
// of its own, such as refreshed usage.
func saveOperationRecordTx(ctx context.Context, tx pgx.Tx, op Operation) error {
	record, err := operationRecordJSON(op)
	if err != nil {
		return err
	}
	id, _ := operationUUID(op.ID)
	if err := sqlc.New().UpdateCustomerOperation(ctx, tx, sqlc.UpdateCustomerOperationParams{ID: id, WorkflowRunID: mustPgUUID(op.WorkflowRunID), JobRunID: mustPgUUID(op.JobRunID), State: string(op.State), Record: record, ExpiresAt: pgtype.Timestamptz{Time: op.ExpiresAt, Valid: true}}); err != nil {
		return fmt.Errorf("state: update operation record: %w", err)
	}
	return nil
}
