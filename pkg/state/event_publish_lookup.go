package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PublishedEventLookupStore checks retained content before applying today's
// schema admission rules. It never creates or refreshes retained identities.
type PublishedEventLookupStore interface {
	LookupPublishedEventAcceptance(context.Context, string, []byte) (PublishedEventAcceptance, error)
}

func (s *PgStore) LookupPublishedEventAcceptance(ctx context.Context, account string, payload []byte) (PublishedEventAcceptance, error) {
	var identity publishedEventIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return PublishedEventAcceptance{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PublishedEventAcceptance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	matches, err := q.EventStorageIdentity(ctx, tx, sqlc.EventStorageIdentityParams{AccountID: mustPgUUID(account), Source: identity.Source, EventID: identity.ID, EventType: identity.Type, SchemaVersion: identity.SchemaVersion, EventData: identity.Data})
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedEventAcceptance{}, ErrNotFound
	}
	if err != nil {
		return PublishedEventAcceptance{}, err
	}
	if !matches {
		return PublishedEventAcceptance{}, ErrConflict
	}
	at, err := q.EventReceiptAcceptedAt(ctx, tx, sqlc.EventReceiptAcceptedAtParams{AccountID: mustPgUUID(account), EventSource: identity.Source, EventID: identity.ID})
	if err != nil {
		return PublishedEventAcceptance{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PublishedEventAcceptance{}, err
	}
	return PublishedEventAcceptance{AcceptedAt: timeFromPgtype(at), Duplicate: true}, nil
}
func (m *MemStore) LookupPublishedEventAcceptance(ctx context.Context, account string, payload []byte) (PublishedEventAcceptance, error) {
	if err := ctx.Err(); err != nil {
		return PublishedEventAcceptance{}, err
	}
	var identity publishedEventIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return PublishedEventAcceptance{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return PublishedEventAcceptance{}, err
	}
	work := m.eventFanout[canonicalMemUUID(account)+"\x00"+identity.Source+"\x00"+identity.ID]
	if work == nil {
		return PublishedEventAcceptance{}, ErrNotFound
	}
	var prior publishedEventIdentity
	if err := json.Unmarshal(work.Payload, &prior); err != nil {
		return PublishedEventAcceptance{}, err
	}
	if prior.Type != identity.Type || prior.SchemaVersion != identity.SchemaVersion || !jsonEqual(prior.Data, identity.Data) {
		return PublishedEventAcceptance{}, ErrConflict
	}
	return PublishedEventAcceptance{AcceptedAt: work.CreatedAt, Duplicate: true}, nil
}
