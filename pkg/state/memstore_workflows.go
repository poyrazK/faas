package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// CreateWorkflowRun inserts a new workflow_runs row into memory.
func (m *MemStore) CreateWorkflowRun(_ context.Context, r *WorkflowRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	r.CreatedAt = now
	r.UpdatedAt = now
	if r.Status == "" {
		r.Status = WorkflowRunStatusPending
	}
	if r.ScheduledFor.IsZero() {
		r.ScheduledFor = now
	}
	if len(r.Input) == 0 {
		r.Input = json.RawMessage("{}")
	}

	m.workflowRuns[r.ID] = *r
	return nil
}

// GetWorkflowRun retrieves a single workflow run by ID.
func (m *MemStore) GetWorkflowRun(_ context.Context, id string) (*WorkflowRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.workflowRuns[id]
	if !ok {
		return nil, ErrWorkflowRunNotFound
	}
	cp := r
	return &cp, nil
}

// ListWorkflowRuns lists runs for an app with pagination and status filtering.
func (m *MemStore) ListWorkflowRuns(_ context.Context, appID string, opts ListWorkflowRunsOpts) ([]*WorkflowRun, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var matched []*WorkflowRun
	for _, r := range m.workflowRuns {
		if r.AppID != appID {
			continue
		}
		if opts.Status != "" && r.Status != opts.Status {
			continue
		}
		cp := r
		matched = append(matched, &cp)
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].CreatedAt.After(matched[j].CreatedAt)
	})

	total := len(matched)
	if opts.Offset >= len(matched) {
		return []*WorkflowRun{}, total, nil
	}
	matched = matched[opts.Offset:]
	if opts.Limit > 0 && opts.Limit < len(matched) {
		matched = matched[:opts.Limit]
	}

	return matched, total, nil
}

// MarkWorkflowRunStatus transitions a workflow run's status and records output/error.
func (m *MemStore) MarkWorkflowRunStatus(_ context.Context, id, status string, output json.RawMessage, lastErr *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.workflowRuns[id]
	if !ok {
		return ErrWorkflowRunNotFound
	}

	now := time.Now().UTC()
	r.Status = status
	r.UpdatedAt = now
	if len(output) > 0 {
		r.Output = output
	}
	if lastErr != nil {
		r.LastError = lastErr
	}
	if status == WorkflowRunStatusRunning && r.StartedAt == nil {
		r.StartedAt = &now
	}
	if status == WorkflowRunStatusSucceeded || status == WorkflowRunStatusFailed || status == WorkflowRunStatusDead {
		r.FinishedAt = &now
	}

	m.workflowRuns[id] = r
	return nil
}

// ClaimNextPendingRun finds the oldest pending run ready for dispatch and sets it to running.
func (m *MemStore) ClaimNextPendingRun(_ context.Context) (*WorkflowRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	var candidates []WorkflowRun
	for _, r := range m.workflowRuns {
		if r.Status == WorkflowRunStatusPending && !r.ScheduledFor.After(now) {
			candidates = append(candidates, r)
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNotFound
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].ScheduledFor.Before(candidates[j].ScheduledFor)
	})

	chosen := candidates[0]
	chosen.Status = WorkflowRunStatusRunning
	chosen.StartedAt = &now
	chosen.UpdatedAt = now
	m.workflowRuns[chosen.ID] = chosen

	cp := chosen
	return &cp, nil
}

// CountActiveRunsByApp counts runs currently in pending, running, or awaiting_event states.
func (m *MemStore) CountActiveRunsByApp(_ context.Context, appID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, r := range m.workflowRuns {
		if r.AppID == appID {
			if r.Status == WorkflowRunStatusPending || r.Status == WorkflowRunStatusRunning || r.Status == WorkflowRunStatusAwaitingEvent {
				count++
			}
		}
	}
	return count, nil
}

// CreateWorkflowSteps persists the step rows for a workflow run.
func (m *MemStore) CreateWorkflowSteps(_ context.Context, runID string, steps []*WorkflowStep) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.workflowSteps[runID]; !ok {
		m.workflowSteps[runID] = make(map[string]WorkflowStep)
	}

	now := time.Now().UTC()
	for _, s := range steps {
		s.RunID = runID
		if s.Status == "" {
			s.Status = WorkflowStepStatusPending
		}
		s.CreatedAt = now
		m.workflowSteps[runID][s.StepName] = *s
	}
	return nil
}

