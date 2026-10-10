package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PublishedEventLookupStore checks retained content before applying today's
// schema admission rules. It never creates or refreshes retained identities.
type PublishedEventLookupStore interface {
	LookupPublishedEventAcceptance(context.Context, string, []byte) (PublishedEventAcceptance, error)
}

// PublishedEventComparison preserves acceptance even for a content mismatch.
type PublishedEventComparison struct {
	AcceptedAt time.Time
	Matches    bool
}
type PublishedEventComparisonStore interface {
	ComparePublishedEventAcceptance(context.Context, string, []byte) (PublishedEventComparison, error)
}

func publishedEventLookupResult(result PublishedEventComparison, err error) (PublishedEventAcceptance, error) {
	if err != nil {
		return PublishedEventAcceptance{}, err
	}
	if !result.Matches {
		return PublishedEventAcceptance{}, ErrConflict
	}
	return PublishedEventAcceptance{AcceptedAt: result.AcceptedAt, Duplicate: true}, nil
}
func (s *PgStore) LookupPublishedEventAcceptance(ctx context.Context, account string, payload []byte) (PublishedEventAcceptance, error) {
	result, err := s.ComparePublishedEventAcceptance(ctx, account, payload)
	return publishedEventLookupResult(result, err)
}
func (m *MemStore) LookupPublishedEventAcceptance(ctx context.Context, account string, payload []byte) (PublishedEventAcceptance, error) {
	result, err := m.ComparePublishedEventAcceptance(ctx, account, payload)
	return publishedEventLookupResult(result, err)
}

func (s *PgStore) ComparePublishedEventAcceptance(ctx context.Context, account string, payload []byte) (PublishedEventComparison, error) {
	var identity publishedEventIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return PublishedEventComparison{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PublishedEventComparison{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	matches, err := q.EventStorageIdentity(ctx, tx, sqlc.EventStorageIdentityParams{AccountID: mustPgUUID(account), Source: identity.Source, EventID: identity.ID, EventType: identity.Type, SchemaVersion: identity.SchemaVersion, EventData: identity.Data})
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedEventComparison{}, ErrNotFound
	}
	if err != nil {
		return PublishedEventComparison{}, err
	}
	at, err := q.EventReceiptAcceptedAt(ctx, tx, sqlc.EventReceiptAcceptedAtParams{AccountID: mustPgUUID(account), EventSource: identity.Source, EventID: identity.ID})
	if err != nil {
		return PublishedEventComparison{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PublishedEventComparison{}, err
	}
	return PublishedEventComparison{AcceptedAt: timeFromPgtype(at), Matches: matches}, nil
}
func (m *MemStore) ComparePublishedEventAcceptance(ctx context.Context, account string, payload []byte) (PublishedEventComparison, error) {
	if err := ctx.Err(); err != nil {
		return PublishedEventComparison{}, err
	}
	var identity publishedEventIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return PublishedEventComparison{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return PublishedEventComparison{}, err
	}
	work := m.eventFanout[canonicalMemUUID(account)+"\x00"+identity.Source+"\x00"+identity.ID]
	if work == nil {
		return PublishedEventComparison{}, ErrNotFound
	}
	var prior publishedEventIdentity
	if err := json.Unmarshal(work.Payload, &prior); err != nil {
		return PublishedEventComparison{}, err
	}
	matches := prior.Type == identity.Type && prior.SchemaVersion == identity.SchemaVersion && jsonEqual(prior.Data, identity.Data)
	return PublishedEventComparison{AcceptedAt: work.CreatedAt, Matches: matches}, nil
}
