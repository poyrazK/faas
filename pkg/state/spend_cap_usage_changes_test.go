package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// checkSpendCapUsageChanges runs the ADR-20261020211500 store contract: a
// consumer's or tenant's usage counts as changed since a time when a usage
// minute was recorded after it, and only for that consumer or tenant.
func checkSpendCapUsageChanges(t *testing.T, ctx context.Context, st state.Store, accountID, appID string) {
	t.Helper()
	changes := st.(state.SpendCapUsageChangeStore)
	consumer, err := st.CreateAPIConsumer(ctx, accountID, appID, "changing", "Changing customer")
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := st.CreateAPIConsumer(ctx, accountID, appID, "quiet", "Quiet customer")
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := st.(state.PlatformTenantStore).CreatePlatformTenant(ctx, accountID, "changing-tenant", "Changing tenant", 250)
	if err != nil {
		t.Fatal(err)
	}
	minute := time.Now().UTC().Truncate(time.Minute)
	before := time.Now().Add(-time.Minute)
	if _, err := st.(state.ConsumerUsageStore).RecordAPIConsumerUsage(ctx, state.APIConsumerUsageEvent{EventID: uuid.NewString(),
		AccountID: accountID, AppID: appID, ConsumerKey: consumer.ID, PlatformTenantID: tenant.ID, WindowStart: minute,
		RequestCount: 1, BillableUnits: 1}); err != nil {
		t.Fatal(err)
	}
	after := time.Now().Add(time.Minute)
	from := minute.Add(-time.Hour)
	for _, tc := range []struct {
		name  string
		check func(since time.Time) (bool, error)
		since time.Time
		want  bool
	}{
		{"consumer since before", func(s time.Time) (bool, error) {
			return changes.APIConsumerUsageChangedSince(ctx, accountID, appID, consumer.ID, from, s)
		}, before, true},
		{"consumer since after", func(s time.Time) (bool, error) {
			return changes.APIConsumerUsageChangedSince(ctx, accountID, appID, consumer.ID, from, s)
		}, after, false},
		{"other consumer", func(s time.Time) (bool, error) {
			return changes.APIConsumerUsageChangedSince(ctx, accountID, appID, quiet.ID, from, s)
		}, before, false},
		{"tenant since before", func(s time.Time) (bool, error) {
			return changes.PlatformTenantUsageChangedSince(ctx, accountID, tenant.ID, from, s)
		}, before, true},
		{"tenant since after", func(s time.Time) (bool, error) {
			return changes.PlatformTenantUsageChangedSince(ctx, accountID, tenant.ID, from, s)
		}, after, false},
	} {
		if got, err := tc.check(tc.since); err != nil || got != tc.want {
			t.Errorf("%s: changed = %t, %v; want %t", tc.name, got, err, tc.want)
		}
	}
}

// adr: 20261020211500
func TestMemStoreSpendCapUsageChanges(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkSpendCapUsageChanges(t, ctx, store, accountID, appID)
}

// adr: 20261020211500
func TestPgStoreSpendCapUsageChanges(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID := seedConsumerKeyAccountApp(t, ctx, store)
	checkSpendCapUsageChanges(t, ctx, store, accountID, appID)
}
