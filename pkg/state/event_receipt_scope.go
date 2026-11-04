package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EventReceiptScope is what an account route needs to apply company access to
// a retained event (ADR-20261025211616): the producer the event was stamped
// with ("" for an unstamped event) and each recipient's app by subscription.
type EventReceiptScope struct {
	Producer      string
	RecipientApps map[string]string
}

type EventReceiptScopeStore interface {
	EventReceiptScope(ctx context.Context, accountID, source, eventID string) (EventReceiptScope, error)
}

func (s *PgStore) EventReceiptScope(ctx context.Context, accountID, source, eventID string) (EventReceiptScope, error) {
	row, err := sqlc.New().EventReceiptScope(ctx, s.pool, sqlc.EventReceiptScopeParams{AccountID: mustPgUUID(accountID), EventSource: source, EventID: eventID})
	if errors.Is(err, pgx.ErrNoRows) {
		return EventReceiptScope{}, ErrNotFound
	}
	if err != nil {
		return EventReceiptScope{}, fmt.Errorf("read event receipt scope: %w", err)
	}
	scope := EventReceiptScope{Producer: row.Producer, RecipientApps: map[string]string{}}
	if err := json.Unmarshal(row.RecipientApps, &scope.RecipientApps); err != nil {
		return EventReceiptScope{}, fmt.Errorf("decode event receipt scope: %w", err)
	}
	return scope, nil
}

func (m *MemStore) EventReceiptScope(_ context.Context, accountID, source, eventID string) (EventReceiptScope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.eventFanout[canonicalMemUUID(accountID)+"\x00"+source+"\x00"+eventID]
	if work == nil {
		return EventReceiptScope{}, ErrNotFound
	}
	var envelope publishedEventIdentity
	if err := json.Unmarshal(work.Payload, &envelope); err != nil {
		return EventReceiptScope{}, err
	}
	scope := EventReceiptScope{Producer: envelope.Producer, RecipientApps: map[string]string{}}
	for _, recipient := range work.RecipientSnapshot {
		scope.RecipientApps[recipient.ID] = recipient.AppID
	}
	for id, added := range work.routingRecipients {
		if _, captured := scope.RecipientApps[id]; !captured && added != nil {
			scope.RecipientApps[id] = added.Recipient.AppID
		}
	}
	return scope, nil
}