// GetWorkflowSteps returns all step records for a run.
func (m *MemStore) GetWorkflowSteps(_ context.Context, runID string) ([]*WorkflowStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	stepsMap, ok := m.workflowSteps[runID]
	if !ok {
		return []*WorkflowStep{}, nil
	}

	var steps []*WorkflowStep
	for _, s := range stepsMap {
		cp := s
		steps = append(steps, &cp)
	}

	sort.Slice(steps, func(i, j int) bool {
		return steps[i].CreatedAt.Before(steps[j].CreatedAt)
	})

	return steps, nil
}

// MarkWorkflowStepStatus updates the execution state of a step.
func (m *MemStore) MarkWorkflowStepStatus(_ context.Context, runID, stepName, status string, attempt int, output json.RawMessage, err *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stepsMap, ok := m.workflowSteps[runID]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	step, ok := stepsMap[stepName]
	if !ok {
		return ErrWorkflowStepNotFound
	}

	now := time.Now().UTC()
	step.Status = status
	step.Attempt = attempt
	if len(output) > 0 {
		step.Output = output
	}
	if err != nil {
		step.Error = err
	}
	if status == WorkflowStepStatusRunning && step.StartedAt == nil {
		step.StartedAt = &now
	}
	if status == WorkflowStepStatusSucceeded || status == WorkflowStepStatusFailed || status == WorkflowStepStatusDead || status == WorkflowStepStatusSkipped {
		step.FinishedAt = &now
	}

	stepsMap[stepName] = step
	m.workflowSteps[runID] = stepsMap

	// Also update run's current_step pointer
	if r, ok := m.workflowRuns[runID]; ok {
		r.CurrentStep = &stepName
		r.UpdatedAt = now
		m.workflowRuns[runID] = r
	}

	return nil
}

// InsertWorkflowEvent appends an external event to a run's event log.
func (m *MemStore) InsertWorkflowEvent(_ context.Context, e *WorkflowEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = time.Now().UTC()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}

	m.workflowEvents[e.RunID] = append(m.workflowEvents[e.RunID], *e)
	return nil
}

// GetWorkflowEventsForRun returns all events received for a given workflow run.
func (m *MemStore) GetWorkflowEventsForRun(_ context.Context, runID string) ([]*WorkflowEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	events := m.workflowEvents[runID]
	var res []*WorkflowEvent
	for _, e := range events {
		cp := e
		res = append(res, &cp)
	}
	return res, nil
}

// FindMatchingEvent finds the first event matching eventName for a run.
func (m *MemStore) FindMatchingEvent(_ context.Context, runID, eventName string) (*WorkflowEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, e := range m.workflowEvents[runID] {
		if e.EventName == eventName {
			cp := e
			return &cp, nil
		}
	}
	return nil, ErrWorkflowEventNotFound
}

// SweepExpiredWorkflowRuns removes finished workflow runs older than olderThan.
func (m *MemStore) SweepExpiredWorkflowRuns(_ context.Context, olderThan time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	threshold := time.Now().UTC().Add(-olderThan)
	deleted := 0
	for id, r := range m.workflowRuns {
		if r.FinishedAt != nil && r.FinishedAt.Before(threshold) {
			delete(m.workflowRuns, id)
			delete(m.workflowSteps, id)
			delete(m.workflowEvents, id)
			deleted++
		}
	}
	return deleted, nil
}

// SweepExpiredWorkflowEvents removes events older than olderThan.
func (m *MemStore) SweepExpiredWorkflowEvents(_ context.Context, olderThan time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	threshold := time.Now().UTC().Add(-olderThan)
	deleted := 0
	for runID, events := range m.workflowEvents {
		var kept []WorkflowEvent
		for _, e := range events {
			if e.ReceivedAt.Before(threshold) {
				deleted++
			} else {
				kept = append(kept, e)
			}
		}
		m.workflowEvents[runID] = kept
	}
	return deleted, nil
}
