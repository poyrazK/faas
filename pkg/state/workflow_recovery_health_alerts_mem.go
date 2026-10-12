package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ProcessNextWorkflowRecoveryHealthAlert(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	ids := []string{}
	for id, p := range m.recoveryPolicies {
		app, ok := m.eventSubscriptionAppLocked(id)
		if ok && m.eventRecoveryAppLocked(app.AccountID, id) && !p.NextHealth.After(now) && (recoveryHealthAlertEnabled(p.Response.Policy) || recoveryHealthIncidentOpen(p.HealthIncident)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.recoveryPolicies[ids[i]].NextHealth, m.recoveryPolicies[ids[j]].NextHealth
		if !a.Equal(b) {
			return a.Before(b)
		}
		return ids[i] < ids[j]
	})
	if len(ids) == 0 {
		return false, nil
	}
	app, _ := m.eventSubscriptionAppLocked(ids[0])
	p := m.recoveryPolicies[ids[0]]
	queue, _ := m.workflowRecoveryAutomaticQueueLocked(app.ID, now, false)
	seconds := api.WorkflowRecoveryHealthDefaultStallSeconds
	if queue.Policy.Policy.HealthAlerts != nil {
		seconds = queue.Policy.Policy.HealthAlerts.StallAfterSeconds
	}
	health := api.RecoveryPolicyHealth(queue, seconds)
	next, event := recoveryHealthTransition(p.HealthIncident, health, recoveryHealthAlertEnabled(queue.Policy.Policy))
	if event != "" {
		receivers := []string{}
		for _, hook := range m.appWebhooks {
			if hook.Scope == AppWebhookScopeApp && sameMemUUID(hook.AccountID, app.AccountID) && sameMemUUID(hook.AppID, app.ID) && hook.Enabled && appWebhookMatches(hook.EventFilter, event) && recoveryHealthReceiverAllowed(next, event, hook.ID) {
				receivers = append(receivers, hook.ID)
			}
		}
		sort.Strings(receivers)
		payload := recoveryHealthPayload(next, health, app.Slug, event)
		raw, err := json.Marshal(payload)
		if err != nil {
			return false, fmt.Errorf("encode recovery health alert: %w", err)
		}
		recoveryHealthCapture(next, payload, event, receivers)
		if len(receivers) > 0 {
			if m.appWebhookEventOutbox == nil {
				m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
			}
			m.appWebhookEventOutbox[payload.EventID] = appWebhookOutboxEvent{ID: payload.EventID, AccountID: app.AccountID, AppID: app.ID, Event: event, SourceID: payload.EventID, Payload: raw, RecipientWebhookIDs: receivers, CreatedAt: now}
		}
	}
	for _, e := range recoveryHealthHistoryEvents(p.HealthIncident, next, p.Response.Version) {
		p.appendHistoryEvent(e, time.Now().UTC())
	}
	p.HealthIncident = next
	p.NextHealth = now.Add(api.WorkflowRecoveryPolicyPollInterval)
	return true, nil
}
