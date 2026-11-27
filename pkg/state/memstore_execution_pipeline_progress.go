package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ExecutionPipelineJob(_ context.Context, account, id string, principal *string) (ExecutionWorkflowJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.executionWorkflowJobs[id]
	if !ok || j.AccountID != account || principal != nil && !sameOptionalString(principal, j.RunsPrincipalID) {
		return ExecutionWorkflowJob{}, ErrNotFound
	}
	if _, ok = m.executionPipelines[id]; !ok {
		return ExecutionWorkflowJob{}, ErrNotFound
	}
	return cloneExecutionWorkflowJob(j), nil
}
func (m *MemStore) ListExecutionPipelineEvents(_ context.Context, account, id string, principal *string, after int64, limit int) (api.ExecutionPipelineEventsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.executionWorkflowJobs[id]
	if !ok || j.AccountID != account || principal != nil && !sameOptionalString(principal, j.RunsPrincipalID) {
		return api.ExecutionPipelineEventsResponse{}, ErrNotFound
	}
	p, ok := m.executionPipelines[id]
	if !ok {
		return api.ExecutionPipelineEventsResponse{}, ErrNotFound
	}
	return pipelineEvents(j, clonePipeline(p), after, limit)
}
