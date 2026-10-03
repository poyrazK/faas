package state

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) enqueuePlatformTenantReconciliationAppliedWebhooksLocked(receipt api.PlatformTenantReconciliationReceiptResponse) {
	tenant, ok := m.platformTenants[receipt.TenantID]
	if !ok {
		return
	}
	payload, err := json.Marshal(api.PlatformTenantReconciliationAppliedWebhookPayload{
		PlatformTenantID: tenant.ID,
		ExternalRef:      tenant.ExternalRef,
		ReceiptID:        receipt.ReceiptID,
		PlanHash:         receipt.PlanHash,
		AppliedAt:        receipt.AppliedAt,
		ChangeCount:      len(receipt.Changes),
	})
	if err != nil {
		return
	}
	event := AppWebhookEvent(PlatformTenantReconciliationAppliedEvent)
	for _, hook := range m.appWebhooks {
		if hook.Scope != AppWebhookScopePlatformTenant || hook.PlatformTenantID != tenant.ID ||
			hook.AccountID != tenant.AccountID || !hook.Enabled || !appWebhookMatches(hook.EventFilter, event) {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{
			ID: id, WebhookID: hook.ID, AccountID: tenant.AccountID,
			Event: event, Payload: json.RawMessage(payload), Status: AppWebhookDeliveryPending,
			NextAttemptAt: receipt.AppliedAt, CreatedAt: receipt.AppliedAt, UpdatedAt: receipt.AppliedAt,
		}
	}
}
