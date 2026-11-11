package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SignupCountStore counts the verified accounts created since a time, for
// the platform-wide daily sign-up cap (ADR-20261104145353 amendment 1). An
// unverified account cannot deploy, so it claims no Free allowance and does
// not count: otherwise a few networks' worth of unverified sign-ups could
// close sign-up for everyone for a day.
type SignupCountStore interface {
	CountVerifiedAccountsCreatedSince(ctx context.Context, since time.Time) (int64, error)
}

// CountVerifiedAccountsCreatedSince implements SignupCountStore.
func (s *PgStore) CountVerifiedAccountsCreatedSince(ctx context.Context, since time.Time) (int64, error) {
	return sqlc.New().CountVerifiedAccountsCreatedSince(ctx, s.pool, pgtype.Timestamptz{Time: since.UTC(), Valid: true})
}

// CountVerifiedAccountsCreatedSince implements SignupCountStore.
func (m *MemStore) CountVerifiedAccountsCreatedSince(_ context.Context, since time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, a := range m.accounts {
		if !a.CreatedAt.Before(since) && a.EmailVerifiedAt != nil {
			n++
		}
	}
	return n, nil
}
