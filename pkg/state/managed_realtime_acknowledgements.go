package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Reports the latest actual ACK separately from a resume baseline or reset.
// This is a client assertion, not proof of application side effects.
type RealtimeAcknowledgement struct {
	CheckpointSequence   string     `json:"checkpoint_sequence"`
	AcknowledgedSequence *string    `json:"acknowledged_sequence"`
	AcknowledgedAt       *time.Time `json:"acknowledged_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}
type RealtimeAcknowledgementStore interface {
	ReadRealtimeAcknowledgement(context.Context, string, string, string, string) (RealtimeAcknowledgement, error)
}

func (s *PgStore) ReadRealtimeAcknowledgement(ctx context.Context, ep, principal, subscription, channel string) (RealtimeAcknowledgement, error) {
	var result RealtimeAcknowledgement
	if err := validateManagedRealtimeDurableCursor(ep, principal, subscription, channel, 0); err != nil {
		return result, err
	}
	err := s.pool.QueryRow(ctx, `select sequence::text,acknowledged_sequence::text,acknowledged_at,updated_at
 from managed_realtime_durable_cursors where endpoint_id=$1 and principal=$2 and subscription=$3 and channel=$4
 and updated_at >= clock_timestamp()-make_interval(secs=>$5::double precision)`, ep, principal, subscription, channel, ManagedRealtimeDurableCursorRetention.Seconds()).Scan(&result.CheckpointSequence, &result.AcknowledgedSequence, &result.AcknowledgedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return result, err
}
func (m *MemStore) ReadRealtimeAcknowledgement(ctx context.Context, ep, principal, subscription, channel string) (RealtimeAcknowledgement, error) {
	var result RealtimeAcknowledgement
	if err := validateManagedRealtimeDurableCursor(ep, principal, subscription, channel, 0); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.managedRealtimeDurableCursors[managedRealtimeDurableCursorKey{ep, principal, subscription, channel}]
	if !ok || c.updatedAt.Before(time.Now().Add(-ManagedRealtimeDurableCursorRetention)) {
		return result, ErrNotFound
	}
	result.CheckpointSequence = strconv.FormatInt(c.sequence, 10)
	result.UpdatedAt = c.updatedAt
	if c.acknowledgedSequence != nil {
		seq := strconv.FormatInt(*c.acknowledgedSequence, 10)
		result.AcknowledgedSequence = &seq
		t := *c.acknowledgedAt
		result.AcknowledgedAt = &t
	}
	return result, nil
}
func (m *MemStore) enqueueRealtimeCheckpointWebhookLocked(key managedRealtimeDurableCursorKey, c managedRealtimeDurableCursorState) {
	ep, ok := m.managedRealtimeEndpoints[key.endpointID]
	if !ok {
		return
	}
	recipients := []string{}
	for id, h := range m.appWebhooks {
		if !h.Enabled || h.Scope != AppWebhookScopeApp || h.AppID != ep.AppID || h.AccountID != ep.AccountID {
			continue
		}
		for _, event := range h.EventFilter {
			if event == string(AppWebhookEventRealtimeChannelAcknowledged) {
				recipients = append(recipients, id)
				break
			}
		}
	}
	if len(recipients) == 0 {
		return
	}
	sort.Strings(recipients)
	id := newID()
	payload, _ := json.Marshal(map[string]any{"event_id": id, "app_id": ep.AppID, "endpoint_id": key.endpointID, "principal": key.principal, "subscription": key.subscription, "channel": key.channel, "sequence": strconv.FormatInt(*c.acknowledgedSequence, 10), "occurred_at": c.acknowledgedAt})
	if m.appWebhookEventOutbox == nil {
		m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
	}
	m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: ep.AccountID, AppID: ep.AppID, Event: AppWebhookEventRealtimeChannelAcknowledged, SourceID: id, Payload: payload, RecipientWebhookIDs: recipients, CreatedAt: *c.acknowledgedAt}
}
