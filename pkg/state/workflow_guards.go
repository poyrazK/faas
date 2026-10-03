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

const (
	WorkflowSkipWhenFalse         = "when_false"
	WorkflowSkipDependencySkipped = "dependency_skipped"
	WorkflowSkipDependencyFailed  = "dependency_failed"
	WorkflowSkipRouteNotTaken     = "route_not_taken"
)

var (
	ErrWorkflowGuardNotReady   = errors.New("workflow guard step no longer ready")
	ErrWorkflowGuardEvaluation = errors.New("workflow guard could not be evaluated")
)

func workflowGuardRunActive(status string) bool {
	return status == WorkflowRunStatusPending || status == WorkflowRunStatusRunning || status == WorkflowRunStatusAwaitingEvent
}

func workflowGuardTarget(snapshot json.RawMessage, name string) (api.WorkflowStepSpec, error) {
	var spec api.WorkflowSpec
	if err := json.Unmarshal(snapshot, &spec); err != nil {
		return api.WorkflowStepSpec{}, ErrWorkflowGuardEvaluation
	}
	for _, step := range spec.Steps {
		if step.Name == name && step.When != nil {
			return step, nil
		}
	}
	return api.WorkflowStepSpec{}, ErrWorkflowGuardEvaluation
}

func evaluateWorkflowStepGuard(snapshot, input json.RawMessage, name string, steps map[string]WorkflowStep) (bool, error) {
	spec, err := workflowGuardTarget(snapshot, name)
	if err != nil {
		return false, err
	}
	outputs := make(map[string]json.RawMessage, len(spec.DependsOn))
	for _, dependency := range spec.DependsOn {
		step, ok := steps[dependency]
		if !ok || step.Status != WorkflowStepStatusSucceeded {
			return false, ErrWorkflowGuardNotReady
		}
		outputs[dependency] = step.Output
	}
	matched, err := api.EvaluateWorkflowGuard(spec.When, input, outputs)
	if err != nil {
		// Never return referenced customer values in errors or audit metadata.
		return false, ErrWorkflowGuardEvaluation
	}
	return matched, nil
}

// ResolveWorkflowStepGuard evaluates under the same run lock as cancellation,
// output completion and recovery. The first decision and a false decision's
// skip are committed together; retries never reevaluate it.
func (m *MemStore) ResolveWorkflowStepGuard(ctx context.Context, runID, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return false, ErrWorkflowRunNotFound
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) || !workflowGuardRunActive(run.Status) {
		return false, ErrWorkflowGuardNotReady
	}
	step, ok := m.workflowSteps[runID][name]
	if !ok {
		return false, ErrWorkflowStepNotFound
	}
	if step.WhenMatched != nil && (step.Status == WorkflowStepStatusPending || step.Status == WorkflowStepStatusSkipped) {
		return *step.WhenMatched, nil
	}
	if step.Status != WorkflowStepStatusPending {
		return false, ErrWorkflowGuardNotReady
	}
	matched, err := evaluateWorkflowStepGuard(run.DefinitionSnapshot, run.Input, name, m.workflowSteps[runID])
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	step.WhenMatched, step.WhenEvaluatedAt = &matched, &now
	if !matched {
		reason := WorkflowSkipWhenFalse
		step.Status, step.SkipReason, step.FinishedAt, step.NextRetryAt = WorkflowStepStatusSkipped, &reason, &now, nil
	}
	m.workflowSteps[runID][name] = step
	return matched, nil
}

func validWorkflowSkipReason(reason string) bool {
	return reason == WorkflowSkipDependencySkipped || reason == WorkflowSkipDependencyFailed || reason == WorkflowSkipRouteNotTaken
}

