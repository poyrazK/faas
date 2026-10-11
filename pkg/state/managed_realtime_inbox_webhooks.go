package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// ManagedRealtimeInboxGapObserver detects retention gaps even while devices are
// offline. Events share the app webhook dispatcher and its durable retry ledger.
type ManagedRealtimeInboxGapObserver interface {
	ScanManagedRealtimeInboxGaps(context.Context, int) (int, error)
}

func (s *PgStore) ScanManagedRealtimeInboxGaps(ctx context.Context, batch int) (int, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	var count int
	err := s.pool.QueryRow(ctx, `select faas_scan_realtime_inbox_gaps($1)`, batch).Scan(&count)
	return count, err
}

// Called with the store lock held, alongside the checkpoint mutation.
func (m *MemStore) enqueueRealtimeInboxWebhookLocked(key managedRealtimeDurableCursorKey, event AppWebhookEvent, payload map[string]any) {
	ep, ok := m.managedRealtimeEndpoints[key.endpointID]
	if !ok {
		return
	}
	var recipients []string
	for id, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && hook.AppID == ep.AppID && hook.AccountID == ep.AccountID && hook.Enabled && appWebhookMatches(hook.EventFilter, event) {
			recipients = append(recipients, id)
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	id, now := newID(), time.Now().UTC()
	payload["event_id"], payload["app_id"], payload["endpoint_id"] = id, ep.AppID, ep.ID
	payload["principal_key"], payload["consumer"], payload["occurred_at"] = key.principal, key.subscription, now
	if event == AppWebhookEventRealtimeInboxAcknowledged {
		payload["message_id"] = nil
		if stream := m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: key.endpointID, channel: key.channel}]; stream != nil {
			for _, message := range stream.messages {
				if message.Sequence == payload["sequence"] {
					payload["message_id"] = message.TargetMessageID
					break
				}
			}
		}
	}
	body, _ := json.Marshal(payload) // Only scalar values constructed above.
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: ep.AccountID, AppID: ep.AppID, Event: event, SourceID: id, Payload: body, RecipientWebhookIDs: recipients, CreatedAt: now}
}

func (m *MemStore) ScanManagedRealtimeInboxGaps(ctx context.Context, batch int) (int, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimeHistoryInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now, count := time.Now().UTC(), 0
	for key, cursor := range m.managedRealtimeInboxCursors {
		if count >= batch {
			break
		}
		if cursor.gapReported || cursor.updatedAt.Before(now.Add(-ManagedRealtimeInboxCursorRetention)) {
			continue
		}
		stream := m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: key.endpointID, channel: key.channel}]
		if stream == nil {
			continue
		}
		oldest := stream.oldest
		for _, message := range stream.messages {
			if message.CreatedAt.Before(now.Add(-ManagedRealtimeInboxRetention)) && message.Sequence >= oldest {
				oldest = message.Sequence + 1
			}
		}
		if cursor.sequence >= oldest-1 {
			continue
		}
		m.enqueueRealtimeInboxWebhookLocked(key, AppWebhookEventRealtimeInboxGap, map[string]any{"sequence": cursor.sequence, "oldest_sequence": oldest, "latest_sequence": stream.next - 1})
		cursor.gapReported = true
		m.managedRealtimeInboxCursors[key] = cursor
		count++
	}
	return count, nil
}

var _ ManagedRealtimeInboxGapObserver = (*PgStore)(nil)
var _ ManagedRealtimeInboxGapObserver = (*MemStore)(nil)
