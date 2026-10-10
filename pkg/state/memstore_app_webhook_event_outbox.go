package state

import (
	"context"
	"sort"
	"time"
)

func (m *MemStore) DrainAppWebhookEventOutbox(_ context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	events := make([]appWebhookOutboxEvent, 0, len(m.appWebhookEventOutbox))
	for _, event := range m.appWebhookEventOutbox {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].ID < events[j].ID
		}
		return events[i].CreatedAt.Before(events[j].CreatedAt)
	})
	processed := 0
	for _, event := range events {
		if processed >= limit {
			break
		}
		m.relayAppWebhookOutboxEventLocked(event)
		processed++
	}
	return processed, nil
}

func (m *MemStore) RelayAppWebhookEventOutboxSource(_ context.Context, event AppWebhookEvent, sourceID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.appWebhookEventOutbox {
		if row.Event == event && row.SourceID == sourceID {
			m.relayAppWebhookOutboxEventLocked(row)
			return true, nil
		}
	}
	return false, nil
}

// The store lock covers every insert and removal; a relay cannot expose a
// partially fanned-out event to another MemStore caller.
func (m *MemStore) relayAppWebhookOutboxEventLocked(event appWebhookOutboxEvent) {
	now := time.Now().UTC()
	for _, webhookID := range event.RecipientWebhookIDs {
		hook, ok := m.appWebhooks[webhookID]
		if !ok || hook.AccountID != event.AccountID || hook.AppID != event.AppID {
			continue
		}
		id := newID()
		m.appWebhookDeliveries[id] = AppWebhookDelivery{
			ID: id, WebhookID: webhookID, AppID: event.AppID, AccountID: event.AccountID,
			Event: event.Event, Payload: append([]byte(nil), event.Payload...),
			Status: AppWebhookDeliveryPending, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
		m.trackRecoveryNotificationDeliveryLocked(event, webhookID, id)
	}
	delete(m.appWebhookEventOutbox, event.ID)
}
