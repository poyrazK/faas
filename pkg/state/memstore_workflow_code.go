package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ WorkflowCodeStore = (*MemStore)(nil)

// lockedWorkflowCodeRun returns a non-terminal run whose generation matches.
// The caller holds m.mu.
func (m *MemStore) lockedWorkflowCodeRun(ctx context.Context, runID string) (WorkflowRun, error) {
	run, ok := m.workflowRuns[runID]
	if !ok {
		return WorkflowRun{}, ErrWorkflowRunNotFound
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return WorkflowRun{}, ErrWorkflowOutboundAttemptExpired
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return WorkflowRun{}, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	return run, nil
}

func (m *MemStore) BeginWorkflowCodeInvocation(ctx context.Context, runID string, driverInput json.RawMessage) (int, error) {
	if err := validateWorkflowJSON(driverInput, true); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, err := m.lockedWorkflowCodeRun(ctx, runID)
	if err != nil {
		return 0, err
	}
	if m.workflowSteps[runID] == nil {
		m.workflowSteps[runID] = map[string]WorkflowStep{}
	}
	now := time.Now().UTC()
	driver, ok := m.workflowSteps[runID][api.WorkflowCodeDriverStep]
	if !ok {
		driver = WorkflowStep{RunID: runID, StepName: api.WorkflowCodeDriverStep, Status: WorkflowStepStatusPending, CreatedAt: now}
	}
	if workflowCodeTerminalStatus(driver.Status) {
		return 0, fmt.Errorf("%w: code workflow driver is %s", ErrConflict, driver.Status)
	}
	driver.Attempt++
	driver.Status = WorkflowStepStatusRunning
	driver.Input = cloneWorkflowJSON(driverInput)
	driver.Error, driver.NextRetryAt, driver.FinishedAt = nil, nil, nil
	if driver.StartedAt == nil {
		driver.StartedAt = &now
	}
	m.workflowSteps[runID][api.WorkflowCodeDriverStep] = driver
	if m.workflowStepAttempts == nil {
		m.workflowStepAttempts = make(map[workflowStepAttemptKey]WorkflowStepAttempt)
	}
	m.workflowStepAttempts[workflowStepAttemptKey{runID: runID, stepName: api.WorkflowCodeDriverStep, attempt: driver.Attempt}] = WorkflowStepAttempt{
		RunID: runID, StepName: api.WorkflowCodeDriverStep, Attempt: driver.Attempt, Status: WorkflowAttemptStatusRunning, StartedAt: now,
	}
	stepName := api.WorkflowCodeDriverStep
	run.Status = WorkflowRunStatusRunning
	run.CurrentStep = &stepName
	if run.StartedAt == nil {
		run.StartedAt = &now
	}
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	return driver.Attempt, nil
}

func (m *MemStore) FinishWorkflowCodeInvocation(ctx context.Context, runID string, finish WorkflowCodeInvocation) error {
	if err := validateWorkflowCodeInvocation(finish); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, err := m.lockedWorkflowCodeRun(ctx, runID)
	if err != nil {
		return err
	}
	steps := m.workflowSteps[runID]
	driver, ok := steps[api.WorkflowCodeDriverStep]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	if driver.Status != WorkflowStepStatusRunning || driver.Attempt != finish.Attempt {
		return fmt.Errorf("%w: code workflow invocation %d is not current", ErrConflict, finish.Attempt)
	}
	// Validate every write before applying any, so a refusal changes nothing.
	pending := map[string]bool{}
	for _, op := range finish.Ops {
		existing, exists := steps[op.StepName]
		switch {
		case op.New && (exists || pending[op.StepName]):
			return fmt.Errorf("%w: code operation %q already exists", ErrConflict, op.StepName)
		case !op.New && (!exists || workflowCodeTerminalStatus(existing.Status)):
			return fmt.Errorf("%w: code operation %q is not pending", ErrConflict, op.StepName)
		}
		pending[op.StepName] = true
	}
	now := time.Now().UTC()
	for _, op := range finish.Ops {
		step := steps[op.StepName]
		if op.New {
			step = WorkflowStep{RunID: runID, StepName: op.StepName, Input: cloneWorkflowJSON(op.Input), CreatedAt: now}
		}
		step.Status = op.Status
		step.Attempt = op.Attempt
		step.Output = cloneWorkflowJSON(op.Output)
		step.Error = cloneWorkflowString(op.Error)
		step.NextRetryAt = cloneWorkflowTime(op.RetryAt)
		if step.StartedAt == nil {
			step.StartedAt = &now
		}
		step.FinishedAt = nil
		if op.Status == WorkflowStepStatusSucceeded || op.Status == WorkflowStepStatusFailed {
			step.FinishedAt = &now
		}
		steps[op.StepName] = step
		if op.Attempt > 0 {
			status := workflowCodeAttemptStatus(op)
			if op.RetryAt != nil {
				status = WorkflowAttemptStatusRetrying
			}
			key := workflowStepAttemptKey{runID: runID, stepName: op.StepName, attempt: op.Attempt}
			record, exists := m.workflowStepAttempts[key]
			if !exists {
				record = WorkflowStepAttempt{RunID: runID, StepName: op.StepName, Attempt: op.Attempt, StartedAt: now}
			}
			record.Status = status
			record.FinishedAt = &now
			record.NextAttemptAt = cloneWorkflowTime(op.RetryAt)
			record.Error = cloneWorkflowString(op.Error)
			m.workflowStepAttempts[key] = record
		}
	}
	key := workflowStepAttemptKey{runID: runID, stepName: api.WorkflowCodeDriverStep, attempt: finish.Attempt}
	record, ok := m.workflowStepAttempts[key]
	if !ok {
		return ErrWorkflowAttemptNotFound
	}
	record.Status = WorkflowAttemptStatusSucceeded
	if finish.Error != nil {
		record.Status = WorkflowAttemptStatusFailed
		if finish.RetryAt != nil {
			record.Status = WorkflowAttemptStatusRetrying
		}
	}
	record.HTTPStatus = cloneWorkflowInt(finish.HTTPStatus)
	record.FinishedAt = &now
	record.NextAttemptAt = cloneWorkflowTime(finish.RetryAt)
	record.Error = cloneWorkflowString(finish.Error)
	m.workflowStepAttempts[key] = record
	driver.Status = WorkflowStepStatusPending
	if len(finish.DriverInput) > 0 {
		driver.Input = cloneWorkflowJSON(finish.DriverInput)
	}
	if len(finish.Result) > 0 {
		driver.Output = cloneWorkflowJSON(finish.Result)
	}
	driver.Error = cloneWorkflowString(finish.Error)
	driver.NextRetryAt = cloneWorkflowTime(finish.RetryAt)
	steps[api.WorkflowCodeDriverStep] = driver
	m.workflowSteps[runID] = steps
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	return m.syncOperationWorkflowLocked(runID)
}

func (m *MemStore) ParkWorkflowCodeEvent(ctx context.Context, runID, stepName, eventName string, occurrence int, timeout time.Duration) (*WorkflowEvent, bool, time.Time, error) {
	if eventName == "" || occurrence < 0 || timeout <= 0 {
		return nil, false, time.Time{}, ErrWorkflowInvalidRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, err := m.lockedWorkflowCodeRun(ctx, runID)
	if err != nil {
		return nil, false, time.Time{}, err
	}
	step, ok := m.workflowSteps[runID][stepName]
	if !ok {
		return nil, false, time.Time{}, ErrWorkflowStepNotFound
	}
	if step.Status != WorkflowStepStatusPending && step.Status != WorkflowStepStatusAwaitingEvent {
		return nil, false, time.Time{}, fmt.Errorf("%w: event wait step is not pending or awaiting", ErrConflict)
	}
	var deadline time.Time
	if step.StartedAt != nil {
		deadline = step.StartedAt.Add(timeout)
	}
	var matches []WorkflowEvent
	for _, event := range m.workflowEvents[runID] {
		if event.EventName == eventName && (deadline.IsZero() || event.ReceivedAt.Before(deadline)) {
			matches = append(matches, event)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].ReceivedAt.Equal(matches[j].ReceivedAt) {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].ReceivedAt.Before(matches[j].ReceivedAt)
	})
	now := time.Now().UTC()
	if occurrence < len(matches) {
		matched := matches[occurrence]
		matched.Payload = cloneWorkflowJSON(matched.Payload)
		step.Status = WorkflowStepStatusSucceeded
		step.Output = cloneWorkflowJSON(matched.Payload)
		if step.StartedAt == nil {
			step.StartedAt = &now
		}
		step.FinishedAt = &now
		m.workflowSteps[runID][stepName] = step
		return &matched, false, time.Time{}, nil
	}
	if !deadline.IsZero() && !now.Before(deadline) {
		step.Status = WorkflowStepStatusSucceeded
		step.Output = nil
		step.FinishedAt = &now
		m.workflowSteps[runID][stepName] = step
		return nil, true, time.Time{}, nil
	}
	if step.StartedAt == nil {
		step.StartedAt = &now
	}
	deadline = step.StartedAt.Add(timeout)
	step.Status = WorkflowStepStatusAwaitingEvent
	m.workflowSteps[runID][stepName] = step
	run.Status = WorkflowRunStatusAwaitingEvent
	run.CurrentStep = &stepName
	run.ScheduledFor = earlierWorkflowWake(run.ScheduledFor, deadline, now)
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	if err := m.syncOperationWorkflowLocked(runID); err != nil {
		return nil, false, time.Time{}, err
	}
	return nil, false, deadline, nil
}
