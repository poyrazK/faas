package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrWorkflowJoinEvaluation = errors.New("workflow join could not be evaluated")

func workflowJoinTarget(snapshot json.RawMessage, name string) *api.WorkflowStepSpec {
	var spec api.WorkflowSpec
	if json.Unmarshal(snapshot, &spec) != nil {
		return nil
	}
	for _, step := range spec.Steps {
		if step.Name == name && step.Join != nil {
			return &step
		}
	}
	return nil
}

type workflowJoinResult struct {
	ready      bool
	output     json.RawMessage
	skipReason *string
}

// workflowInactiveBranch verifies the ancestry rather than trusting a generic
// dependency_skipped label. Ordinary steps can skip before all their other
// dependencies finish, so an unfinished ancestor also keeps the join pending.
// Memoization bounds shared ancestry traversal to one visit per step.
type workflowInactiveBranch struct {
	specs    map[string]api.WorkflowStepSpec
	steps    map[string]WorkflowStep
	visiting map[string]bool
	memo     map[string]workflowBranchState
}

type workflowBranchState struct{ ready, inactive bool }

func (b *workflowInactiveBranch) check(name string) workflowBranchState {
	if value, ok := b.memo[name]; ok {
		return value
	}
	step, exists := b.steps[name]
	spec, defined := b.specs[name]
	result := workflowBranchState{ready: true}
	if !exists || !defined || b.visiting[name] {
		return result // Missing state and cycles cannot authorize a join.
	}
	if step.Status == WorkflowStepStatusSucceeded {
		return workflowBranchState{ready: true, inactive: true}
	}
	if step.Status == WorkflowStepStatusPending || step.Status == WorkflowStepStatusRunning || step.Status == WorkflowStepStatusAwaitingEvent {
		return workflowBranchState{}
	}
	if step.Status != WorkflowStepStatusSkipped || step.SkipReason == nil {
		return result
	}
	guardFalse := *step.SkipReason == WorkflowSkipWhenFalse && spec.When != nil && step.WhenMatched != nil && !*step.WhenMatched
	propagated := *step.SkipReason == WorkflowSkipDependencySkipped
	if !guardFalse && !propagated {
		return result
	}
	b.visiting[name] = true
	allSafe, waiting, skipped := true, false, false
	for _, dependency := range spec.DependsOn {
		child := b.check(dependency)
		waiting = waiting || !child.ready
		allSafe = allSafe && child.inactive
		if source, ok := b.steps[dependency]; ok && source.Status == WorkflowStepStatusSkipped {
			skipped = true
		}
	}
	delete(b.visiting, name)
	result.ready = !waiting
	result.inactive = !waiting && allSafe && (guardFalse || skipped)
	b.memo[name] = result
	return result
}

func evaluateWorkflowJoin(snapshot json.RawMessage, name string, steps map[string]WorkflowStep) (workflowJoinResult, error) {
	var spec api.WorkflowSpec
	if json.Unmarshal(snapshot, &spec) != nil {
		return workflowJoinResult{}, ErrWorkflowJoinEvaluation
	}
	specs := make(map[string]api.WorkflowStepSpec, len(spec.Steps))
	for _, item := range spec.Steps {
		specs[item.Name] = item
	}
	return evaluateWorkflowJoinDefinition(specs, name, steps)
}

