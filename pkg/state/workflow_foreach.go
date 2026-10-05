package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	ErrWorkflowForEachEvaluation  = errors.New("for_each source or inputs are invalid or exceed limits")
	ErrWorkflowForEachOutputLimit = errors.New("for_each collected output exceeds the limit")
)

type WorkflowForEachOutcome struct {
	Item     *WorkflowStep
	Complete bool
}

func workflowForEachOutputs(steps map[string]WorkflowStep, parent string, count int, proposedName string, proposed json.RawMessage) (json.RawMessage, error) {
	values := make([]json.RawMessage, 0, count)
	for index := range count {
		step, ok := steps[api.WorkflowForEachItemName(parent, index)]
		if !ok || (step.Status != WorkflowStepStatusSucceeded && step.StepName != proposedName) {
			break
		}
		value := step.Output
		if step.StepName == proposedName {
			value = proposed
		}
		if len(value) == 0 {
			value = json.RawMessage("null")
		}
		values = append(values, value)
	}
	output, err := json.Marshal(values)
	if err != nil || int64(len(output)) > api.WorkflowForEachMaxOutputBytes {
		return nil, ErrWorkflowForEachOutputLimit
	}
	return output, nil
}

func workflowForEachDefinition(run WorkflowRun, parent WorkflowStep, steps map[string]WorkflowStep) (*api.WorkflowStepSpec, error) {
	spec := api.WorkflowRuntimeStep(run.DefinitionSnapshot, parent.StepName)
	if spec == nil || spec.ForEach == nil || parent.ForEachParent != nil {
		return nil, ErrWorkflowForEachEvaluation
	}
	if parent.Status != WorkflowStepStatusPending {
		return nil, ErrWorkflowGuardNotReady
	}
	if spec.When != nil && (parent.WhenMatched == nil || !*parent.WhenMatched) {
		return nil, ErrWorkflowGuardNotReady
	}
	for _, name := range spec.DependsOn {
		if child, ok := steps[name]; !ok || child.Status != WorkflowStepStatusSucceeded {
			return nil, ErrWorkflowGuardNotReady
		}
	}
	return spec, nil
}

func workflowForEachInputs(run WorkflowRun, spec api.WorkflowStepSpec, steps map[string]WorkflowStep) (json.RawMessage, []json.RawMessage, error) {
	outputs := make(map[string]json.RawMessage, len(spec.DependsOn))
	for _, name := range spec.DependsOn {
		outputs[name] = steps[name].Output
	}
	items, inputs, err := api.ResolveWorkflowForEachInputs(spec, run.Input, outputs)
	if err != nil {
		return nil, nil, ErrWorkflowForEachEvaluation
	}
	for index := range inputs {
		if _, exists := steps[api.WorkflowForEachItemName(spec.Name, index)]; exists {
			return nil, nil, ErrWorkflowForEachEvaluation
		}
	}
	return items, inputs, nil
}

func workflowForEachNext(parent WorkflowStep, steps map[string]WorkflowStep) (WorkflowForEachOutcome, string, json.RawMessage, *string, error) {
	if parent.ForEachCount == nil {
		return WorkflowForEachOutcome{}, "", nil, nil, ErrWorkflowForEachEvaluation
	}
	for index := range *parent.ForEachCount {
		item, ok := steps[api.WorkflowForEachItemName(parent.StepName, index)]
		if !ok || item.ForEachParent == nil || *item.ForEachParent != parent.StepName || item.ForEachIndex == nil || *item.ForEachIndex != index {
			return WorkflowForEachOutcome{}, "", nil, nil, ErrWorkflowForEachEvaluation
		}
		switch item.Status {
		case WorkflowStepStatusSucceeded:
			continue
		case WorkflowStepStatusPending:
			if item.NextRetryAt != nil && item.NextRetryAt.After(time.Now()) {
				return WorkflowForEachOutcome{}, "", nil, nil, nil
			}
			item.ForEachParent = cloneWorkflowString(item.ForEachParent)
			item.ForEachIndex = cloneWorkflowInt(item.ForEachIndex)
			item.Input = cloneWorkflowJSON(item.Input)
			return WorkflowForEachOutcome{Item: &item}, "", nil, nil, nil
		case WorkflowStepStatusRunning:
			return WorkflowForEachOutcome{}, "", nil, nil, nil
		default:
			output, err := workflowForEachOutputs(steps, parent.StepName, *parent.ForEachCount, "", nil)
			message := fmt.Sprintf("for_each item %d failed", index)
			status := WorkflowStepStatusFailed
			if item.Status == WorkflowStepStatusDead {
				status = WorkflowStepStatusDead
			}
			return WorkflowForEachOutcome{Complete: true}, status, output, &message, err
		}
	}
	output, err := workflowForEachOutputs(steps, parent.StepName, *parent.ForEachCount, "", nil)
	return WorkflowForEachOutcome{Complete: true}, WorkflowStepStatusSucceeded, output, nil, err
}

