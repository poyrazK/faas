package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresChargeStore is a MemStore with a live managed PostgreSQL
// overage charge.
type managedPostgresChargeStore struct {
	*state.MemStore
	delivered, pending int64
}

func (s managedPostgresChargeStore) LiveManagedPostgresMillicents(context.Context, string, time.Time) (int64, int64, error) {
	return s.delivered, s.pending, nil
}

// The spend cap counts the managed PostgreSQL overage charge like compute,
// egress and object storage: the month's spending the cap compares includes
// what was delivered, a part cent counting as a whole one (billing review
// 2026-11-13, R7-1).
func TestSpendCapCountsTheManagedPostgresCharge(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct := makeAccount(t, ctx, mem, api.PlanPro)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	store := managedPostgresChargeStore{MemStore: mem, delivered: 250*api.MillicentsPerCent + 1, pending: 900 * api.MillicentsPerCent}
	if cents, err := meter.SpendCapManagedPostgresCents(ctx, store, acct.ID, now); err != nil || cents != 251 {
		t.Fatalf("SpendCapManagedPostgresCents = %d, %v; want 251", cents, err)
	}
	spent, err := meter.MonthToDateSpendCents(ctx, store, nil, acct, now)
	if err != nil || spent != 251 {
		t.Fatalf("MonthToDateSpendCents = %d, %v; want the 251-cent database charge", spent, err)
	}
	// A store without managed PostgreSQL charges counts none.
	if cents, err := meter.SpendCapManagedPostgresCents(ctx, mem, acct.ID, now); err != nil || cents != 0 {
		t.Fatalf("plain store = %d, %v", cents, err)
	}
}
