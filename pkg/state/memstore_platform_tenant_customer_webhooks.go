package state

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) enqueuePlatformTenantCustomerLifecycleWebhookLocked(consumer APIConsumer, event string, now time.Time) {
	tenant, ok := m.platformTenants[consumer.PlatformTenantID]
	if !ok || tenant.AccountID != consumer.AccountID || consumer.AppID == "" {
		return
	}
	payload, err := json.Marshal(api.PlatformTenantCustomerLifecycleWebhookPayload{
		PlatformTenantID: tenant.ID, ExternalRef: tenant.ExternalRef, ConsumerID: consumer.ID,
		AppID: consumer.AppID, CustomerExternalRef: consumer.ExternalRef,
		CustomerName: consumer.Name, CustomerStatus: string(consumer.Status), ChangedAt: now.UTC(),
	})
	if err != nil {
		return
	}
	for _, hook := range m.appWebhooks {
		if hook.Scope != AppWebhookScopePlatformTenant || hook.PlatformTenantID != tenant.ID ||
			hook.AccountID != tenant.AccountID || !hook.Enabled || !appWebhookMatches(hook.EventFilter, AppWebhookEvent(event)) {
			continue
		}
		alreadyQueued := false
		for _, delivery := range m.appWebhookDeliveries {
			if delivery.WebhookID != hook.ID || delivery.Event != AppWebhookEvent(event) {
				continue
			}
			var existing api.PlatformTenantCustomerLifecycleWebhookPayload
			if json.Unmarshal(delivery.Payload, &existing) == nil && existing.ConsumerID == consumer.ID {
				alreadyQueued = true
				break
			}
		}
		if alreadyQueued {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{
			ID: id, WebhookID: hook.ID, AccountID: tenant.AccountID,
			Event: AppWebhookEvent(event), Payload: json.RawMessage(payload),
			Status: AppWebhookDeliveryPending, NextAttemptAt: now,
			CreatedAt: now, UpdatedAt: now,
		}
	}
}
