package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// PublishedEventAcceptance is determined while holding the publication lock.
// Duplicate means that identical content was already durably accepted.
type PublishedEventAcceptance struct {
	AcceptedAt time.Time
	Duplicate  bool
}

type PublishedEventAcceptanceStore interface {
	AcceptPublishedEvent(context.Context, string, string, []byte) (PublishedEventAcceptance, error)
}

func (s *PgStore) AcceptPublishedEvent(ctx context.Context, actor, accountID string, payload []byte) (PublishedEventAcceptance, error) {
	var result PublishedEventAcceptance
	err := s.appendCustomerPublishedEventResult(ctx, actor, accountID, "", "", payload, nil, nil, &result)
	return result, err
}

func (m *MemStore) AcceptPublishedEvent(ctx context.Context, actor, accountID string, payload []byte) (PublishedEventAcceptance, error) {
	if err := ctx.Err(); err != nil {
		return PublishedEventAcceptance{}, err
	}
	var identity publishedEventIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return PublishedEventAcceptance{}, fmt.Errorf("decode published event: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return PublishedEventAcceptance{}, err
	}
	key := canonicalMemUUID(accountID) + "\x00" + identity.Source + "\x00" + identity.ID
	duplicate := m.eventFanout[key] != nil
	if err := m.appendEventLocked(actor, "event.published", &accountID, payload, nil, time.Now().UTC()); err != nil {
		return PublishedEventAcceptance{}, err
	}
	work := m.eventFanout[key]
	if work == nil {
		return PublishedEventAcceptance{}, ErrNotFound
	}
	return PublishedEventAcceptance{AcceptedAt: work.CreatedAt, Duplicate: duplicate}, nil
}