// Reuse parsed definitions during bounded simulations without repeatedly
// decoding an entire snapshot for a join that is still waiting on ancestry.
func evaluateWorkflowJoinDefinition(specs map[string]api.WorkflowStepSpec, name string, steps map[string]WorkflowStep) (workflowJoinResult, error) {
	join, ok := specs[name]
	if !ok || join.Join == nil || api.ValidateWorkflowJoinStep(join) != nil {
		return workflowJoinResult{}, ErrWorkflowJoinEvaluation
	}
	// Never select an early finisher: every direct branch must be terminal.
	for _, dependency := range join.DependsOn {
		step, exists := steps[dependency]
		if !exists || step.Status == WorkflowStepStatusPending || step.Status == WorkflowStepStatusRunning || step.Status == WorkflowStepStatusAwaitingEvent {
			return workflowJoinResult{}, nil
		}
	}
	branches := workflowInactiveBranch{specs: specs, steps: steps, visiting: map[string]bool{}, memo: map[string]workflowBranchState{}}
	unsafe, waiting := false, false
	for _, dependency := range join.DependsOn {
		branch := branches.check(dependency)
		waiting = waiting || !branch.ready
		unsafe = unsafe || !branch.inactive
	}
	if waiting {
		return workflowJoinResult{}, nil
	}
	if unsafe {
		reason := WorkflowSkipDependencyFailed
		return workflowJoinResult{ready: true, skipReason: &reason}, nil
	}
	for _, source := range join.Join.OutputFrom {
		step := steps[source]
		if step.Status != WorkflowStepStatusSucceeded {
			continue
		}
		value := step.Output
		if len(value) == 0 {
			value = json.RawMessage("null")
		}
		output, err := json.Marshal(struct {
			Source string          `json:"source"`
			Value  json.RawMessage `json:"value"`
		}{source, value})
		if err != nil {
			return workflowJoinResult{}, ErrWorkflowJoinEvaluation
		}
		return workflowJoinResult{ready: true, output: output}, nil
	}
	reason := WorkflowSkipDependencySkipped
	return workflowJoinResult{ready: true, skipReason: &reason}, nil
}

func (m *MemStore) ResolveWorkflowStepJoin(ctx context.Context, runID, name string) (bool, error) {
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
	if workflowJoinTarget(run.DefinitionSnapshot, name) == nil {
		return false, ErrWorkflowJoinEvaluation
	}
	if step.Status == WorkflowStepStatusSucceeded || step.Status == WorkflowStepStatusSkipped {
		return true, nil
	}
	if step.Status != WorkflowStepStatusPending {
		return false, ErrWorkflowGuardNotReady
	}
	result, err := evaluateWorkflowJoin(run.DefinitionSnapshot, name, m.workflowSteps[runID])
	if err != nil || !result.ready {
		return false, err
	}
	now := time.Now().UTC()
	step.Status, step.Output, step.FinishedAt, step.NextRetryAt = WorkflowStepStatusSucceeded, cloneWorkflowJSON(result.output), &now, nil
	step.SkipReason = cloneWorkflowString(result.skipReason)
	if result.skipReason != nil {
		step.Status = WorkflowStepStatusSkipped
	}
	m.workflowSteps[runID][name] = step
	return true, nil
}

func (s *PgStore) ResolveWorkflowStepJoin(ctx context.Context, runID, name string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("pgstore: begin workflow join: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, step, err := lockWorkflowGuard(ctx, tx, runID, name)
	if err != nil {
		return false, err
	}
	if workflowJoinTarget(run.DefinitionSnapshot, name) == nil {
		return false, ErrWorkflowJoinEvaluation
	}
	if step.Status == WorkflowStepStatusSucceeded || step.Status == WorkflowStepStatusSkipped {
		return true, nil
	}
	if step.Status != WorkflowStepStatusPending {
		return false, ErrWorkflowGuardNotReady
	}
	rows, err := sqlc.New().WorkflowJoinSteps(ctx, tx, mustPgUUID(runID))
	if err != nil {
		return false, fmt.Errorf("pgstore: read workflow join branches: %w", err)
	}
	steps := make(map[string]WorkflowStep, len(rows))
	for _, row := range rows {
		item := WorkflowStep{Status: row.Status, Output: row.Output}
		if row.SkipReason.Valid {
			value := row.SkipReason.String
			item.SkipReason = &value
		}
		if row.WhenMatched.Valid {
			value := row.WhenMatched.Bool
			item.WhenMatched = &value
		}
		steps[row.StepName] = item
	}
	result, err := evaluateWorkflowJoin(run.DefinitionSnapshot, name, steps)
	if err != nil || !result.ready {
		return false, err
	}
	status := WorkflowStepStatusSucceeded
	if result.skipReason != nil {
		status = WorkflowStepStatusSkipped
	}
	var skipReason pgtype.Text
	if result.skipReason != nil {
		skipReason = pgtype.Text{String: *result.skipReason, Valid: true}
	}
	if err := sqlc.New().CompleteWorkflowJoin(ctx, tx, sqlc.CompleteWorkflowJoinParams{
		RunID: mustPgUUID(runID), StepName: name, Status: status, Output: result.output, SkipReason: skipReason,
	}); err != nil {
		return false, fmt.Errorf("pgstore: persist workflow join: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("pgstore: commit workflow join: %w", err)
	}
	return true, nil
}