func (m *MemStore) ResolveWorkflowForEach(ctx context.Context, runID, name string) (WorkflowForEachOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return WorkflowForEachOutcome{}, ErrWorkflowRunNotFound
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) || !workflowGuardRunActive(run.Status) {
		return WorkflowForEachOutcome{}, ErrWorkflowGuardNotReady
	}
	steps := m.workflowSteps[runID]
	parent, ok := steps[name]
	if !ok {
		return WorkflowForEachOutcome{}, ErrWorkflowStepNotFound
	}
	if parent.Status == WorkflowStepStatusSucceeded || parent.Status == WorkflowStepStatusFailed || parent.Status == WorkflowStepStatusDead {
		return WorkflowForEachOutcome{Complete: true}, nil
	}
	spec, err := workflowForEachDefinition(run, parent, steps)
	if err != nil {
		return WorkflowForEachOutcome{}, err
	}
	now := time.Now().UTC()
	if parent.ForEachCount == nil {
		items, inputs, err := workflowForEachInputs(run, *spec, steps)
		if err != nil {
			return WorkflowForEachOutcome{}, err
		}
		count := len(inputs)
		parent.ForEachCount, parent.Input, parent.StartedAt = &count, cloneWorkflowJSON(items), &now
		steps[name] = parent
		for index, input := range inputs {
			position, owner := index, name
			childName := api.WorkflowForEachItemName(name, index)
			steps[childName] = WorkflowStep{RunID: runID, StepName: childName, Status: WorkflowStepStatusPending, Input: cloneWorkflowJSON(input), ForEachParent: &owner, ForEachIndex: &position, CreatedAt: now}
		}
	}
	outcome, status, output, message, err := workflowForEachNext(parent, steps)
	if err != nil || !outcome.Complete {
		return outcome, err
	}
	parent.Status, parent.Output, parent.Error, parent.FinishedAt = status, cloneWorkflowJSON(output), message, &now
	steps[name] = parent
	if status != WorkflowStepStatusSucceeded {
		for childName, child := range steps {
			if child.ForEachParent != nil && *child.ForEachParent == name && child.Status == WorkflowStepStatusPending {
				reason := WorkflowSkipDependencyFailed
				child.Status, child.SkipReason, child.FinishedAt, child.NextRetryAt = WorkflowStepStatusSkipped, &reason, &now, nil
				steps[childName] = child
			}
		}
	}
	return outcome, nil
}

func workflowControlSteps(ctx context.Context, db sqlc.DBTX, runID string) (map[string]WorkflowStep, error) {
	rows, err := sqlc.New().WorkflowControlSteps(ctx, db, mustPgUUID(runID))
	if err != nil {
		return nil, err
	}
	steps := make(map[string]WorkflowStep, len(rows))
	for _, row := range rows {
		step := WorkflowStep{RunID: runID, StepName: row.StepName, Status: row.Status, Attempt: int(row.Attempt), Input: row.Input, Output: row.Output, RetryBase: int(row.RetryBase)}
		if row.WhenMatched.Valid {
			value := row.WhenMatched.Bool
			step.WhenMatched = &value
		}
		if row.ForeachParent.Valid {
			value := row.ForeachParent.String
			step.ForEachParent = &value
		}
		if row.ForeachIndex.Valid {
			value := int(row.ForeachIndex.Int32)
			step.ForEachIndex = &value
		}
		if row.ForeachCount.Valid {
			value := int(row.ForeachCount.Int32)
			step.ForEachCount = &value
		}
		if row.NextRetryAt.Valid {
			value := row.NextRetryAt.Time
			step.NextRetryAt = &value
		}
		steps[row.StepName] = step
	}
	return steps, nil
}

