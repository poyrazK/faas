// adr: 20261121231500
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A paid hour not yet delivered when the subscription ends is held as
// subscription_ended, not dropped as Free usage; a Free hour stays unbilled
// and unheld.
func TestPushPendingHoldsPaidHoursAfterSubscriptionEnded(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "cancelled@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "stripe",
		CustomerID: "customer-" + acct.ID, SubscriptionID: "sub-" + acct.ID, BillingFrom: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	paidHour := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	freeAt := paidHour.Add(90 * time.Minute)
	provider := &recordingStripe{}
	store.SetPlanChangedAt(acct.ID, paidHour.Add(-24*time.Hour)) // Pro since before the paid hour
	if err := meter.RecordFinancialPricingForTest(ctx, store, provider, paidHour.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	store.SetPlanChangedAt(acct.ID, freeAt)
	if err := meter.RecordFinancialPricingForTest(ctx, store, provider, freeAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	freeHour := freeAt.Truncate(time.Hour).Add(time.Hour)
	for _, at := range []time.Time{paidHour.Add(5 * time.Minute), freeHour.Add(5 * time.Minute)} {
		if err := store.AppendUsage(ctx, acct.ID, "app-a", "inst-a", at, 1000, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	now := freeHour.Add(2 * time.Hour)
	completeUsageThrough(t, ctx, store, now)
	ops := wire.NewOpsMetrics("meter_subscription_ended_test")
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, ops)

	if pushed, err := pusher.PushPending(ctx, 5*24*time.Hour); pushed != 0 || err != nil {
		t.Fatalf("PushPending = (%d, %v), want nothing delivered", pushed, err)
	}
	ended := map[string]string{"meter": string(state.BillingMeterCompute), "reason": meter.UsageHeldSubscriptionEnded}
	if got := gaugeValue(t, ops.Registry(), "meterd_billing_usage_held_windows", ended); got != 1 {
		t.Fatalf("held subscription_ended windows = %v, want 1 (the paid hour only)", got)
	}
	if calls := provider.Calls(); len(calls) != 0 {
		t.Fatalf("provider calls = %+v, want none", calls)
	}
}
