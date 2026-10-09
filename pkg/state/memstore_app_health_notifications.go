package state

import (
	"slices"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) appHealthNotificationRecipientsLocked(app App) map[string]string {
	ids := make(map[string]string)
	if !m.accounts[app.AccountID].MayDeploy() {
		return ids
	}
	for id, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == app.ID && hook.AccountID == app.AccountID && hook.Enabled && slices.Contains(hook.EventFilter, string(AppWebhookEventAppHealthChanged)) {
			ids[id] = hook.UpdatedAt.UTC().Format(time.RFC3339Nano)
		}
	}
	return ids
}

func (m *MemStore) publishAppHealthNotificationLocked(app App, n appHealthNotification, now time.Time) {
	if n.SourceID == "" {
		return
	}
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = make(map[string]appWebhookOutboxEvent)
	}
	id := uuid.NewString()
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: app.AccountID, AppID: app.ID,
		Event: AppWebhookEventAppHealthChanged, SourceID: n.SourceID, Payload: n.Payload,
		RecipientWebhookIDs: n.Recipients, CreatedAt: now}
}