// SkipWorkflowStep cannot replace a dispatched or terminal step. Existing skip
// metadata remains unchanged when a caller repeats the transition.
func (m *MemStore) SkipWorkflowStep(ctx context.Context, runID, name, reason string) error {
	if !validWorkflowSkipReason(reason) {
		return ErrWorkflowInvalidRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return ErrWorkflowRunNotFound
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) || !workflowGuardRunActive(run.Status) {
		return ErrWorkflowGuardNotReady
	}
	step, ok := m.workflowSteps[runID][name]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	if step.Status == WorkflowStepStatusSkipped {
		return nil
	}
	if step.Status != WorkflowStepStatusPending {
		return ErrWorkflowGuardNotReady
	}
	now := time.Now().UTC()
	step.Status, step.SkipReason, step.FinishedAt, step.NextRetryAt = WorkflowStepStatusSkipped, &reason, &now, nil
	m.workflowSteps[runID][name] = step
	return nil
}

func lockWorkflowGuard(ctx context.Context, tx sqlc.DBTX, runID, name string) (sqlc.LockWorkflowGuardRunRow, sqlc.LockWorkflowGuardStepRow, error) {
	q := sqlc.New()
	run, err := q.LockWorkflowGuardRun(ctx, tx, mustPgUUID(runID))
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrWorkflowRunNotFound
	}
	if err != nil {
		return run, sqlc.LockWorkflowGuardStepRow{}, err
	}
	if err := checkWorkflowGenerationTx(ctx, tx, runID); err != nil {
		return run, sqlc.LockWorkflowGuardStepRow{}, err
	}
	if !workflowGuardRunActive(run.Status) {
		return run, sqlc.LockWorkflowGuardStepRow{}, ErrWorkflowGuardNotReady
	}
	step, err := q.LockWorkflowGuardStep(ctx, tx, sqlc.LockWorkflowGuardStepParams{RunID: mustPgUUID(runID), StepName: name})
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrWorkflowStepNotFound
	}
	return run, step, err
}

func (s *PgStore) ResolveWorkflowStepGuard(ctx context.Context, runID, name string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("pgstore: begin workflow guard: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, step, err := lockWorkflowGuard(ctx, tx, runID, name)
	if err != nil {
		return false, err
	}
	if step.WhenMatched.Valid && (step.Status == WorkflowStepStatusPending || step.Status == WorkflowStepStatusSkipped) {
		return step.WhenMatched.Bool, nil
	}
	if step.Status != WorkflowStepStatusPending {
		return false, ErrWorkflowGuardNotReady
	}
	rows, err := sqlc.New().WorkflowGuardOutputs(ctx, tx, mustPgUUID(runID))
	if err != nil {
		return false, fmt.Errorf("pgstore: read workflow guard outputs: %w", err)
	}
	steps := make(map[string]WorkflowStep, len(rows))
	for _, row := range rows {
		steps[row.StepName] = WorkflowStep{Status: row.Status, Output: row.Output}
	}
	matched, err := evaluateWorkflowStepGuard(run.DefinitionSnapshot, run.Input, name, steps)
	if err != nil {
		return false, err
	}
	if err := sqlc.New().RecordWorkflowGuardDecision(ctx, tx, sqlc.RecordWorkflowGuardDecisionParams{RunID: mustPgUUID(runID), StepName: name, Matched: matched}); err != nil {
		return false, fmt.Errorf("pgstore: persist workflow guard: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("pgstore: commit workflow guard: %w", err)
	}
	return matched, nil
}

func (s *PgStore) SkipWorkflowStep(ctx context.Context, runID, name, reason string) error {
	if !validWorkflowSkipReason(reason) {
		return ErrWorkflowInvalidRecord
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgstore: begin workflow skip: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, step, err := lockWorkflowGuard(ctx, tx, runID, name)
	if err != nil {
		return err
	}
	if step.Status == WorkflowStepStatusSkipped {
		return nil
	}
	if step.Status != WorkflowStepStatusPending {
		return ErrWorkflowGuardNotReady
	}
	if err := sqlc.New().SkipPendingWorkflowStep(ctx, tx, sqlc.SkipPendingWorkflowStepParams{RunID: mustPgUUID(runID), StepName: name, Reason: reason}); err != nil {
		return fmt.Errorf("pgstore: persist workflow skip: %w", err)
	}
	return tx.Commit(ctx)
}
