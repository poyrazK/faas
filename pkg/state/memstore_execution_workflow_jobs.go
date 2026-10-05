package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ExecutionWorkflowJobStore = (*MemStore)(nil)

func (m *MemStore) CreateExecutionWorkflowJob(_ context.Context, params CreateExecutionWorkflowJobParams) (ExecutionWorkflowJob, error) {
	if err := validateExecutionWorkflowJobParams(params); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[params.AccountID]; !ok {
		return ExecutionWorkflowJob{}, ErrNotFound
	}
	for _, existing := range m.executionWorkflowJobs {
		if existing.AccountID == params.AccountID && existing.WorkflowID == params.WorkflowID && sameOptionalString(existing.RunsPrincipalID, params.RunsPrincipalID) {
			return ExecutionWorkflowJob{}, ErrExecutionWorkflowJobExists
		}
	}
	active := 0
	for _, existing := range m.executionWorkflowJobs {
		if existing.AccountID == params.AccountID &&
			(existing.Status == api.ManagedExecutionWorkflowQueued || existing.Status == api.ManagedExecutionWorkflowRunning) {
			active++
		}
	}
	if active >= ExecutionWorkflowManagedMaxActivePerAccount {
		return ExecutionWorkflowJob{}, ErrExecutionWorkflowQueueFull
	}
	row := ExecutionWorkflowJob{
		ID: uuid.NewString(), AccountID: params.AccountID,
		RunsPrincipalID: cloneStringPtr(params.RunsPrincipalID), WorkflowID: params.WorkflowID,
		PlanID: params.PlanID, Status: api.ManagedExecutionWorkflowQueued,
		StepCount: params.StepCount, SealedPlan: append([]byte(nil), params.SealedPlan...),
		PayloadKID: params.PayloadKID, ScheduledFor: params.CreatedAt.UTC(),
		CreatedAt: params.CreatedAt.UTC(), UpdatedAt: params.CreatedAt.UTC(),
	}
	m.executionWorkflowJobs[row.ID] = row
	return cloneExecutionWorkflowJob(row), nil
}

func (m *MemStore) ClaimExecutionWorkflowJob(_ context.Context, owner string, at time.Time, leaseDuration time.Duration) (ExecutionWorkflowJobClaim, error) {
	if owner == "" || at.IsZero() || leaseDuration <= 0 {
		return ExecutionWorkflowJobClaim{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.executionWorkflowJobs))
	for id, row := range m.executionWorkflowJobs {
		if (row.Status == api.ManagedExecutionWorkflowQueued || row.Status == api.ManagedExecutionWorkflowRunning) &&
			!row.ScheduledFor.After(at) && (row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(at)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.executionWorkflowJobs[ids[i]], m.executionWorkflowJobs[ids[j]]
		if a.ScheduledFor.Equal(b.ScheduledFor) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ScheduledFor.Before(b.ScheduledFor)
	})
	if len(ids) == 0 {
		return ExecutionWorkflowJobClaim{}, ErrNotFound
	}
	row := m.executionWorkflowJobs[ids[0]]
	token := uuid.NewString()
	leaseOwner, expires := owner, at.UTC().Add(leaseDuration)
	row.Status = api.ManagedExecutionWorkflowRunning
	row.LeaseToken, row.LeaseOwner, row.LeaseExpiresAt = &token, &leaseOwner, &expires
	row.UpdatedAt = at.UTC()
	m.executionWorkflowJobs[row.ID] = row
	return ExecutionWorkflowJobClaim{ExecutionWorkflowJob: cloneExecutionWorkflowJob(row), ClaimToken: token}, nil
}

func (m *MemStore) UpdateExecutionWorkflowJob(_ context.Context, id, leaseToken string, update ExecutionWorkflowJobUpdate) error {
	if update.Status != api.ManagedExecutionWorkflowQueued && update.Status != api.ManagedExecutionWorkflowRunning &&
		update.Status != api.ManagedExecutionWorkflowSucceeded && update.Status != api.ManagedExecutionWorkflowFailed {
		return ErrExecutionInvalid
	}
	if update.UpdatedAt.IsZero() || update.ScheduledFor.IsZero() || len(update.LastError) > 2048 {
		return ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executionWorkflowJobs[id]
	if !ok {
		return ErrNotFound
	}
	if row.Status != api.ManagedExecutionWorkflowRunning || row.LeaseToken == nil || *row.LeaseToken != leaseToken {
		return ErrExecutionWorkflowLeaseLost
	}
	if update.NextStep < 0 || update.NextStep > row.StepCount {
		return ErrExecutionInvalid
	}
	row.Status, row.NextStep = update.Status, update.NextStep
	row.ScheduledFor, row.LastError, row.UpdatedAt = update.ScheduledFor.UTC(), update.LastError, update.UpdatedAt.UTC()
	row.LeaseToken, row.LeaseOwner, row.LeaseExpiresAt = nil, nil, nil
	if update.Status == api.ManagedExecutionWorkflowSucceeded || update.Status == api.ManagedExecutionWorkflowFailed {
		finished := update.UpdatedAt.UTC()
		row.FinishedAt = &finished
		clear(row.SealedPlan)
		row.SealedPlan = nil
		row.PayloadKID = ""
	}
	m.executionWorkflowJobs[id] = row
	return nil
}

func (m *MemStore) ExecutionWorkflowJobByKey(_ context.Context, accountID, workflowID string, principalID *string) (ExecutionWorkflowJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var chosen *ExecutionWorkflowJob
	for _, row := range m.executionWorkflowJobs {
		if row.AccountID != accountID || row.WorkflowID != workflowID ||
			(principalID != nil && !sameOptionalString(row.RunsPrincipalID, principalID)) {
			continue
		}
		if chosen == nil || row.CreatedAt.After(chosen.CreatedAt) {
			copy := cloneExecutionWorkflowJob(row)
			chosen = &copy
		}
	}
	if chosen == nil {
		return ExecutionWorkflowJob{}, ErrNotFound
	}
	return *chosen, nil
}

func (m *MemStore) ListExecutionWorkflowJobsByKey(_ context.Context, accountID, workflowID string, principalID *string) ([]ExecutionWorkflowJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]ExecutionWorkflowJob, 0)
	for _, row := range m.executionWorkflowJobs {
		if row.AccountID != accountID || row.WorkflowID != workflowID ||
			(principalID != nil && !sameOptionalString(row.RunsPrincipalID, principalID)) {
			continue
		}
		rows = append(rows, cloneExecutionWorkflowJob(row))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	return rows, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
