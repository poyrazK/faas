package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// CreateWorkflowRun inserts a new workflow_runs row into memory.
func (m *MemStore) CreateWorkflowRun(_ context.Context, r *WorkflowRun) error {
	if r == nil {
		return fmt.Errorf("%w: nil run", ErrWorkflowInvalidRecord)
	}
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
	if err := validateWorkflowRunStatus(r.Status); err != nil {
		return err
	}
	if r.ScheduledFor.IsZero() {
		r.ScheduledFor = now
	}
	if len(r.Input) == 0 {
		r.Input = json.RawMessage("{}")
	}
	if err := validateWorkflowJSON(r.Input, true); err != nil {
		return err
	}
	if err := validateWorkflowJSON(r.DefinitionSnapshot, true); err != nil {
		return err
	}

	if _, exists := m.workflowRuns[r.ID]; exists {
		return fmt.Errorf("%w: workflow_runs.id", ErrConflict)
	}
	stored := *r
	stored.Input = cloneWorkflowJSON(r.Input)
	stored.Output = cloneWorkflowJSON(r.Output)
	stored.DefinitionSnapshot = cloneWorkflowJSON(r.DefinitionSnapshot)
	m.workflowRuns[r.ID] = stored
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
	cp.Input = cloneWorkflowJSON(r.Input)
	cp.Output = cloneWorkflowJSON(r.Output)
	cp.DefinitionSnapshot = cloneWorkflowJSON(r.DefinitionSnapshot)
	return &cp, nil
}

// ListWorkflowRuns lists runs for an app with pagination and status filtering.
func (m *MemStore) ListWorkflowRuns(_ context.Context, appID string, opts ListWorkflowRunsOpts) ([]*WorkflowRun, int, error) {
	if opts.Offset < 0 || opts.Limit < 0 {
		return nil, 0, ErrWorkflowInvalidPagination
	}
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
		cp.Input = cloneWorkflowJSON(r.Input)
		cp.Output = cloneWorkflowJSON(r.Output)
		cp.DefinitionSnapshot = cloneWorkflowJSON(r.DefinitionSnapshot)
		matched = append(matched, &cp)
	}

	sort.Slice(matched, func(i, j int) bool {
		if matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].ID > matched[j].ID
		}
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
	if err := validateWorkflowRunStatus(status); err != nil {
		return err
	}
	if err := validateWorkflowJSON(output, false); err != nil {
		return err
	}
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
		r.Output = cloneWorkflowJSON(output)
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
		if candidates[i].ScheduledFor.Equal(candidates[j].ScheduledFor) {
			return candidates[i].ID < candidates[j].ID
		}
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

// ClaimNextDueWorkflowRun claims the oldest pending or parked run whose
// scheduled time has arrived. A parked wait uses scheduled_for as its timeout
// deadline, so this same claim path handles both normal dispatch and wakeups.
func (m *MemStore) ClaimNextDueWorkflowRun(_ context.Context) (*WorkflowRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	var candidates []WorkflowRun
	for _, r := range m.workflowRuns {
		if (r.Status == WorkflowRunStatusPending || r.Status == WorkflowRunStatusAwaitingEvent) && !r.ScheduledFor.After(now) {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ScheduledFor.Equal(candidates[j].ScheduledFor) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].ScheduledFor.Before(candidates[j].ScheduledFor)
	})
	chosen := candidates[0]
	chosen.Status = WorkflowRunStatusRunning
	chosen.StartedAt = firstWorkflowTime(chosen.StartedAt, now)
	chosen.UpdatedAt = now
	m.workflowRuns[chosen.ID] = chosen
	cp := chosen
	cp.Input = cloneWorkflowJSON(chosen.Input)
	cp.Output = cloneWorkflowJSON(chosen.Output)
	cp.DefinitionSnapshot = cloneWorkflowJSON(chosen.DefinitionSnapshot)
	return &cp, nil
}

// ScheduleWorkflowRun changes a run's scheduler-visible state and deadline.
func (m *MemStore) ScheduleWorkflowRun(_ context.Context, id, status string, scheduledFor time.Time) error {
	if err := validateWorkflowRunStatus(status); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.workflowRuns[id]
	if !ok {
		return ErrWorkflowRunNotFound
	}
	r.Status = status
	r.ScheduledFor = scheduledFor.UTC()
	r.UpdatedAt = time.Now().UTC()
	m.workflowRuns[id] = r
	return nil
}