func (s *PgStore) ResolveWorkflowForEach(ctx context.Context, runID, name string) (WorkflowForEachOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkflowForEachOutcome{}, fmt.Errorf("pgstore: begin for_each: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, _, err := lockWorkflowGuard(ctx, tx, runID, name)
	if err != nil {
		return WorkflowForEachOutcome{}, err
	}
	steps, err := workflowControlSteps(ctx, tx, runID)
	if err != nil {
		return WorkflowForEachOutcome{}, fmt.Errorf("pgstore: read for_each: %w", err)
	}
	parent := steps[name]
	if parent.Status == WorkflowStepStatusSucceeded || parent.Status == WorkflowStepStatusFailed || parent.Status == WorkflowStepStatusDead {
		return WorkflowForEachOutcome{Complete: true}, nil
	}
	run := WorkflowRun{Input: locked.Input, DefinitionSnapshot: locked.DefinitionSnapshot}
	spec, err := workflowForEachDefinition(run, parent, steps)
	if err != nil {
		return WorkflowForEachOutcome{}, err
	}
	q := sqlc.New()
	if parent.ForEachCount == nil {
		items, inputs, err := workflowForEachInputs(run, *spec, steps)
		if err != nil {
			return WorkflowForEachOutcome{}, err
		}
		count := len(inputs)
		if err := q.InitializeWorkflowForEach(ctx, tx, sqlc.InitializeWorkflowForEachParams{RunID: mustPgUUID(runID), StepName: name, ItemCount: int32(count), Items: items}); err != nil {
			return WorkflowForEachOutcome{}, fmt.Errorf("pgstore: snapshot for_each: %w", err)
		}
		parent.ForEachCount = &count
		for index, input := range inputs {
			childName := api.WorkflowForEachItemName(name, index)
			if err := q.CreateWorkflowForEachItem(ctx, tx, sqlc.CreateWorkflowForEachItemParams{RunID: mustPgUUID(runID), StepName: childName, Input: input, Parent: name, ItemIndex: int32(index)}); err != nil {
				return WorkflowForEachOutcome{}, fmt.Errorf("pgstore: create for_each item: %w", err)
			}
			owner, position := name, index
			steps[childName] = WorkflowStep{RunID: runID, StepName: childName, Status: WorkflowStepStatusPending, Input: input, ForEachParent: &owner, ForEachIndex: &position}
		}
	}
	outcome, status, output, message, err := workflowForEachNext(parent, steps)
	if err != nil {
		return outcome, err
	}
	if outcome.Complete {
		var errorText pgtype.Text
		if message != nil {
			errorText = pgtype.Text{String: *message, Valid: true}
		}
		if err := q.CompleteWorkflowForEach(ctx, tx, sqlc.CompleteWorkflowForEachParams{RunID: mustPgUUID(runID), StepName: name, Status: status, Output: output, Error: errorText}); err != nil {
			return outcome, fmt.Errorf("pgstore: complete for_each: %w", err)
		}
		if status != WorkflowStepStatusSucceeded {
			if err := q.SkipWorkflowForEachRemaining(ctx, tx, sqlc.SkipWorkflowForEachRemainingParams{RunID: mustPgUUID(runID), Parent: pgtype.Text{String: name, Valid: true}}); err != nil {
				return outcome, err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkflowForEachOutcome{}, fmt.Errorf("pgstore: commit for_each: %w", err)
	}
	return outcome, nil
}

func workflowForEachStartAllowed(snapshot json.RawMessage, steps map[string]WorkflowStep, step WorkflowStep, input json.RawMessage) bool {
	spec := api.WorkflowRuntimeStep(snapshot, step.StepName)
	if spec != nil && spec.ForEach != nil {
		return false
	}
	if step.ForEachParent == nil {
		return true
	}
	if step.ForEachIndex == nil || !bytes.Equal(input, step.Input) {
		return false
	}
	parent, ok := steps[*step.ForEachParent]
	if !ok || parent.Status != WorkflowStepStatusPending || parent.ForEachCount == nil || *parent.ForEachCount <= *step.ForEachIndex {
		return false
	}
	for index := range *step.ForEachIndex {
		if previous := steps[api.WorkflowForEachItemName(parent.StepName, index)]; previous.Status != WorkflowStepStatusSucceeded {
			return false
		}
	}
	return true
}
