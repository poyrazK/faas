package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

var ErrManagedRealtimeFallbackSubscription = errors.New("state: fallback requires an enabled push provider or matching app webhook")

// NotificationStore commits a fallback deadline with the retained message.
// Drain records a fallback event only if no device has acknowledged its sequence.
type ManagedRealtimeNotificationStore interface {
	AppendManagedRealtimeNotification(context.Context, string, string, []byte, bool, string, int) (ManagedRealtimeChannelMessage, error)
	DrainManagedRealtimeFallbacks(context.Context, int) (int, error)
}

type managedRealtimeFallbackKey struct {
	endpointID, principal string
	sequence              int64
}
type managedRealtimeFallback struct {
	notBefore            time.Time
	collapseKey          string
	ttlSeconds           int
	expiresAt            time.Time
	priority             string
	groupKey, groupLabel string
	category             string
	messageID            string
	deadline             time.Time
}

func notificationKey(principal, messageID string, after int) (string, error) {
	if after < 1 || after > 86400 {
		return "", ErrManagedRealtimeHistoryInvalid
	}
	if err := validateManagedRealtimeInboxMessageID(messageID); err != nil {
		return "", err
	}
	return managedRealtimeInboxKey(principal)
}

func (s *PgStore) AppendManagedRealtimeNotification(ctx context.Context, endpointID, principal string, data []byte, binary bool, messageID string, after int) (ManagedRealtimeChannelMessage, error) {
	key, err := notificationKey(principal, messageID, after)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return s.appendManagedRealtimeInboxMessage(ctx, endpointID, key, data, binary, messageID, after)
}

func (m *MemStore) AppendManagedRealtimeNotification(ctx context.Context, endpointID, principal string, data []byte, binary bool, messageID string, after int) (ManagedRealtimeChannelMessage, error) {
	key, err := notificationKey(principal, messageID, after)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if err := ctx.Err(); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return m.appendManagedRealtimeInboxMessage(ctx, endpointID, key, data, binary, messageID, after)
}

func (s *PgStore) DrainManagedRealtimeFallbacks(ctx context.Context, batch int) (int, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	var count int
	err := s.pool.QueryRow(ctx, `select faas_drain_realtime_inbox_fallbacks($1)`, batch).Scan(&count)
	return count, err
}

func (m *MemStore) DrainManagedRealtimeFallbacks(ctx context.Context, batch int) (int, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var due []managedRealtimeFallbackKey
	for key, pending := range m.managedRealtimeInboxFallbacks {
		if pending.deadline.After(now) {
			continue
		}
		if m.managedRealtimeEndpoints[key.endpointID].Enabled {
			due = append(due, key)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		return m.managedRealtimeInboxFallbacks[due[i]].deadline.Before(m.managedRealtimeInboxFallbacks[due[j]].deadline)
	})
	count := 0
	for _, key := range due {
		if count >= batch {
			break
		}
		pending := m.managedRealtimeInboxFallbacks[key]
		ep := m.managedRealtimeEndpoints[key.endpointID]
		hookAvailable := false
		for _, hook := range m.appWebhooks {
			if hook.Scope == AppWebhookScopeApp && hook.AppID == ep.AppID && hook.AccountID == ep.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, AppWebhookEventRealtimeInboxFallbackRequired) {
				hookAvailable = true
				break
			}
		}
		pushed, capacity := m.enqueuePushLocked(key, pending)
		if !capacity || (pushed == 0 && !hookAvailable) {
			continue
		}
		if pending.ttlSeconds == 0 || pending.expiresAt.After(now) {
			m.enqueueRealtimeInboxWebhookLocked(managedRealtimeDurableCursorKey{endpointID: key.endpointID, principal: key.principal, channel: key.principal}, AppWebhookEventRealtimeInboxFallbackRequired, map[string]any{"message_id": pending.messageID, "sequence": key.sequence, "deadline": pending.deadline, "notification_not_before": pending.notBefore})
		}
		m.deleteNotificationFallbackLocked(key)
		count++
	}
	return count, nil
}

var _ ManagedRealtimeNotificationStore = (*PgStore)(nil)
var _ ManagedRealtimeNotificationStore = (*MemStore)(nil)

// Categories are part of the retained message's idempotency policy.
type ManagedRealtimeCategorizedNotificationStore interface {
	AppendManagedRealtimeCategorizedNotification(context.Context, string, string, []byte, bool, string, int, string, ...string) (ManagedRealtimeChannelMessage, error)
}

func (s *PgStore) AppendManagedRealtimeCategorizedNotification(ctx context.Context, ep, principal string, data []byte, binary bool, id string, after int, category string, groups ...string) (ManagedRealtimeChannelMessage, error) {
	key, err := notificationKey(principal, id, after)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if api.ValidateRealtimeNotificationCategory(category) != nil {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	return s.appendManagedRealtimeInboxMessage(ctx, ep, key, data, binary, id, after, append([]string{category}, groups...)...)
}
func (m *MemStore) AppendManagedRealtimeCategorizedNotification(ctx context.Context, ep, principal string, data []byte, binary bool, id string, after int, category string, groups ...string) (ManagedRealtimeChannelMessage, error) {
	key, err := notificationKey(principal, id, after)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if api.ValidateRealtimeNotificationCategory(category) != nil {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if err = ctx.Err(); err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return m.appendManagedRealtimeInboxMessage(ctx, ep, key, data, binary, id, after, append([]string{category}, groups...)...)
}
