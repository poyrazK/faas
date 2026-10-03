package state

import "context"

func (m *MemStore) AppWebhookEventOutboxHealth(_ context.Context) (AppWebhookEventOutboxHealth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var health AppWebhookEventOutboxHealth
	for _, event := range m.appWebhookEventOutbox {
		health.PendingCount++
		if health.OldestPendingAt == nil || event.CreatedAt.Before(*health.OldestPendingAt) {
			at := event.CreatedAt
			health.OldestPendingAt = &at
		}
	}
	return health, nil
}
