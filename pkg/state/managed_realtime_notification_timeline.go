package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type ManagedRealtimeNotificationTimelineStore interface {
	ListManagedRealtimeNotificationTimeline(context.Context, string, string, string, int64) ([]api.ManagedRealtimeNotificationTimelineEvent, error)
}

func (m *MemStore) appendNotificationTimelineLocked(ep string, event api.ManagedRealtimeNotificationTimelineEvent) {
	if _, exists := m.managedRealtimeEndpoints[ep]; !exists {
		return
	}
	if m.realtimeNotificationTimeline == nil {
		m.realtimeNotificationTimeline = map[string][]api.ManagedRealtimeNotificationTimelineEvent{}
	}
	m.realtimeNotificationTimelineSequence++
	event.ID = m.realtimeNotificationTimelineSequence
	now := time.Now().UTC()
	event.OccurredAt = now
	events := m.realtimeNotificationTimeline[ep]
	first := 0
	for first < len(events) && events[first].OccurredAt.Before(now.Add(-7*24*time.Hour)) {
		first++
	}
	events = events[first:]
	if len(events) >= 8192 {
		events = events[len(events)-8191:]
	}
	m.realtimeNotificationTimeline[ep] = append(events, event)
}
func (m *MemStore) setPushDeliveryLocked(id string, j ManagedRealtimePushDelivery) {
	old, exists := m.managedRealtimePushDeliveries[id]
	m.managedRealtimePushDeliveries[id] = j
	if exists && old.Status == j.Status && old.Code == j.Code && old.Attempts == j.Attempts && old.NextAttempt.Equal(j.NextAttempt) && old.NotBefore.Equal(j.NotBefore) && old.StatusCode == j.StatusCode {
		return
	}
	if !exists || old.Status != j.Status {
		if event := pushOutcomeWebhook(j); event != "" {
			m.enqueueRealtimeInboxWebhookLocked(managedRealtimeDurableCursorKey{endpointID: j.EndpointID, principal: j.Principal, channel: j.Principal}, event, map[string]any{"delivery_id": id, "message_id": j.MessageID, "sequence": j.Sequence, "device": j.Device, "provider": j.Provider, "status": j.Status, "reason": j.Code, "attempts": j.Attempts, "status_code": j.StatusCode, "category": j.Category, "priority": j.Priority, "digest_id": j.DigestID})
		}
	}
	m.appendNotificationTimelineLocked(j.EndpointID, api.ManagedRealtimeNotificationTimelineEvent{Principal: j.Principal, MessageID: j.MessageID, Device: j.Device, DeliveryID: id, Event: j.Status, Reason: j.Code, Attempts: j.Attempts, StatusCode: j.StatusCode, NotBefore: j.NotBefore, NextAttempt: j.NextAttempt})
}
func (m *MemStore) setNotificationFallbackLocked(key managedRealtimeFallbackKey, f managedRealtimeFallback) {
	old, exists := m.managedRealtimeInboxFallbacks[key]
	m.managedRealtimeInboxFallbacks[key] = f
	event := "fallback_scheduled"
	if exists {
		if old.notBefore.Equal(f.notBefore) {
			return
		}
		event = "fallback_rescheduled"
	}
	m.appendNotificationTimelineLocked(key.endpointID, api.ManagedRealtimeNotificationTimelineEvent{Principal: key.principal, MessageID: f.messageID, Event: event, NotBefore: f.notBefore, NextAttempt: f.deadline})
}
func (m *MemStore) deleteNotificationFallbackLocked(key managedRealtimeFallbackKey) {
	f, exists := m.managedRealtimeInboxFallbacks[key]
	if !exists {
		return
	}
	delete(m.managedRealtimeInboxFallbacks, key)
	m.appendNotificationTimelineLocked(key.endpointID, api.ManagedRealtimeNotificationTimelineEvent{Principal: key.principal, MessageID: f.messageID, Event: "fallback_removed", NotBefore: f.notBefore, NextAttempt: f.deadline})
}
func (m *MemStore) ListManagedRealtimeNotificationTimeline(ctx context.Context, ep, principal, messageID string, before int64) ([]api.ManagedRealtimeNotificationTimelineEvent, error) {
	pk, err := notificationKey(principal, messageID, 1)
	if err != nil || before < 0 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []api.ManagedRealtimeNotificationTimelineEvent{}
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
	events := m.realtimeNotificationTimeline[ep]
	for i := len(events) - 1; i >= 0 && len(out) < 100; i-- {
		e := events[i]
		if e.Principal == pk && e.MessageID == messageID && (before == 0 || e.ID < before) && !e.OccurredAt.Before(cutoff) {
			e.Principal = ""
			out = append(out, e)
		}
	}
	return out, nil
}
func (s *PgStore) ListManagedRealtimeNotificationTimeline(ctx context.Context, ep, principal, messageID string, before int64) ([]api.ManagedRealtimeNotificationTimelineEvent, error) {
	pk, err := notificationKey(principal, messageID, 1)
	if err != nil || before < 0 {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	rows, err := s.pool.Query(ctx, `select id,message_id,device,delivery_id,event,reason,attempts,status_code,occurred_at,not_before,next_attempt from managed_realtime_notification_timeline where endpoint_id=$1 and principal=$2 and message_id=$3 and ($4::bigint=0 or id<$4) and occurred_at>=clock_timestamp()-interval '7 days' order by id desc limit 100`, ep, pk, messageID, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.ManagedRealtimeNotificationTimelineEvent{}
	for rows.Next() {
		var e api.ManagedRealtimeNotificationTimelineEvent
		if err = rows.Scan(&e.ID, &e.MessageID, &e.Device, &e.DeliveryID, &e.Event, &e.Reason, &e.Attempts, &e.StatusCode, &e.OccurredAt, &e.NotBefore, &e.NextAttempt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func pushOutcomeWebhook(j ManagedRealtimePushDelivery) AppWebhookEvent {
	switch j.Status {
	case "sent":
		return AppWebhookEventRealtimeNotificationSent
	case "failed":
		return AppWebhookEventRealtimeNotificationFailed
	case "cancelled":
		switch j.Code {
		case "expired", "quiet_hours_expired":
			return AppWebhookEventRealtimeNotificationExpired
		case "superseded":
			return AppWebhookEventRealtimeNotificationSuperseded
		default:
			return AppWebhookEventRealtimeNotificationCancelled
		}
	}
	return ""
}
