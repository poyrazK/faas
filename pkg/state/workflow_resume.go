package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	ErrWorkflowResumeConflict    = errors.New("workflow resume revision or status changed")
	ErrWorkflowResumeUnsafe      = errors.New("workflow cannot be safely resumed")
	ErrWorkflowResumeLimit       = errors.New("workflow resume limit reached")
	ErrWorkflowResumeUnavailable = errors.New("workflow resume target unavailable")
)

type WorkflowResume struct {
	RunID          string    `json:"run_id"`
	ResumeNumber   int       `json:"resume_number"`
	AccountID      string    `json:"account_id"`
	PreviousStatus string    `json:"previous_status"`
	PreviousError  *string   `json:"previous_error,omitempty"`
	ResumedSteps   []string  `json:"resumed_steps"`
	CreatedAt      time.Time `json:"created_at"`
}

type WorkflowResumeOptions struct {
	RunID, AppID, AccountID string
	PlatformTenantID        string
	ExpectedResumeCount     int
}

type WorkflowResumeStore interface {
	ResumeWorkflowRun(context.Context, WorkflowResumeOptions) (*WorkflowRun, *WorkflowResume, int, error)
	ListWorkflowResumes(context.Context, string) ([]WorkflowResume, error)
}

type workflowGenerationKey struct{}
type workflowGeneration struct {
	id    string
	count int
}

// WithWorkflowRunGeneration pins a scheduler invocation to its first observed
// resume generation. Nested advancement must not silently refresh that fence.
func WithWorkflowRunGeneration(ctx context.Context, id string, count int) context.Context {
	if prior, ok := ctx.Value(workflowGenerationKey{}).(workflowGeneration); ok && prior.id == id {
		return ctx
	}
	return context.WithValue(ctx, workflowGenerationKey{}, workflowGeneration{id, count})
}
func WorkflowRunGenerationMatches(ctx context.Context, id string, count int) bool {
	value, ok := ctx.Value(workflowGenerationKey{}).(workflowGeneration)
	return !ok || value.id != id || value.count == count
}
func workflowGenerationArg(ctx context.Context, id string) pgtype.Int4 {
	value, ok := ctx.Value(workflowGenerationKey{}).(workflowGeneration)
	return pgtype.Int4{Int32: int32(value.count), Valid: ok && value.id == id}
}
func checkWorkflowGenerationTx(ctx context.Context, db sqlc.DBTX, id string) error {
	if !workflowGenerationArg(ctx, id).Valid {
		return nil
	}
	current, err := sqlc.New().WorkflowGenerationCurrent(ctx, db, sqlc.WorkflowGenerationCurrentParams{RunID: mustPgUUID(id), Generation: workflowGenerationArg(ctx, id)})
	if err != nil {
		return err
	}
	if !current {
		return ErrWorkflowOutboundAttemptExpired
	}
	return nil
}

