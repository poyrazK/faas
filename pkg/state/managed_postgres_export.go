package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PersonalManagedPostgresReference checks audit references against persisted
// ownership, including old events that predate explicit org_id attribution.
func (s *PgStore) PersonalManagedPostgresReference(ctx context.Context, accountID, kind, id string) (bool, error) {
	key, err := parsePgUUID(id)
	if err != nil {
		return false, nil //nolint:nilerr // Exclude malformed historical resource IDs.
	}
	account, err := parsePgUUID(accountID)
	if err != nil {
		return false, err
	}
	q := sqlc.New()
	if kind == "binding_id" {
		return q.ManagedPostgresPersonalBindingReference(ctx, s.pool, sqlc.ManagedPostgresPersonalBindingReferenceParams{ID: key, AccountID: account})
	}
	return q.ManagedPostgresPersonalDatabaseReference(ctx, s.pool, sqlc.ManagedPostgresPersonalDatabaseReferenceParams{ID: key, AccountID: account})
}
