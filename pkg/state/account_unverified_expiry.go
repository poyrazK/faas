package state

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// UnverifiedAccountExpiryStore finds accounts nobody ever verified, long
// after signup, that hold nothing: no app was ever created and there is no
// billing customer (ADR 20261110120000). apid schedules their deletion.
type UnverifiedAccountExpiryStore interface {
	// ListExpiredUnverifiedAccounts returns up to limit active, unverified,
	// free accounts without apps or billing whose grace clock (signup, or
	// enforcedFrom when later) started before cutoff, oldest first.
	ListExpiredUnverifiedAccounts(ctx context.Context, enforcedFrom, cutoff time.Time, limit int) ([]string, error)
}

var (
	_ UnverifiedAccountExpiryStore = (*MemStore)(nil)
	_ UnverifiedAccountExpiryStore = (*PgStore)(nil)
)

func (m *MemStore) ListExpiredUnverifiedAccounts(_ context.Context, enforcedFrom, cutoff time.Time, limit int) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hasApp := map[string]bool{}
	for _, app := range m.apps {
		hasApp[app.AccountID] = true
	}
	var out []Account
	for _, a := range m.accounts {
		start := a.CreatedAt
		if start.Before(enforcedFrom) {
			start = enforcedFrom
		}
		if a.Status != AccountActive || a.EmailVerified() || !start.Before(cutoff) || a.Plan != "free" ||
			a.ProviderCustomerID != "" || a.StripeSubscriptionItem != "" || hasApp[a.ID] {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	ids := make([]string, 0, min(limit, len(out)))
	for i := 0; i < len(out) && i < limit; i++ {
		ids = append(ids, out[i].ID)
	}
	return ids, nil
}

func (s *PgStore) ListExpiredUnverifiedAccounts(ctx context.Context, enforcedFrom, cutoff time.Time, limit int) ([]string, error) {
	rows, err := sqlc.New().ListExpiredUnverifiedAccounts(ctx, s.pool, sqlc.ListExpiredUnverifiedAccountsParams{
		EnforcedFrom: pgtype.Timestamptz{Time: enforcedFrom, Valid: true},
		Cutoff:       pgtype.Timestamptz{Time: cutoff, Valid: true},
		MaxRows:      int32(min(limit, 10_000)), //nolint:gosec // bounded above
	})
	if err != nil {
		return nil, mapErr(err)
	}
	ids := make([]string, 0, len(rows))
	for _, id := range rows {
		ids = append(ids, uuidString(id))
	}
	return ids, nil
}