// Build the continuation from persisted state alone. Completed actions and
// durable guard decisions stay intact. Only failed actions, their batch parent,
// and descendants skipped because of those failures are reopened.
func workflowResumePlan(run WorkflowRun, steps map[string]WorkflowStep, expected int, plan api.Plan) (api.WorkflowSpec, []string, error) {
	var spec api.WorkflowSpec
	if expected < 0 || run.ResumeCount != expected || (run.Status != WorkflowRunStatusFailed && run.Status != WorkflowRunStatusDead) {
		return spec, nil, ErrWorkflowResumeConflict
	}
	if run.ResumeCount >= api.WorkflowRunMaxResumes {
		return spec, nil, ErrWorkflowResumeLimit
	}
	if run.CancelledAt != nil || run.LastError != nil && *run.LastError == "cancelled by operator" {
		return spec, nil, ErrWorkflowResumeUnsafe
	}
	if json.Unmarshal(run.DefinitionSnapshot, &spec) != nil {
		return spec, nil, ErrWorkflowResumeUnsafe
	}
	if _, err := api.ValidateWorkflowDAG(spec, plan); err != nil {
		return spec, nil, ErrWorkflowResumeUnsafe
	}
	for _, action := range spec.Steps {
		step, exists := steps[action.Name]
		if !exists || step.ForEachParent != nil {
			return spec, nil, ErrWorkflowResumeUnsafe
		}
		if action.ForEach != nil && step.ForEachCount != nil {
			for index := range *step.ForEachCount {
				child, ok := steps[api.WorkflowForEachItemName(action.Name, index)]
				if !ok || child.ForEachParent == nil || *child.ForEachParent != action.Name || child.ForEachIndex == nil || *child.ForEachIndex != index {
					return spec, nil, ErrWorkflowResumeUnsafe
				}
			}
		}
	}

	// Compensation or timeout-handler effects invalidate the original happy path.
	routed := map[string]bool{}
	for _, s := range spec.Steps {
		if s.OnFailure != "" {
			routed[s.OnFailure] = true
		}
		if s.OnTimeout != "" {
			routed[s.OnTimeout] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, s := range spec.Steps {
			for _, dep := range s.DependsOn {
				if routed[dep] && !routed[s.Name] {
					routed[s.Name] = true
					changed = true
				}
			}
		}
	}
	reset := map[string]bool{}
	for name, step := range steps {
		if step.Status == WorkflowStepStatusSkipped && step.SkipReason == nil {
			return spec, nil, ErrWorkflowResumeUnsafe
		}
		if step.Status == WorkflowStepStatusRunning || step.Status == WorkflowStepStatusAwaitingEvent {
			return spec, nil, ErrWorkflowResumeUnsafe
		}
		if routed[name] && (step.Attempt > 0 || step.Status == WorkflowStepStatusSucceeded || step.Status == WorkflowStepStatusFailed || step.Status == WorkflowStepStatusDead) {
			return spec, nil, ErrWorkflowResumeUnsafe
		}
		if step.Status != WorkflowStepStatusFailed && step.Status != WorkflowStepStatusDead {
			continue
		}
		action := api.WorkflowRuntimeStep(run.DefinitionSnapshot, name)
		if action == nil {
			return spec, nil, ErrWorkflowResumeUnsafe
		}
		if action.ForEach != nil {
			if step.ForEachCount == nil {
				return spec, nil, ErrWorkflowResumeUnsafe
			}
			found := false
			for _, child := range steps {
				if child.ForEachParent != nil && *child.ForEachParent == name && (child.Status == WorkflowStepStatusFailed || child.Status == WorkflowStepStatusDead) {
					found = true
				}
			}
			if !found {
				return spec, nil, ErrWorkflowResumeUnsafe
			}
		} else if step.Attempt == 0 || action.WaitForCondition != nil || action.WaitForCallback || action.WaitForEvent != "" || action.WaitForDuration > 0 || action.Join != nil || (action.Run == "" && action.Path == "" && action.Outbound == nil) {
			return spec, nil, ErrWorkflowResumeUnsafe
		}
		if action.Outbound != nil && step.Attempt > 0 && !action.Outbound.SafeToRepeat() {
			return spec, nil, ErrWorkflowResumeUnsafe
		}
		reset[name] = true
	}
	if len(reset) == 0 {
		return spec, nil, ErrWorkflowResumeUnsafe
	}
	for changed := true; changed; {
		changed = false
		for name, step := range steps {
			if reset[name] || step.Status != WorkflowStepStatusSkipped || step.SkipReason == nil {
				continue
			}
			affected := step.ForEachParent != nil && reset[*step.ForEachParent] && *step.SkipReason == WorkflowSkipDependencyFailed
			if step.ForEachParent == nil && (*step.SkipReason == WorkflowSkipDependencyFailed || *step.SkipReason == WorkflowSkipDependencySkipped) {
				action := api.WorkflowRuntimeStep(run.DefinitionSnapshot, name)
				if action == nil {
					return spec, nil, ErrWorkflowResumeUnsafe
				}
				for _, dep := range action.DependsOn {
					affected = affected || reset[dep]
				}
			}
			if affected {
				reset[name] = true
				changed = true
			}
		}
	}
	names := make([]string, 0, len(reset))
	for name := range reset {
		names = append(names, name)
	}
	sort.Strings(names)
	return spec, names, nil
}

func cloneWorkflowResume(value WorkflowResume) WorkflowResume {
	value.PreviousError = cloneWorkflowString(value.PreviousError)
	value.ResumedSteps = append([]string(nil), value.ResumedSteps...)
	return value
}
func (m *MemStore) ListWorkflowResumes(_ context.Context, id string) ([]WorkflowResume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.workflowRuns[id]; !ok {
		return nil, ErrWorkflowRunNotFound
	}
	records := make([]WorkflowResume, 0, len(m.workflowResumes[id]))
	for _, record := range m.workflowResumes[id] {
		records = append(records, cloneWorkflowResume(record))
	}
	return records, nil
}
func (m *MemStore) ResumeWorkflowRun(_ context.Context, opts WorkflowResumeOptions) (*WorkflowRun, *WorkflowResume, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resumeWorkflowRunLocked(opts, false)
}

func (m *MemStore) resumeWorkflowRunLocked(opts WorkflowResumeOptions, operation bool) (*WorkflowRun, *WorkflowResume, int, error) {
	if _, linked := m.operationForWorkflowLocked(opts.RunID); linked && !operation {
		return nil, nil, 0, ErrWorkflowResumeUnsafe
	}
	run, ok := m.workflowRuns[opts.RunID]
	app, appOK := m.apps[opts.AppID]
	account := m.accounts[opts.AccountID]
	if !ok || !appOK || run.AppID != app.ID || app.AccountID != opts.AccountID {
		return nil, nil, 0, ErrWorkflowRunNotFound
	}
	if opts.PlatformTenantID != "" && run.PlatformTenantID != opts.PlatformTenantID {
		return nil, nil, 0, ErrWorkflowRunNotFound
	}
	if !account.Active() || !account.Plan.WorkflowsAllowed() || app.Status == AppDeleted || app.MaintenanceMode || app.PlatformTenantRequired && run.PlatformTenantID == "" {
		return nil, nil, 0, ErrWorkflowResumeUnavailable
	}
	live := false
	for _, dep := range m.deployments {
		if dep.AppID == app.ID && dep.Status == "live" && dep.Scope == "default" {
			live = true
		}
	}
	if !live && !operation {
		return nil, nil, 0, ErrWorkflowResumeUnavailable
	}
	spec, names, err := workflowResumePlan(run, m.workflowSteps[run.ID], opts.ExpectedResumeCount, account.Plan)
	if err != nil {
		return nil, nil, 0, err
	}
	for key, record := range m.workflowStepAttempts {
		if key.runID == run.ID && record.Status == WorkflowAttemptStatusRunning {
			return nil, nil, 0, ErrWorkflowResumeUnsafe
		}
	}
	if err := m.validateWorkflowOutboundLocked(app.ID, account.ID, spec); err != nil {
		return nil, nil, 0, ErrWorkflowResumeUnavailable
	}
	active := 0
	for _, other := range m.workflowRuns {
		if other.AppID == app.ID && workflowGuardRunActive(other.Status) {
			active++
		}
	}
	if active >= account.Plan.WorkflowMaxConcurrentRuns() {
		return nil, nil, active, ErrWorkflowRunQuotaExceeded
	}
	now := time.Now().UTC()
	record := WorkflowResume{RunID: run.ID, ResumeNumber: run.ResumeCount + 1, AccountID: account.ID, PreviousStatus: run.Status, PreviousError: cloneWorkflowString(run.LastError), ResumedSteps: names, CreatedAt: now}
	for _, name := range names {
		step := m.workflowSteps[run.ID][name]
		step.Status, step.RetryBase, step.Output, step.Error = WorkflowStepStatusPending, step.Attempt, nil, nil
		step.FinishedAt, step.NextRetryAt, step.NextCheckAt, step.SkipReason = nil, nil, nil, nil
		step.outboundAttemptToken = ""
		m.workflowSteps[run.ID][name] = step
	}
	run.Status, run.ResumeCount, run.ScheduledFor, run.UpdatedAt = WorkflowRunStatusPending, run.ResumeCount+1, now, now
	run.FinishedAt, run.LastError, run.Output, run.CurrentStep = nil, nil, nil, nil
	m.workflowRuns[run.ID] = run
	delete(m.workflowRunLeases, run.ID)
	if m.workflowResumes == nil {
		m.workflowResumes = make(map[string][]WorkflowResume)
	}
	m.workflowResumes[run.ID] = append(m.workflowResumes[run.ID], record)
	run.Input = cloneWorkflowJSON(run.Input)
	run.DefinitionSnapshot = cloneWorkflowJSON(run.DefinitionSnapshot)
	record = cloneWorkflowResume(record)
	return &run, &record, active, nil
}

func (s *PgStore) ListWorkflowResumes(ctx context.Context, id string) ([]WorkflowResume, error) {
	rows, err := sqlc.New().ListWorkflowResumes(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return nil, fmt.Errorf("list workflow resumes: %w", err)
	}
	records := make([]WorkflowResume, 0, len(rows))
	for _, row := range rows {
		record := WorkflowResume{RunID: id, ResumeNumber: int(row.ResumeNumber), AccountID: pgUUIDString(row.AccountID), PreviousStatus: row.PreviousStatus, PreviousError: workflowResumeTextPtr(row.PreviousError), CreatedAt: row.CreatedAt.Time}
		if err := json.Unmarshal(row.ResumedSteps, &record.ResumedSteps); err != nil {
			return nil, fmt.Errorf("decode workflow resume: %w", err)
		}
		records = append(records, record)
	}
	return records, nil
}
func workflowResumeTextPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
func workflowResumeTimePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
func workflowResumeIntPtr(value pgtype.Int4) *int {
	if !value.Valid {
		return nil
	}
	v := int(value.Int32)
	return &v
}
