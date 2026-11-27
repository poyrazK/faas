package state

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"time"
)

func (m *MemStore) enqueueExecutionFinishedLocked(r Execution) {
	if r.FinishedAt == nil {
		return
	}
	payload := api.ExecutionFinishedWebhookPayload{EventID: r.ID, ExecutionID: r.ID, Status: r.Status, Runtime: r.Runtime, FinishedAt: r.FinishedAt.UTC().Format(time.RFC3339Nano), ReceiptURL: "/v1/executions/" + r.ID, EnvironmentID: r.EnvironmentID, WorkflowID: r.WorkflowID}
	if r.ResultValidation != nil {
		payload.ResultValidationStatus = r.ResultValidation.Status
	}
	body, _ := json.Marshal(payload)
	now := time.Now().UTC()
	for _, hook := range m.appWebhooks {
		if hook.AccountID != r.AccountID || hook.Scope != AppWebhookScopeAccount || !hook.Enabled || !slices.Contains(hook.EventFilter, api.ExecutionFinishedWebhookEvent) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{ID: id, WebhookID: hook.ID, AccountID: r.AccountID, Event: AppWebhookEventExecutionFinished, Payload: body, Status: AppWebhookDeliveryPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		if m.appWebhookDeliverySources == nil {
			m.appWebhookDeliverySources = map[string]string{}
		}
		m.appWebhookDeliverySources[id] = r.ID
	}
}
