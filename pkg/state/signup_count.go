package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SignupCountStore counts the accounts created since a time, for the
// platform-wide daily sign-up cap (ADR-20261104145353).
type SignupCountStore interface {
	CountAccountsCreatedSince(ctx context.Context, since time.Time) (int64, error)
}

// CountAccountsCreatedSince implements SignupCountStore.
func (s *PgStore) CountAccountsCreatedSince(ctx context.Context, since time.Time) (int64, error) {
	return sqlc.New().CountAccountsCreatedSince(ctx, s.pool, pgtype.Timestamptz{Time: since.UTC(), Valid: true})
}

// CountAccountsCreatedSince implements SignupCountStore.
func (m *MemStore) CountAccountsCreatedSince(_ context.Context, since time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, a := range m.accounts {
		if !a.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}
