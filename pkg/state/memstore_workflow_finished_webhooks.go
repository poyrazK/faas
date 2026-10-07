package state

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) enqueueWorkflowFinishedWebhookLocked(run WorkflowRun, finishedAt time.Time) {
	if run.Status != WorkflowRunStatusSucceeded && run.Status != WorkflowRunStatusFailed && run.Status != WorkflowRunStatusDead {
		return
	}
	app, ok := m.apps[run.AppID]
	if !ok {
		return
	}

	var recipients []string
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == app.ID && hook.AccountID == app.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, AppWebhookEventWorkflowFinished) {
			recipients = append(recipients, hook.ID)
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	payload, _ := json.Marshal(api.WorkflowFinishedWebhookPayload{
		AppID: app.ID, RunID: run.ID, WorkflowName: run.WorkflowName,
		Status: run.Status, FinishedAt: finishedAt.UTC(), ResumeCount: run.ResumeCount,
	})
	outboxID := uuid.NewString()
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = make(map[string]appWebhookOutboxEvent)
	}
	m.appWebhookEventOutbox[outboxID] = appWebhookOutboxEvent{
		ID: outboxID, AccountID: app.AccountID, AppID: app.ID,
		Event: AppWebhookEventWorkflowFinished, SourceID: uuid.NewString(),
		Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: finishedAt.UTC(),
	}
}
