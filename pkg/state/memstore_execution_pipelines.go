package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"sort"
	"time"
)

var _ ExecutionPipelineStore = (*MemStore)(nil)
var pipelineNever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

func (m *MemStore) CreateExecutionPipeline(_ context.Context, p CreateExecutionWorkflowJobParams, policy ExecutionPipelinePolicy) (ExecutionWorkflowJob, error) {
	if err := validateExecutionWorkflowJobParams(p); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	if err := validatePipelinePolicy(policy, p.StepCount); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if policy.Rerun != nil {
		parent, ok := m.executionWorkflowJobs[policy.Rerun.ParentID]
		meta, exists := m.executionPipelines[policy.Rerun.ParentID]
		if !ok || !exists {
			return ExecutionWorkflowJob{}, ErrNotFound
		}
		if err := validatePipelineRerun(parent, meta, p, policy); err != nil {
			return ExecutionWorkflowJob{}, err
		}
	}
	if policy.Callback != nil {
		ep, ok := m.managedRealtimeEndpoints[policy.Callback.EndpointID]
		if !ok || ep.AccountID != p.AccountID {
			return ExecutionWorkflowJob{}, ErrNotFound
		}
		for _, step := range policy.Steps {
			v, ok := m.executionTaskVersions[executionTaskKey(p.AccountID, step.Task.Name, step.Task.Version)]
			if !ok || v.ID != step.Task.ID || v.Digest != step.Task.Digest || v.Status != "active" || !slices.Contains(v.Metadata.AgentEndpointIDs, policy.Callback.EndpointID) {
				return ExecutionWorkflowJob{}, ErrExecutionTaskUnavailable
			}
		}
	}
	journal, err := initialPipelineJournal(policy, p.CreatedAt)
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	job, err := m.createExecutionWorkflowJobLocked(p)
	if err != nil {
		return job, err
	}
	job.ScheduledFor = pipelineNever
	m.executionWorkflowJobs[job.ID] = job
	m.executionPipelines[job.ID] = clonePipeline(ExecutionPipeline{Journal: journal, JobID: job.ID, Policy: policy, DeadlineAt: p.CreatedAt.Add(time.Duration(policy.TotalTimeoutMS) * time.Millisecond), DueAt: p.CreatedAt})
	return cloneExecutionWorkflowJob(job), nil
}
func (m *MemStore) ExecutionPipelineByID(_ context.Context, id string) (ExecutionPipeline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.executionPipelines[id]
	if !ok {
		return p, ErrNotFound
	}
	return clonePipeline(p), nil
}
func (m *MemStore) ClaimExecutionPipeline(_ context.Context, owner string, at time.Time, lease time.Duration) (ExecutionWorkflowJobClaim, error) {
	if owner == "" || at.IsZero() || lease <= 0 {
		return ExecutionWorkflowJobClaim{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, p := range m.executionPipelines {
		j := m.executionWorkflowJobs[id]
		if (j.Status == api.ManagedExecutionWorkflowQueued || j.Status == api.ManagedExecutionWorkflowRunning) && !p.DueAt.After(at) && (j.LeaseExpiresAt == nil || !j.LeaseExpiresAt.After(at)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.executionPipelines[ids[i]], m.executionPipelines[ids[j]]
		if a.DueAt.Equal(b.DueAt) {
			return ids[i] < ids[j]
		}
		return a.DueAt.Before(b.DueAt)
	})
	if len(ids) == 0 {
		return ExecutionWorkflowJobClaim{}, ErrNotFound
	}
	j := m.executionWorkflowJobs[ids[0]]
	token := uuid.NewString()
	expires := at.Add(lease)
	j.Status = api.ManagedExecutionWorkflowRunning
	j.LeaseToken = &token
	j.LeaseOwner = &owner
	j.LeaseExpiresAt = &expires
	j.UpdatedAt = at
	m.executionWorkflowJobs[j.ID] = j
	return ExecutionWorkflowJobClaim{ExecutionWorkflowJob: cloneExecutionWorkflowJob(j), ClaimToken: token}, nil
}
func (m *MemStore) UpdateExecutionPipeline(_ context.Context, id, token string, u ExecutionWorkflowJobUpdate) error {
	if u.UpdatedAt.IsZero() || u.ScheduledFor.IsZero() || len(u.LastError) > 2048 || (u.Status != api.ManagedExecutionWorkflowQueued && u.Status != api.ManagedExecutionWorkflowSucceeded && u.Status != api.ManagedExecutionWorkflowFailed) {
		return ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.executionWorkflowJobs[id]
	if !ok {
		return ErrNotFound
	}
	p, ok := m.executionPipelines[id]
	if !ok {
		return ErrNotFound
	}
	if j.Status != api.ManagedExecutionWorkflowRunning || j.LeaseToken == nil || *j.LeaseToken != token {
		return ErrExecutionWorkflowLeaseLost
	}
	if u.NextStep < 0 || u.NextStep > j.StepCount {
		return ErrExecutionInvalid
	}
	if err := validPipelineCompletion(j, p, u); err != nil {
		return err
	}
	if err := advancePipelineProgress(&p, u.PipelineProgress, u.UpdatedAt); err != nil {
		return err
	}
	j.Status = u.Status
	j.NextStep = u.NextStep
	j.LastError = u.LastError
	j.UpdatedAt = u.UpdatedAt
	j.LeaseToken = nil
	j.LeaseOwner = nil
	j.LeaseExpiresAt = nil
	p.DueAt = u.ScheduledFor
	if u.Status == api.ManagedExecutionWorkflowSucceeded || u.Status == api.ManagedExecutionWorkflowFailed {
		finished := u.UpdatedAt
		j.FinishedAt = &finished
		clear(j.SealedPlan)
		j.SealedPlan = nil
		j.PayloadKID = ""
	}
	p.Completion = u.PipelineCompletion
	p = clonePipeline(p)
	m.executionWorkflowJobs[id] = j
	m.executionPipelines[id] = p
	if p.Completion != nil {
		m.enqueuePipelineFinishedLocked(j, *p.Completion)
	}
	return nil
}
func (m *MemStore) CancelExecutionPipeline(_ context.Context, account, workflow string, principal *string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	found := false
	for id, p := range m.executionPipelines {
		j := m.executionWorkflowJobs[id]
		if j.AccountID == account && j.WorkflowID == workflow && (principal == nil || sameOptionalString(j.RunsPrincipalID, principal)) {
			found = true
			if j.Status == api.ManagedExecutionWorkflowQueued || j.Status == api.ManagedExecutionWorkflowRunning {
				p.CancelRequested = true
				if at.Before(p.DueAt) {
					p.DueAt = at
				}
				m.executionPipelines[id] = p
			}
		}
	}
	if !found {
		return ErrNotFound
	}
	return nil
}
func (m *MemStore) validatePipelineAdmissionLocked(p CreateExecutionParams) error {
	if p.Pipeline == nil {
		return nil
	}
	j, ok := m.executionWorkflowJobs[p.Pipeline.JobID]
	if !ok {
		return ErrExecutionInvalid
	}
	meta, ok := m.executionPipelines[j.ID]
	if !ok {
		return ErrExecutionInvalid
	}
	var previous *Execution
	if p.Pipeline.PreviousRunID != "" {
		r, ok := m.executions[p.Pipeline.PreviousRunID]
		if !ok {
			return ErrExecutionInvalid
		}
		previous = &r
	}
	return validatePipelineAdmission(p, j, meta, previous, time.Now().UTC())
}