func firstWorkflowTime(current *time.Time, fallback time.Time) *time.Time {
	if current != nil {
		return current
	}
	return &fallback
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
	if len(steps) == 0 {
		return nil
	}
	for _, step := range steps {
		if step == nil {
			return fmt.Errorf("%w: nil step", ErrWorkflowInvalidRecord)
		}
		if step.StepName == "" {
			return fmt.Errorf("%w: step name is empty", ErrWorkflowInvalidRecord)
		}
		status := step.Status
		if status == "" {
			status = WorkflowStepStatusPending
		}
		if err := validateWorkflowStepStatus(status); err != nil {
			return err
		}
		if step.Attempt < 0 {
			return ErrWorkflowInvalidAttempt
		}
		if err := validateWorkflowJSON(step.Input, false); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.workflowRuns[runID]; !ok {
		return ErrWorkflowRunNotFound
	}
	if _, ok := m.workflowSteps[runID]; !ok {
		m.workflowSteps[runID] = make(map[string]WorkflowStep)
	}

	now := time.Now().UTC()
	for _, s := range steps {
		if _, exists := m.workflowSteps[runID][s.StepName]; exists {
			continue
		}
		stored := *s
		stored.RunID = runID
		if stored.Status == "" {
			stored.Status = WorkflowStepStatusPending
		}
		stored.CreatedAt = now
		stored.Input = cloneWorkflowJSON(stored.Input)
		m.workflowSteps[runID][stored.StepName] = stored
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
		cp.Input = cloneWorkflowJSON(s.Input)
		cp.Output = cloneWorkflowJSON(s.Output)
		steps = append(steps, &cp)
	}

	sort.Slice(steps, func(i, j int) bool {
		if steps[i].CreatedAt.Equal(steps[j].CreatedAt) {
			return steps[i].StepName < steps[j].StepName
		}
		return steps[i].CreatedAt.Before(steps[j].CreatedAt)
	})

	return steps, nil
}

// MarkWorkflowStepStatus updates the execution state of a step.
func (m *MemStore) MarkWorkflowStepStatus(_ context.Context, runID, stepName, status string, attempt int, output json.RawMessage, err *string) error {
	if statusErr := validateWorkflowStepStatus(status); statusErr != nil {
		return statusErr
	}
	if attempt < 0 {
		return ErrWorkflowInvalidAttempt
	}
	if jsonErr := validateWorkflowJSON(output, false); jsonErr != nil {
		return jsonErr
	}
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
		step.Output = cloneWorkflowJSON(output)
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
	if e == nil {
		return fmt.Errorf("%w: nil event", ErrWorkflowInvalidRecord)
	}
	if e.RunID == "" || e.EventName == "" {
		return fmt.Errorf("%w: event run_id and event_name are required", ErrWorkflowInvalidRecord)
	}
	if err := validateWorkflowJSON(e.Payload, false); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workflowRuns[e.RunID]; !ok {
		return ErrWorkflowRunNotFound
	}

	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.ReceivedAt.IsZero() {
		e.ReceivedAt = time.Now().UTC()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage("{}")
	}
	for _, events := range m.workflowEvents {
		for _, existing := range events {
			if existing.ID == e.ID {
				return fmt.Errorf("%w: workflow_events.id", ErrConflict)
			}
		}
	}

	stored := *e
	stored.Payload = cloneWorkflowJSON(e.Payload)
	m.workflowEvents[e.RunID] = append(m.workflowEvents[e.RunID], stored)
	return nil
}

// GetWorkflowEventsForRun returns all events received for a given workflow run.
func (m *MemStore) GetWorkflowEventsForRun(_ context.Context, runID string) ([]*WorkflowEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	events := append([]WorkflowEvent(nil), m.workflowEvents[runID]...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].ReceivedAt.Equal(events[j].ReceivedAt) {
			return events[i].ID < events[j].ID
		}
		return events[i].ReceivedAt.Before(events[j].ReceivedAt)
	})
	var res []*WorkflowEvent
	for _, e := range events {
		cp := e
		cp.Payload = cloneWorkflowJSON(e.Payload)
		res = append(res, &cp)
	}
	return res, nil
}

// FindMatchingEvent finds the first event matching eventName for a run.
func (m *MemStore) FindMatchingEvent(_ context.Context, runID, eventName string) (*WorkflowEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	events := append([]WorkflowEvent(nil), m.workflowEvents[runID]...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].ReceivedAt.Equal(events[j].ReceivedAt) {
			return events[i].ID < events[j].ID
		}
		return events[i].ReceivedAt.Before(events[j].ReceivedAt)
	})
	for _, e := range events {
		if e.EventName == eventName {
			cp := e
			cp.Payload = cloneWorkflowJSON(e.Payload)
			return &cp, nil
		}
	}
	return nil, ErrWorkflowEventNotFound
}

// SweepExpiredWorkflowRuns removes finished workflow runs older than olderThan.
func (m *MemStore) SweepExpiredWorkflowRuns(_ context.Context, olderThan time.Duration) (int, error) {
	if olderThan < 0 {
		return 0, ErrWorkflowInvalidRecord
	}
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
	if olderThan < 0 {
		return 0, ErrWorkflowInvalidRecord
	}
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
