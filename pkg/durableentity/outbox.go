// adr: 843
package durableentity

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// OutboxIntent describes outgoing work without performing it. WebhookID names
// a registered app webhook, never an arbitrary URL or signing credential. This
// engine validates shape only; a future relay must recheck ownership/admission.
type OutboxIntent struct {
	WebhookID string          `json:"webhook_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// OutboxMessage is immutable pending work rooted in the committed snapshot.
// ID is deterministic for the full entity scope, committed version and ordinal.
type OutboxMessage struct {
	ID      string       `json:"id"`
	Version uint64       `json:"state_version"`
	Ordinal int          `json:"ordinal"`
	Intent  OutboxIntent `json:"intent"`
}

// OutboxView is a bounded observation, not authority to dispatch or acknowledge.
type OutboxView struct {
	Version  uint64          `json:"state_version"`
	Messages []OutboxMessage `json:"messages"`
}

func validOutboxIntent(intent OutboxIntent) bool {
	return validUUID(intent.WebhookID) && validIdentity(intent.EventType) && json.Valid(intent.Payload)
}

func outboxMessageID(id ID, version uint64, ordinal int) string {
	body, _ := json.Marshal(struct {
		Entity  ID
		Version uint64
		Ordinal int
	}{id, version, ordinal})
	return uuid.NewSHA1(uuid.NameSpaceURL, append([]byte("gregale/durable-entity-outbox/v1/"), body...)).String()
}

func appendOutbox(state *snapshot, intents []OutboxIntent) error {
	if len(intents) > api.MaxDurableEntityOutboxPerTransition {
		return exceeded("outbox_transition_messages", api.MaxDurableEntityOutboxPerTransition, len(intents))
	}
	if len(state.Outbox)+len(intents) > api.MaxDurableEntityOutboxPending {
		return exceeded("outbox_pending_messages", api.MaxDurableEntityOutboxPending, len(state.Outbox)+len(intents))
	}
	for ordinal, intent := range intents {
		if !validOutboxIntent(intent) {
			return ErrInvalid
		}
		if len(intent.Payload) > api.MaxDurableEntityOutboxPayloadBytes {
			return exceeded("outbox_payload_bytes", api.MaxDurableEntityOutboxPayloadBytes, len(intent.Payload))
		}
		intent.Payload = append(json.RawMessage(nil), intent.Payload...)
		state.Outbox = append(state.Outbox, OutboxMessage{ID: outboxMessageID(state.ID, state.Version, ordinal), Version: state.Version, Ordinal: ordinal, Intent: intent})
	}
	body, err := json.Marshal(state.Outbox)
	if err != nil {
		return ErrInvalid
	}
	if len(body) > api.MaxDurableEntityOutboxBytes {
		return exceeded("outbox_bytes", api.MaxDurableEntityOutboxBytes, len(body))
	}
	return nil
}

func validOutbox(state snapshot) bool {
	if len(state.Outbox) > api.MaxDurableEntityOutboxPending {
		return false
	}
	var previous OutboxMessage
	for _, message := range state.Outbox {
		if message.Version == 0 || message.Version > state.Version || message.Ordinal < 0 || message.Ordinal >= api.MaxDurableEntityOutboxPerTransition ||
			message.ID != outboxMessageID(state.ID, message.Version, message.Ordinal) || !validOutboxIntent(message.Intent) || len(message.Intent.Payload) > api.MaxDurableEntityOutboxPayloadBytes ||
			message.Version < previous.Version || message.Version == previous.Version && message.Ordinal <= previous.Ordinal {
			return false
		}
		previous = message
	}
	body, err := json.Marshal(state.Outbox)
	return err == nil && len(body) <= api.MaxDurableEntityOutboxBytes
}

// PendingOutbox restores only committed messages for one exact private scope.
// It never trusts LIST, creates a claim, exposes pending work to the guest, or
// performs delivery. Missing/corrupt committed snapshots always fail closed.
func (m *Manager) PendingOutbox(ctx context.Context, id ID) (OutboxView, error) {
	base, _, err := m.readManifest(ctx, id)
	if err != nil {
		return OutboxView{}, err
	}
	state, err := m.readSnapshot(ctx, base)
	if errors.Is(err, ErrNotFound) {
		latest, _, readErr := m.readManifest(ctx, id)
		if readErr != nil {
			return OutboxView{}, readErr
		}
		if latest.SnapshotKey != base.SnapshotKey {
			return OutboxView{}, ErrConflict
		}
	}
	if err != nil {
		return OutboxView{}, err
	}
	// Restore already allocated an independent snapshot; no manager-owned slice
	// or payload escapes, and an empty queue is returned as an empty JSON array.
	messages := state.Outbox
	if messages == nil {
		messages = []OutboxMessage{}
	}
	return OutboxView{Version: state.Version, Messages: messages}, nil
}
