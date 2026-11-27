package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"sort"
	"time"
)

func (m *MemStore) PendingExecutionPipelineCallbacks(_ context.Context, after string, limit int) ([]ExecutionPipelineDelivery, error) {
	if limit < 1 || limit > api.ExecutionTaskCallbackBatch {
		return nil, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := []string{}
	for id, p := range m.executionPipelines {
		if id > after && p.Policy.Callback != nil && p.Completion != nil && p.DeliveredAt == nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]ExecutionPipelineDelivery, 0, len(ids))
	for _, id := range ids {
		out = append(out, pipelineDelivery(m.executionWorkflowJobs[id], m.executionPipelines[id]))
	}
	return out, nil
}
func (m *MemStore) MarkExecutionPipelineCallbackDelivered(_ context.Context, account, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.executionPipelines[id]
	j := m.executionWorkflowJobs[id]
	if !ok || j.AccountID != account || p.Policy.Callback == nil || p.Completion == nil {
		return ErrNotFound
	}
	if p.DeliveredAt == nil {
		now := time.Now().UTC()
		p.DeliveredAt = &now
		m.executionPipelines[id] = p
	}
	return nil
}
func (m *MemStore) ExecutionPipelineDeliveryByID(_ context.Context, account, id string) (ExecutionPipelineDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.executionPipelines[id]
	j := m.executionWorkflowJobs[id]
	if !ok || j.AccountID != account || p.Policy.Callback == nil {
		return ExecutionPipelineDelivery{}, ErrNotFound
	}
	return pipelineDelivery(j, p), nil
}
func (m *MemStore) ExecutionPipelineDeliveryForRun(_ context.Context, account, run string) (ExecutionPipelineDelivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.executions[run]
	if !ok || r.AccountID != account {
		return ExecutionPipelineDelivery{}, ErrNotFound
	}
	for id, p := range m.executionPipelines {
		j := m.executionWorkflowJobs[id]
		if j.AccountID != account || j.WorkflowID != r.WorkflowID || !sameOptionalString(j.RunsPrincipalID, r.RunsPrincipalID) || p.Policy.Callback == nil {
			continue
		}
		for i, s := range p.Policy.Steps {
			for a := 1; a <= s.Retry.MaxAttempts; a++ {
				if r.StepLabel == pipelineAdmissionLabel(j.PlanID, i, a) {
					return pipelineDelivery(j, p), nil
				}
			}
		}
	}
	return ExecutionPipelineDelivery{}, ErrNotFound
}
func (m *MemStore) enqueuePipelineFinishedLocked(j ExecutionWorkflowJob, c api.ExecutionPipelineCompletion) {
	payload := api.ExecutionPipelineFinishedWebhookPayload{EventID: j.ID, WorkflowID: j.WorkflowID, Outcome: c.Outcome, FinishedAt: c.FinishedAt, ReceiptURL: "/v1/execution-workflows/" + j.WorkflowID}
	body, _ := json.Marshal(payload)
	now := time.Now().UTC()
	for _, hook := range m.appWebhooks {
		if hook.AccountID != j.AccountID || hook.Scope != AppWebhookScopeAccount || !hook.Enabled || !slices.Contains(hook.EventFilter, api.ExecutionPipelineFinishedWebhookEvent) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{ID: id, WebhookID: hook.ID, AccountID: j.AccountID, Event: AppWebhookEvent(api.ExecutionPipelineFinishedWebhookEvent), Payload: body, Status: AppWebhookDeliveryPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		if m.appWebhookDeliverySources == nil {
			m.appWebhookDeliverySources = map[string]string{}
		}
		m.appWebhookDeliverySources[id] = j.ID
	}
}
