package state

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

type workflowRunCreateKey struct {
	appID, workflowName, key string
}

type workflowRunCreateKeyEntry struct {
	runID              string
	requestFingerprint []byte
}

// CreateWorkflowRun inserts a new workflow_runs row into memory.
func (m *MemStore) CreateWorkflowRun(_ context.Context, r *WorkflowRun) error {
	if err := prepareWorkflowRun(r); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.insertWorkflowRunLocked(r)
}

func (m *MemStore) insertWorkflowRunLocked(r *WorkflowRun) error {
	now := time.Now().UTC()
	r.CreatedAt = now
	r.UpdatedAt = now
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

// CreateWorkflowRunAdmitted performs the quota check and insert under the same
// store lock, mirroring PgStore's per-app advisory-lock transaction.
func (m *MemStore) CreateWorkflowRunAdmitted(_ context.Context, r *WorkflowRun, maxActive int) (int, error) {
	if maxActive < 1 {
		return 0, ErrWorkflowRunQuotaExceeded
	}
	if err := prepareWorkflowRun(r); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	active := 0
	for _, existing := range m.workflowRuns {
		if existing.AppID == r.AppID && (existing.Status == WorkflowRunStatusPending || existing.Status == WorkflowRunStatusRunning || existing.Status == WorkflowRunStatusAwaitingEvent) {
			active++
		}
	}
	if active >= maxActive {
		return active, ErrWorkflowRunQuotaExceeded
	}
	if err := m.insertWorkflowRunLocked(r); err != nil {
		return active, err
	}
	return active + 1, nil
}

func (m *MemStore) GetWorkflowRunByIdempotencyKey(_ context.Context, appID, workflowName, key string, requestFingerprint []byte) (*WorkflowRun, error) {
	if err := validateWorkflowRunCreateIdempotency(key, requestFingerprint); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.workflowRunCreateKeys[workflowRunCreateKey{appID: appID, workflowName: workflowName, key: key}]
	if !ok {
		return nil, ErrWorkflowRunNotFound
	}
	if !bytes.Equal(entry.requestFingerprint, requestFingerprint) {
		return nil, ErrWorkflowRunIdempotencyConflict
	}
	run, ok := m.workflowRuns[entry.runID]
	if !ok {
		return nil, ErrWorkflowRunNotFound
	}
	copy := run
	copy.CancelledAt = cloneWorkflowTime(run.CancelledAt)
	copy.Input = cloneWorkflowJSON(run.Input)
	copy.Output = cloneWorkflowJSON(run.Output)
	copy.DefinitionSnapshot = cloneWorkflowJSON(run.DefinitionSnapshot)
	return &copy, nil
}

func (m *MemStore) CreateWorkflowRunAdmittedWithIdempotencyKey(_ context.Context, r *WorkflowRun, maxActive int, key string, requestFingerprint []byte) (int, bool, error) {
	if err := validateWorkflowRunCreateIdempotency(key, requestFingerprint); err != nil {
		return 0, false, err
	}
	if err := prepareWorkflowRun(r); err != nil {
		return 0, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	indexKey := workflowRunCreateKey{appID: r.AppID, workflowName: r.WorkflowName, key: key}
	if entry, ok := m.workflowRunCreateKeys[indexKey]; ok {
		if !bytes.Equal(entry.requestFingerprint, requestFingerprint) {
			return 0, false, ErrWorkflowRunIdempotencyConflict
		}
		existing, ok := m.workflowRuns[entry.runID]
		if !ok {
			return 0, false, ErrWorkflowRunNotFound
		}
		*r = existing
		r.CancelledAt = cloneWorkflowTime(existing.CancelledAt)
		r.Input = cloneWorkflowJSON(existing.Input)
		r.Output = cloneWorkflowJSON(existing.Output)
		r.DefinitionSnapshot = cloneWorkflowJSON(existing.DefinitionSnapshot)
		return 0, true, nil
	}
	if maxActive < 1 {
		return 0, false, ErrWorkflowRunQuotaExceeded
	}
	active := 0
	for _, existing := range m.workflowRuns {
		if existing.AppID == r.AppID && (existing.Status == WorkflowRunStatusPending || existing.Status == WorkflowRunStatusRunning || existing.Status == WorkflowRunStatusAwaitingEvent) {
			active++
		}
	}
	if active >= maxActive {
		return active, false, ErrWorkflowRunQuotaExceeded
	}
	if err := m.insertWorkflowRunLocked(r); err != nil {
		return active, false, err
	}
	if m.workflowRunCreateKeys == nil {
		m.workflowRunCreateKeys = make(map[workflowRunCreateKey]workflowRunCreateKeyEntry)
	}
	m.workflowRunCreateKeys[indexKey] = workflowRunCreateKeyEntry{
		runID: r.ID, requestFingerprint: append([]byte(nil), requestFingerprint...),
	}
	return active + 1, false, nil
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
	cp.CancelledAt = cloneWorkflowTime(r.CancelledAt)
	cp.Input = cloneWorkflowJSON(r.Input)
	cp.Output = cloneWorkflowJSON(r.Output)
	cp.DefinitionSnapshot = cloneWorkflowJSON(r.DefinitionSnapshot)
	return &cp, nil
}

// ListWorkflowRuns lists runs for an app with pagination and optional filters.
func (m *MemStore) ListWorkflowRuns(_ context.Context, appID string, opts ListWorkflowRunsOpts) ([]*WorkflowRun, int, error) {
	if opts.Offset < 0 || opts.Limit < 0 {
		return nil, 0, ErrWorkflowInvalidPagination
	}
	if opts.CreatedAfter != nil && opts.CreatedBefore != nil && opts.CreatedAfter.After(*opts.CreatedBefore) {
		return nil, 0, ErrWorkflowInvalidCreatedRange
	}
	if opts.Status != "" {
		if err := validateWorkflowRunStatus(opts.Status); err != nil {
			return nil, 0, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var matched []*WorkflowRun
	for _, r := range m.workflowRuns {
		if r.AppID != appID {
			continue
		}
		if opts.PlatformTenantID != "" && r.PlatformTenantID != opts.PlatformTenantID {
			continue
		}
		if opts.Status != "" && r.Status != opts.Status {
			continue
		}
		if opts.WorkflowName != "" && r.WorkflowName != opts.WorkflowName {
			continue
		}
		if opts.CreatedAfter != nil && r.CreatedAt.Before(*opts.CreatedAfter) {
			continue
		}
		if opts.CreatedBefore != nil && r.CreatedAt.After(*opts.CreatedBefore) {
			continue
		}
		cp := r
		cp.CancelledAt = cloneWorkflowTime(r.CancelledAt)
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
func (m *MemStore) MarkWorkflowRunStatus(ctx context.Context, id, status string, output json.RawMessage, lastErr *string) error {
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

	if !WorkflowRunGenerationMatches(ctx, id, r.ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if (r.Status == WorkflowRunStatusSucceeded || r.Status == WorkflowRunStatusFailed || r.Status == WorkflowRunStatusDead) && r.Status != status {
		return fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}

	previousStatus := r.Status
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
		if previousStatus != status {
			m.enqueueWorkflowFinishedWebhookLocked(r, now)
		}
	}

	m.workflowRuns[id] = r
	return m.syncOperationWorkflowLocked(id)
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
	if m.workflowRunLeases == nil {
		m.workflowRunLeases = make(map[string]time.Time)
	}
	m.workflowRunLeases[chosen.ID] = now.Add(5 * time.Minute)

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
		due := (r.Status == WorkflowRunStatusPending || r.Status == WorkflowRunStatusAwaitingEvent) && !r.ScheduledFor.After(now)
		deadline, leased := m.workflowRunLeases[r.ID]
		if !leased {
			deadline = r.UpdatedAt.Add(WorkflowRunStaleAfter)
		}
		stale := r.Status == WorkflowRunStatusRunning && !deadline.After(now)
		if due || stale {
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
	var chosen WorkflowRun
	claimedCandidate := false
	for _, candidate := range candidates {
		maxConcurrent := workflowRunMaxConcurrentRuns(candidate.DefinitionSnapshot)
		if maxConcurrent > 0 {
			active := 0
			for _, existing := range m.workflowRuns {
				if existing.ID == candidate.ID || existing.AppID != candidate.AppID || existing.WorkflowName != candidate.WorkflowName || !workflowRunConsumesConcurrency(existing) {
					continue
				}
				if existing.Status == WorkflowRunStatusRunning {
					deadline, leased := m.workflowRunLeases[existing.ID]
					if !leased {
						deadline = existing.UpdatedAt.Add(WorkflowRunStaleAfter)
					}
					if !deadline.After(now) {
						continue
					}
				}
				active++
			}
			if active >= maxConcurrent {
				continue
			}
		}
		chosen, claimedCandidate = candidate, true
		break
	}
	if !claimedCandidate {
		return nil, ErrNotFound
	}
	priorStatus := chosen.Status
	chosen.Status = WorkflowRunStatusRunning
	chosen.StartedAt = firstWorkflowTime(chosen.StartedAt, now)
	chosen.UpdatedAt = now
	m.workflowRuns[chosen.ID] = chosen
	if m.workflowRunLeases == nil {
		m.workflowRunLeases = make(map[string]time.Time)
	}
	m.workflowRunLeases[chosen.ID] = now.Add(5 * time.Minute)
	if priorStatus == WorkflowRunStatusRunning {
		if _, linked := m.operationForWorkflowLocked(chosen.ID); linked {
			m.interruptOperationWorkflowLocked(chosen.ID, now)
		} else {
			m.recoverWorkflowStepsLocked(chosen, now)
		}
	}
	if err := m.syncOperationWorkflowLocked(chosen.ID); err != nil {
		return nil, err
	}

	cp := chosen
	cp.Input = cloneWorkflowJSON(chosen.Input)
	cp.Output = cloneWorkflowJSON(chosen.Output)
	cp.DefinitionSnapshot = cloneWorkflowJSON(chosen.DefinitionSnapshot)
	return &cp, nil
}

func (m *MemStore) ExtendWorkflowRunLease(ctx context.Context, runID string, timeout time.Duration) error {
	if timeout <= 0 {
		return ErrWorkflowInvalidInput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok || run.Status != WorkflowRunStatusRunning {
		return ErrWorkflowNotRunning
	}

	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if m.workflowRunLeases == nil {
		m.workflowRunLeases = make(map[string]time.Time)
	}
	m.workflowRunLeases[runID] = time.Now().UTC().Add(timeout + 5*time.Minute)
	return nil
}

// ScheduleWorkflowRun changes a run's scheduler-visible state and deadline.
func (m *MemStore) ScheduleWorkflowRun(ctx context.Context, id, status string, scheduledFor time.Time) error {
	if err := validateWorkflowRunStatus(status); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.workflowRuns[id]
	if !ok {
		return ErrWorkflowRunNotFound
	}

	if !WorkflowRunGenerationMatches(ctx, id, r.ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if (r.Status == WorkflowRunStatusSucceeded || r.Status == WorkflowRunStatusFailed || r.Status == WorkflowRunStatusDead) && r.Status != status {
		return fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	if status == WorkflowRunStatusAwaitingEvent && r.Status == WorkflowRunStatusAwaitingEvent && r.ScheduledFor.Before(scheduledFor) {
		scheduledFor = r.ScheduledFor
	}
	r.Status = status
	r.ScheduledFor = scheduledFor.UTC()
	r.UpdatedAt = time.Now().UTC()
	m.workflowRuns[id] = r
	return nil
}

func (m *MemStore) SetWorkflowRunWake(ctx context.Context, id, status string, scheduledFor time.Time) error {
	if status != WorkflowRunStatusPending && status != WorkflowRunStatusAwaitingEvent {
		return ErrWorkflowInvalidRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[id]
	if !ok {
		return ErrWorkflowRunNotFound
	}

	if !WorkflowRunGenerationMatches(ctx, id, run.ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return nil
	}
	if run.Status == WorkflowRunStatusPending && !run.ScheduledFor.After(time.Now().UTC()) {
		return nil
	}
	run.Status = status
	run.ScheduledFor = scheduledFor.UTC()
	run.UpdatedAt = time.Now().UTC()
	m.workflowRuns[id] = run
	return nil
}

func (m *MemStore) RecoverWorkflowRun(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[id]
	if !ok {
		return ErrWorkflowRunNotFound
	}

	if !WorkflowRunGenerationMatches(ctx, id, run.ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return nil
	}
	now := time.Now().UTC()
	if _, linked := m.operationForWorkflowLocked(id); linked {
		m.interruptOperationWorkflowLocked(id, now)
	} else {
		m.recoverWorkflowStepsLocked(run, now)
	}
	run.Status = WorkflowRunStatusPending
	run.ScheduledFor = now
	run.UpdatedAt = now
	m.workflowRuns[id] = run
	return m.syncOperationWorkflowLocked(id)
}

func (m *MemStore) CancelWorkflowRun(_ context.Context, id, reason string) (*WorkflowRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[id]
	if !ok {
		return nil, ErrWorkflowRunNotFound
	}
	if op, linked := m.operationForWorkflowLocked(id); linked {
		if _, err := m.cancelOperationWorkflowLocked(op, op.Generation); err != nil {
			return nil, err
		}
		copy := m.workflowRuns[id]
		copy.Input = cloneWorkflowJSON(copy.Input)
		copy.Output = cloneWorkflowJSON(copy.Output)
		copy.DefinitionSnapshot = cloneWorkflowJSON(copy.DefinitionSnapshot)
		copy.CancelledAt = cloneWorkflowTime(copy.CancelledAt)
		return &copy, nil
	}
	if run.Status != WorkflowRunStatusSucceeded && run.Status != WorkflowRunStatusFailed && run.Status != WorkflowRunStatusDead {
		now := time.Now().UTC()
		for key, record := range m.workflowStepAttempts {
			if key.runID == id && record.Status == WorkflowAttemptStatusRunning && (workflowOutboundSpec(run.DefinitionSnapshot, key.stepName) != nil || m.workflowSteps[id][key.stepName].ForEachParent != nil) {
				record.Status = WorkflowAttemptStatusFailed
				record.Error = &reason
				record.FinishedAt = &now
				m.workflowStepAttempts[key] = record
			}
		}
		for name, step := range m.workflowSteps[id] {
			if step.Status == WorkflowStepStatusPending || step.Status == WorkflowStepStatusRunning || step.Status == WorkflowStepStatusAwaitingEvent {
				step.Status = WorkflowStepStatusSkipped
				step.Error = &reason
				step.FinishedAt = &now
				step.NextRetryAt = nil
				m.workflowSteps[id][name] = step
			}
		}
		run.Status = WorkflowRunStatusFailed
		run.LastError = &reason
		run.FinishedAt = &now
		run.CancelledAt = &now
		run.UpdatedAt = now
		m.enqueueWorkflowFinishedWebhookLocked(run, now)
		m.workflowRuns[id] = run
	}
	cp := run
	cp.Input = cloneWorkflowJSON(run.Input)
	cp.Output = cloneWorkflowJSON(run.Output)
	cp.DefinitionSnapshot = cloneWorkflowJSON(run.DefinitionSnapshot)
	return &cp, nil
}

// RetryWorkflowStep requeues a failed HTTP step in the existing run. The
// store lock makes eligibility checks, descendant reopening, quota admission,
// and the terminal-to-pending transition atomic.
func (m *MemStore) RetryWorkflowStep(_ context.Context, runID, stepName string, maxActive int) (*WorkflowRun, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxActive < 1 {
		return nil, 0, ErrWorkflowRunQuotaExceeded
	}
	run, ok := m.workflowRuns[runID]
	if !ok {
		return nil, 0, ErrWorkflowRunNotFound
	}
	if _, linked := m.operationForWorkflowLocked(runID); linked {
		return nil, 0, ErrWorkflowRetryNotAllowed
	}
	steps := m.workflowSteps[runID]
	stepList := make([]*WorkflowStep, 0, len(steps))
	for _, step := range steps {
		copy := step
		stepList = append(stepList, &copy)
	}
	reopen, err := workflowRetryPlan(&run, stepName, stepList)
	if err != nil {
		return nil, 0, err
	}
	active := 0
	for _, existing := range m.workflowRuns {
		if existing.AppID == run.AppID && (existing.Status == WorkflowRunStatusPending || existing.Status == WorkflowRunStatusRunning || existing.Status == WorkflowRunStatusAwaitingEvent) {
			active++
		}
	}
	if active >= maxActive {
		return nil, active, ErrWorkflowRunQuotaExceeded
	}
	failed := steps[stepName]
	failed.Status = WorkflowStepStatusPending
	failed.Output = nil
	failed.NextCheckAt = nil
	failed.NextRetryAt = nil
	failed.FinishedAt = nil
	failed.Error = nil
	steps[stepName] = failed
	for _, descendant := range reopen {
		step := steps[descendant]
		step.Status = WorkflowStepStatusPending
		step.Input = nil
		step.Output = nil
		step.NextCheckAt = nil
		step.NextRetryAt = nil
		step.FinishedAt = nil
		step.Error = nil
		steps[descendant] = step
	}
	now := time.Now().UTC()
	run.Status = WorkflowRunStatusPending
	run.CurrentStep = &stepName
	run.ScheduledFor = now
	run.Output = nil
	run.FinishedAt = nil
	run.LastError = nil
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	delete(m.workflowRunLeases, runID)
	m.workflowSteps[runID] = steps
	cp := run
	cp.Input = cloneWorkflowJSON(run.Input)
	cp.Output = cloneWorkflowJSON(run.Output)
	cp.DefinitionSnapshot = cloneWorkflowJSON(run.DefinitionSnapshot)
	return &cp, active + 1, nil
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
		if step.RetryBase != 0 {
			return ErrWorkflowInvalidRecord
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
		cp.ForEachParent = cloneWorkflowString(s.ForEachParent)
		cp.ForEachIndex = cloneWorkflowInt(s.ForEachIndex)
		cp.ForEachCount = cloneWorkflowInt(s.ForEachCount)
		cp.Input = cloneWorkflowJSON(s.Input)
		cp.Output = cloneWorkflowJSON(s.Output)
		if s.WhenMatched != nil {
			v := *s.WhenMatched
			cp.WhenMatched = &v
		}
		if s.WhenEvaluatedAt != nil {
			v := *s.WhenEvaluatedAt
			cp.WhenEvaluatedAt = &v
		}
		if s.SkipReason != nil {
			v := *s.SkipReason
			cp.SkipReason = &v
		}
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

// StartWorkflowStep persists the resolved input with the running transition.
// A retry therefore reads and reuses the same request body after recovery.
func (m *MemStore) StartWorkflowStep(ctx context.Context, runID, stepName string, attempt int, input json.RawMessage) (json.RawMessage, error) {
	if attempt < 1 {
		return nil, ErrWorkflowInvalidAttempt
	}
	if err := validateWorkflowJSON(input, true); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return nil, ErrWorkflowRunNotFound
	}

	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return nil, ErrWorkflowOutboundAttemptExpired
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return nil, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	steps, ok := m.workflowSteps[runID]
	if !ok {
		return nil, ErrWorkflowStepNotFound
	}
	step, ok := steps[stepName]
	if !ok {
		return nil, ErrWorkflowStepNotFound
	}
	if step.ForEachParent != nil && step.Status == WorkflowStepStatusSkipped {
		return nil, ErrWorkflowGuardNotReady
	}
	if step.Status != WorkflowStepStatusPending && step.Status != WorkflowStepStatusAwaitingEvent {
		if step.ForEachParent != nil {
			return nil, ErrWorkflowOutboundAttemptExpired
		}
		return nil, fmt.Errorf("%w: workflow step is not pending or parked", ErrConflict)
	}
	if actionLimit := workflowRunMaxConcurrentActions(run.DefinitionSnapshot); actionLimit > 0 {
		activeActions := 0
		for otherRunID, otherRun := range m.workflowRuns {
			if otherRun.AppID != run.AppID || otherRun.WorkflowName != run.WorkflowName {
				continue
			}
			for _, otherStep := range m.workflowSteps[otherRunID] {
				if otherStep.Status == WorkflowStepStatusRunning {
					activeActions++
				}
			}
		}
		if activeActions >= actionLimit {
			return nil, ErrWorkflowActionConcurrencyLimit
		}
	}
	if !workflowForEachStartAllowed(run.DefinitionSnapshot, steps, step, input) {
		return nil, ErrWorkflowGuardNotReady
	}
	if workflowJoinTarget(run.DefinitionSnapshot, stepName) != nil {
		return nil, ErrWorkflowGuardNotReady
	}
	if _, err := workflowGuardTarget(run.DefinitionSnapshot, stepName); err == nil && (step.WhenMatched == nil || !*step.WhenMatched) {
		return nil, ErrWorkflowGuardNotReady
	}
	_, linkedOperation := m.operationForWorkflowLocked(runID)
	if (linkedOperation || run.ResumeCount > 0 || workflowOutboundSpec(run.DefinitionSnapshot, stepName) != nil || step.ForEachParent != nil) && (run.Status != WorkflowRunStatusRunning || !m.workflowRunLeases[runID].After(time.Now()) || step.Status != WorkflowStepStatusPending || step.Attempt != attempt-1) {
		return nil, ErrWorkflowOutboundAttemptExpired
	}
	now := time.Now().UTC()
	step.Status = WorkflowStepStatusRunning
	step.Attempt = attempt
	step.outboundAttemptToken = uuid.NewString()
	step.Input = cloneWorkflowJSON(input)
	step.Error = nil
	step.NextRetryAt = nil
	step.FinishedAt = nil
	if step.StartedAt == nil {
		step.StartedAt = &now
	}
	if m.workflowStepAttempts == nil {
		m.workflowStepAttempts = make(map[workflowStepAttemptKey]WorkflowStepAttempt)
	}
	key := workflowStepAttemptKey{runID: runID, stepName: stepName, attempt: attempt}
	attemptRecord, exists := m.workflowStepAttempts[key]
	if !exists {
		attemptRecord = WorkflowStepAttempt{RunID: runID, StepName: stepName, Attempt: attempt, StartedAt: now}
	}
	attemptRecord.Status = WorkflowAttemptStatusRunning
	attemptRecord.HTTPStatus = nil
	attemptRecord.FinishedAt = nil
	attemptRecord.NextAttemptAt = nil
	attemptRecord.Error = nil
	m.workflowStepAttempts[key] = attemptRecord
	steps[stepName] = step
	m.workflowSteps[runID] = steps
	run.CurrentStep = &stepName
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	return cloneWorkflowJSON(step.Input), nil
}

// MarkWorkflowStepStatus updates the execution state of a step.
func (m *MemStore) MarkWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, output json.RawMessage, err *string) error {
	return m.markWorkflowStepStatus(ctx, runID, stepName, status, attempt, nil, nil, output, err)
}

// MarkWorkflowStepAttemptStatus atomically closes an executor attempt and
// updates its compact step summary.
func (m *MemStore) MarkWorkflowStepAttemptStatus(ctx context.Context, runID, stepName, status string, attempt int, httpStatus *int, output json.RawMessage, err *string) error {
	if status != WorkflowStepStatusSucceeded && status != WorkflowStepStatusFailed && status != WorkflowStepStatusDead {
		return fmt.Errorf("%w: attempt completion requires a terminal status", ErrWorkflowInvalidStatus)
	}
	if err := validateWorkflowHTTPStatus(httpStatus); err != nil {
		return err
	}
	attemptStatus := WorkflowAttemptStatusFailed
	if status == WorkflowStepStatusSucceeded {
		attemptStatus = WorkflowAttemptStatusSucceeded
	}
	return m.markWorkflowStepStatus(ctx, runID, stepName, status, attempt, &attemptStatus, httpStatus, output, err)
}

func (m *MemStore) markWorkflowStepStatus(ctx context.Context, runID, stepName, status string, attempt int, attemptStatus *string, httpStatus *int, output json.RawMessage, err *string) error {
	if statusErr := validateWorkflowStepStatus(status); statusErr != nil {
		return statusErr
	}
	if attempt < 0 {
		return ErrWorkflowInvalidAttempt
	}
	if jsonErr := validateWorkflowJSON(output, false); jsonErr != nil {
		return jsonErr
	}
	if statusErr := validateWorkflowHTTPStatus(httpStatus); statusErr != nil {
		return statusErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if !WorkflowRunGenerationMatches(ctx, runID, m.workflowRuns[runID].ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}

	stepsMap, ok := m.workflowSteps[runID]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	step, ok := stepsMap[stepName]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	_, linkedOperation := m.operationForWorkflowLocked(runID)
	if run := m.workflowRuns[runID]; attemptStatus != nil && (linkedOperation || run.ResumeCount > 0 || workflowOutboundSpec(run.DefinitionSnapshot, stepName) != nil || step.ForEachParent != nil) && (run.Status != WorkflowRunStatusRunning || step.Status != WorkflowStepStatusRunning || step.Attempt != attempt || ((linkedOperation || run.ResumeCount > 0 || step.ForEachParent != nil) && !m.workflowRunLeases[runID].After(time.Now()))) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if status == WorkflowStepStatusSucceeded && attemptStatus != nil && linkedOperation {
		if _, deadlineErr := m.workflowOperationWindowLocked(m.workflowRuns[runID], stepName, attempt); deadlineErr != nil {
			return deadlineErr
		}
	}
	if status == WorkflowStepStatusSucceeded && attemptStatus != nil && step.ForEachParent != nil {
		parent := stepsMap[*step.ForEachParent]
		if parent.ForEachCount == nil {
			return ErrWorkflowGuardNotReady
		}
		continueOnFailure := workflowForEachContinuesOnFailure(m.workflowRuns[runID].DefinitionSnapshot, parent.StepName)
		if _, limitErr := workflowForEachOutputs(stepsMap, parent.StepName, *parent.ForEachCount, stepName, output, continueOnFailure); limitErr != nil {
			return limitErr
		}
	}
	if (step.Status == WorkflowStepStatusSucceeded || step.Status == WorkflowStepStatusFailed || step.Status == WorkflowStepStatusDead || step.Status == WorkflowStepStatusSkipped) && step.Status != status {
		return fmt.Errorf("%w: workflow step is terminal", ErrConflict)
	}

	now := time.Now().UTC()
	if attemptStatus != nil {
		key := workflowStepAttemptKey{runID: runID, stepName: stepName, attempt: attempt}
		attemptRecord, exists := m.workflowStepAttempts[key]
		if !exists {
			return ErrWorkflowAttemptNotFound
		}
		attemptRecord.Status = *attemptStatus
		attemptRecord.HTTPStatus = cloneWorkflowInt(httpStatus)
		attemptRecord.FinishedAt = &now
		attemptRecord.NextAttemptAt = nil
		attemptRecord.Error = cloneWorkflowString(err)
		m.workflowStepAttempts[key] = attemptRecord
	}
	step.Status = status
	step.Attempt = attempt
	if len(output) > 0 {
		step.Output = cloneWorkflowJSON(output)
	}
	if status == WorkflowStepStatusRunning || status == WorkflowStepStatusSucceeded {
		step.Error = nil
	} else if err != nil {
		step.Error = err
	}
	if status == WorkflowStepStatusRunning && step.StartedAt == nil {
		step.StartedAt = &now
	}
	if status == WorkflowStepStatusRunning || status == WorkflowStepStatusSucceeded || status == WorkflowStepStatusFailed || status == WorkflowStepStatusDead || status == WorkflowStepStatusSkipped {
		step.NextRetryAt = nil
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

	return m.syncOperationWorkflowLocked(runID)
}

// ScheduleWorkflowStepRetry persists a retry deadline and scheduler wake under
// the same lock, so a crash cannot lose either half of the retry transition.
func (m *MemStore) ScheduleWorkflowStepRetry(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, stepErr string) error {
	return m.scheduleWorkflowStepRetry(ctx, runID, stepName, attempt, retryAt, nil, stepErr, false)
}

// ScheduleWorkflowStepRetryWithHTTPStatus persists the retry and its failed
// executor attempt under the same lock as the scheduler wake.
func (m *MemStore) ScheduleWorkflowStepRetryWithHTTPStatus(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, httpStatus *int, stepErr string) error {
	if err := validateWorkflowHTTPStatus(httpStatus); err != nil {
		return err
	}
	return m.scheduleWorkflowStepRetry(ctx, runID, stepName, attempt, retryAt, httpStatus, stepErr, true)
}

func (m *MemStore) scheduleWorkflowStepRetry(ctx context.Context, runID, stepName string, attempt int, retryAt time.Time, httpStatus *int, stepErr string, recordAttempt bool) error {
	if attempt < 1 || retryAt.IsZero() || stepErr == "" {
		return ErrWorkflowInvalidRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return ErrWorkflowRunNotFound
	}
	if step, ok := m.workflowSteps[runID][stepName]; ok && recordAttempt && (run.ResumeCount > 0 || workflowOutboundSpec(run.DefinitionSnapshot, stepName) != nil || step.ForEachParent != nil) && (run.Status != WorkflowRunStatusRunning || step.Status != WorkflowStepStatusRunning || step.Attempt != attempt || ((run.ResumeCount > 0 || step.ForEachParent != nil) && !m.workflowRunLeases[runID].After(time.Now()))) {
		return ErrWorkflowOutboundAttemptExpired
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return ErrWorkflowOutboundAttemptExpired
	}

	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	step, ok := m.workflowSteps[runID][stepName]
	if !ok {
		return ErrWorkflowStepNotFound
	}
	if step.Status != WorkflowStepStatusRunning {
		return fmt.Errorf("%w: workflow step is not running", ErrConflict)
	}
	now := time.Now().UTC()
	retryDeadline := retryAt.UTC()
	if recordAttempt {
		key := workflowStepAttemptKey{runID: runID, stepName: stepName, attempt: attempt}
		attemptRecord, exists := m.workflowStepAttempts[key]
		if !exists {
			return ErrWorkflowAttemptNotFound
		}
		attemptRecord.Status = WorkflowAttemptStatusRetrying
		attemptRecord.HTTPStatus = cloneWorkflowInt(httpStatus)
		attemptRecord.FinishedAt = &now
		attemptRecord.NextAttemptAt = &retryDeadline
		attemptRecord.Error = cloneWorkflowString(&stepErr)
		m.workflowStepAttempts[key] = attemptRecord
	}
	wakeAt := earlierWorkflowWake(run.ScheduledFor, retryDeadline, now)
	step.Status = WorkflowStepStatusPending
	step.Attempt = attempt
	step.NextRetryAt = &retryDeadline
	step.FinishedAt = nil
	step.Error = &stepErr
	m.workflowSteps[runID][stepName] = step

	run.Status = WorkflowRunStatusPending
	run.CurrentStep = &stepName
	run.ScheduledFor = wakeAt
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	return nil
}

func (m *MemStore) GetWorkflowStepAttempts(_ context.Context, runID, stepName string) ([]*WorkflowStepAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workflowRuns[runID]; !ok {
		return nil, ErrWorkflowRunNotFound
	}
	if _, ok := m.workflowSteps[runID][stepName]; !ok {
		return nil, ErrWorkflowStepNotFound
	}
	attempts := make([]*WorkflowStepAttempt, 0)
	for key, value := range m.workflowStepAttempts {
		if key.runID != runID || key.stepName != stepName {
			continue
		}
		cp := value
		cp.HTTPStatus = cloneWorkflowInt(value.HTTPStatus)
		cp.FinishedAt = cloneWorkflowTime(value.FinishedAt)
		cp.NextAttemptAt = cloneWorkflowTime(value.NextAttemptAt)
		cp.Error = cloneWorkflowString(value.Error)
		for _, effect := range m.workflowOperationEffects[key] {
			delivery, deliveryExists := m.appWebhookDeliveries[effect.Record.DeliveryID]
			receiverExists := false
			for id := range m.appWebhooks {
				if canonicalMemUUID(id) == canonicalMemUUID(effect.Record.WebhookID) {
					receiverExists = true
					break
				}
			}
			cp.Effects = append(cp.Effects, workflowEffectStatusRecord(effect, delivery, deliveryExists, receiverExists))
		}
		attempts = append(attempts, &cp)
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].Attempt < attempts[j].Attempt })
	return attempts, nil
}

// ParkWorkflowTimer updates the step and run under one lock. The existing
// started_at is the deadline anchor when an unrelated event wakes the run.
func (m *MemStore) ParkWorkflowTimer(ctx context.Context, runID, stepName string, duration time.Duration) (time.Time, error) {
	if duration <= 0 {
		return time.Time{}, ErrWorkflowInvalidRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return time.Time{}, ErrWorkflowRunNotFound
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return time.Time{}, ErrWorkflowOutboundAttemptExpired
	}

	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return time.Time{}, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	steps, ok := m.workflowSteps[runID]
	if !ok {
		return time.Time{}, ErrWorkflowStepNotFound
	}
	step, ok := steps[stepName]
	if !ok {
		return time.Time{}, ErrWorkflowStepNotFound
	}
	if step.Status != WorkflowStepStatusPending && step.Status != WorkflowStepStatusAwaitingEvent {
		return time.Time{}, fmt.Errorf("%w: timer step is not pending or awaiting", ErrConflict)
	}
	now := time.Now().UTC()
	if step.StartedAt == nil {
		step.StartedAt = &now
	}
	deadline := step.StartedAt.Add(duration)
	deadline = earlierWorkflowWake(run.ScheduledFor, deadline, now)
	step.Status = WorkflowStepStatusAwaitingEvent
	steps[stepName] = step
	run.Status = WorkflowRunStatusAwaitingEvent
	run.CurrentStep = &stepName
	run.ScheduledFor = deadline
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	return deadline, nil
}

func (m *MemStore) ParkWorkflowEvent(ctx context.Context, runID, stepName, eventName string, timeout time.Duration) (*WorkflowEvent, time.Time, error) {
	if eventName == "" || timeout <= 0 {
		return nil, time.Time{}, ErrWorkflowInvalidRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return nil, time.Time{}, ErrWorkflowRunNotFound
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return nil, time.Time{}, ErrWorkflowOutboundAttemptExpired
	}

	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return nil, time.Time{}, fmt.Errorf("%w: workflow run is terminal", ErrConflict)
	}
	step, ok := m.workflowSteps[runID][stepName]
	if !ok {
		return nil, time.Time{}, ErrWorkflowStepNotFound
	}
	if step.Status != WorkflowStepStatusPending && step.Status != WorkflowStepStatusAwaitingEvent {
		return nil, time.Time{}, fmt.Errorf("%w: event wait step is not pending or awaiting", ErrConflict)
	}
	var matched *WorkflowEvent
	var deadline time.Time
	if step.StartedAt != nil {
		deadline = step.StartedAt.Add(timeout)
	}
	for _, event := range m.workflowEvents[runID] {
		if event.EventName != eventName || (!deadline.IsZero() && !event.ReceivedAt.Before(deadline)) {
			continue
		}
		if matched == nil || event.ReceivedAt.Before(matched.ReceivedAt) || (event.ReceivedAt.Equal(matched.ReceivedAt) && event.ID < matched.ID) {
			cp := event
			matched = &cp
		}
	}
	if matched != nil {
		matched.Payload = cloneWorkflowJSON(matched.Payload)
		return matched, time.Time{}, nil
	}
	now := time.Now().UTC()
	if step.StartedAt == nil {
		step.StartedAt = &now
	}
	deadline = step.StartedAt.Add(timeout)
	wakeAt := deadline
	wakeAt = earlierWorkflowWake(run.ScheduledFor, wakeAt, now)
	step.Status = WorkflowStepStatusAwaitingEvent
	m.workflowSteps[runID][stepName] = step
	run.Status = WorkflowRunStatusAwaitingEvent
	run.CurrentStep = &stepName
	run.ScheduledFor = wakeAt
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	return nil, deadline, nil
}

func (m *MemStore) ResolveWorkflowEventWait(ctx context.Context, runID, stepName, eventName string, timeout time.Duration, onTimeout bool) (*WorkflowEvent, bool, error) {
	if eventName == "" || timeout <= 0 {
		return nil, false, ErrWorkflowInvalidRecord
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return nil, false, ErrWorkflowRunNotFound
	}
	if !WorkflowRunGenerationMatches(ctx, runID, run.ResumeCount) {
		return nil, false, ErrWorkflowOutboundAttemptExpired
	}

	step, ok := m.workflowSteps[runID][stepName]
	if !ok {
		return nil, false, ErrWorkflowStepNotFound
	}
	if step.Status != WorkflowStepStatusAwaitingEvent || step.StartedAt == nil {
		return nil, false, fmt.Errorf("%w: workflow event wait is not active", ErrConflict)
	}
	deadline := step.StartedAt.Add(timeout)
	for _, event := range m.workflowEvents[runID] {
		if event.EventName == eventName && event.ReceivedAt.Before(deadline) {
			cp := event
			cp.Payload = cloneWorkflowJSON(event.Payload)
			return &cp, false, nil
		}
	}
	now := time.Now().UTC()
	if now.Before(deadline) {
		return nil, false, nil
	}
	step.FinishedAt = &now
	if onTimeout {
		step.Status = WorkflowStepStatusSucceeded
		step.Output = json.RawMessage(`{"timeout":true}`)
		run.Status = WorkflowRunStatusPending
		run.ScheduledFor = now
	} else {
		message := "workflow event or callback wait timed out with no handler"
		step.Status = WorkflowStepStatusDead
		step.Error = &message
		run.Status = WorkflowRunStatusDead
		run.LastError = &message
		run.FinishedAt = &now
		m.enqueueWorkflowFinishedWebhookLocked(run, now)
	}
	m.workflowSteps[runID][stepName] = step
	run.CurrentStep = &stepName
	run.UpdatedAt = now
	m.workflowRuns[runID] = run
	return nil, true, nil
}

func (m *MemStore) CompleteWorkflowCallback(ctx context.Context, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	return m.completeWorkflowCallback(ctx, "", runID, stepName, eventName, eventID, timeout, payload)
}

func (m *MemStore) CompleteTenantWorkflowCallback(ctx context.Context, tenantID, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	if tenantID == "" {
		return false, ErrWorkflowInvalidRecord
	}
	return m.completeWorkflowCallback(ctx, tenantID, runID, stepName, eventName, eventID, timeout, payload)
}

func (m *MemStore) completeWorkflowCallback(_ context.Context, tenantID, runID, stepName, eventName, eventID string, timeout time.Duration, payload json.RawMessage) (bool, error) {
	if runID == "" || stepName == "" || eventName == "" || eventID == "" || timeout <= 0 {
		return false, ErrWorkflowInvalidRecord
	}
	if err := validateWorkflowJSON(payload, false); err != nil {
		return false, err
	}
	if len(payload) == 0 {
		payload = json.RawMessage("{}")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.workflowRuns[runID]
	if !ok {
		return false, ErrWorkflowRunNotFound
	}
	if tenantID != "" {
		app := m.apps[run.AppID]
		if canonicalMemUUID(run.PlatformTenantID) != canonicalMemUUID(tenantID) ||
			!m.workflowOutboundTenantLinkActiveLocked(app.AccountID, tenantID, app.ID) {
			return false, ErrWorkflowRunNotFound
		}
	}
	for _, events := range m.workflowEvents {
		for _, event := range events {
			if event.ID != eventID {
				continue
			}
			if event.RunID == runID && event.EventName == eventName && equalWorkflowJSON(event.Payload, payload) {
				return true, nil
			}
			return false, fmt.Errorf("%w: workflow callback payload differs", ErrConflict)
		}
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		return false, ErrWorkflowCallbackClosed
	}
	now := time.Now().UTC()
	if step, exists := m.workflowSteps[runID][stepName]; exists {
		if step.Status == WorkflowStepStatusSucceeded || step.Status == WorkflowStepStatusFailed || step.Status == WorkflowStepStatusDead || step.Status == WorkflowStepStatusSkipped {
			return false, ErrWorkflowCallbackClosed
		}
		if step.StartedAt != nil && !now.Before(step.StartedAt.Add(timeout)) {
			return false, ErrWorkflowCallbackExpired
		}
	}
	m.workflowEvents[runID] = append(m.workflowEvents[runID], WorkflowEvent{
		ID: eventID, RunID: runID, EventName: eventName,
		Payload: cloneWorkflowJSON(payload), ReceivedAt: now,
	})
	if run.Status == WorkflowRunStatusAwaitingEvent {
		run.Status = WorkflowRunStatusPending
		run.ScheduledFor = now
		run.UpdatedAt = now
		m.workflowRuns[runID] = run
	}
	return false, nil
}

// InsertWorkflowEvent appends an external event to a run's event log.
func (m *MemStore) InsertWorkflowEvent(ctx context.Context, e *WorkflowEvent) error {
	return m.insertWorkflowEvent(ctx, "", e)
}

func (m *MemStore) InsertTenantWorkflowEvent(ctx context.Context, tenantID string, e *WorkflowEvent) error {
	if tenantID == "" {
		return ErrWorkflowInvalidRecord
	}
	return m.insertWorkflowEvent(ctx, tenantID, e)
}

func (m *MemStore) insertWorkflowEvent(_ context.Context, tenantID string, e *WorkflowEvent) error {
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
	run, ok := m.workflowRuns[e.RunID]
	if !ok {
		return ErrWorkflowRunNotFound
	}
	if tenantID != "" {
		app := m.apps[run.AppID]
		if canonicalMemUUID(run.PlatformTenantID) != canonicalMemUUID(tenantID) ||
			!m.workflowOutboundTenantLinkActiveLocked(app.AccountID, tenantID, app.ID) {
			return ErrWorkflowRunNotFound
		}
		if run.Status != WorkflowRunStatusPending && run.Status != WorkflowRunStatusRunning && run.Status != WorkflowRunStatusAwaitingEvent {
			return ErrWorkflowNotRunning
		}
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
				if existing.RunID != e.RunID || existing.EventName != e.EventName {
					return fmt.Errorf("%w: workflow_events.id", ErrConflict)
				}
				if run := m.workflowRuns[e.RunID]; run.Status == WorkflowRunStatusAwaitingEvent {
					run.Status = WorkflowRunStatusPending
					run.ScheduledFor = time.Now().UTC()
					run.UpdatedAt = run.ScheduledFor
					m.workflowRuns[e.RunID] = run
				}
				return nil
			}
		}
	}

	stored := *e
	stored.Payload = cloneWorkflowJSON(e.Payload)
	m.workflowEvents[e.RunID] = append(m.workflowEvents[e.RunID], stored)
	if run := m.workflowRuns[e.RunID]; run.Status == WorkflowRunStatusAwaitingEvent {
		run.Status = WorkflowRunStatusPending
		run.ScheduledFor = time.Now().UTC()
		run.UpdatedAt = run.ScheduledFor
		m.workflowRuns[e.RunID] = run
	}
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
		_, retainedByOperation := m.operationForWorkflowLocked(id)
		if r.FinishedAt != nil && !r.FinishedAt.After(threshold) && !retainedByOperation {
			delete(m.workflowRuns, id)
			delete(m.workflowSteps, id)
			delete(m.workflowEvents, id)
			for key := range m.workflowStepAttempts {
				if key.runID == id {
					delete(m.workflowStepAttempts, key)
				}
			}
			for bindingID, binding := range m.workflowCallbackWebhookBindings {
				if binding.RunID == id {
					delete(m.workflowCallbackWebhookBindings, bindingID)
				}
			}
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
		if run, ok := m.workflowRuns[runID]; ok && run.FinishedAt == nil {
			continue
		}
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
