// adr: 933
package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) AcceptEntityOutboxDelivery(ctx context.Context, in AppWebhookDelivery) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fingerprint, err := entityOutboxFingerprint(in)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if previous, ok := m.entityOutboxAcceptances[in.ID]; ok {
		if previous != fingerprint {
			return "", ErrConflict
		}
		return in.ID, nil
	}
	hook, ok := m.appWebhooks[in.WebhookID]
	if !ok || hook.Scope != AppWebhookScopeApp || hook.AppID != in.AppID || hook.AccountID != in.AccountID || !hook.Enabled {
		return "", ErrNotFound
	}
	account, accountExists := m.accounts[in.AccountID]
	app, appExists := m.apps[in.AppID]
	limits, knownPlan := api.LimitsFor(account.Plan)
	if !accountExists || !appExists || !knownPlan || !limits.AsyncInvokeAllowed || limits.WebhookPerApp <= 0 ||
		account.Status != AccountActive && account.Status != AccountPastDue || account.DeletionRequestedAt != nil || account.AbuseHeld() ||
		app.AccountID != account.ID || app.DeletedAt != nil || app.Status == AppDeleted || !app.AcceptsRequestInvocations() {
		return "", ErrNotFound
	}
	if _, exists := m.appWebhookDeliveries[in.ID]; exists {
		return "", ErrConflict
	}
	now := time.Now().UTC()
	in.Status, in.Attempt, in.NextAttemptAt, in.CreatedAt, in.UpdatedAt = AppWebhookDeliveryPending, 0, now, now, now
	in.LastError, in.LastResponseCode, in.DeliveredAt = "", 0, nil
	in.Payload = append([]byte(nil), in.Payload...)
	m.appWebhookDeliveries[in.ID] = in
	if m.entityOutboxAcceptances == nil {
		m.entityOutboxAcceptances = make(map[string]string)
	}
	m.entityOutboxAcceptances[in.ID] = fingerprint
	return in.ID, nil
}
