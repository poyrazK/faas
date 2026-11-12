// adr: 20261111140000
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

// A paid account whose provider identity has no subscription (a former
// subscriber an operator kept on a paid plan) is held as identity_incomplete
// every pass, so meterd warns about it until the lookback drops the usage. A
// complimentary account is neither pushed nor held.
func TestPushPendingSkipsComplimentaryAccounts(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "partner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, provider := range []string{"stripe", "paddle", "polar"} {
		if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: provider,
			CustomerID: "customer-" + acct.ID, BillingFrom: time.Unix(0, 0).UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "inst-a", now.Add(-2*time.Hour), 1000, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("meter_complimentary_test")
	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, ops)
	completeUsageThrough(t, ctx, store, now)
	labels := map[string]string{"meter": string(state.BillingMeterCompute), "reason": meter.UsageHeldIdentityIncomplete}

	if _, err := pusher.PushPending(ctx, 5*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, ops.Registry(), "meterd_billing_usage_held_windows", labels); got != 1 {
		t.Fatalf("held windows without a subscription = %v, want 1", got)
	}

	if err := store.SetComplimentaryAccount(ctx, state.ComplimentaryAccount{AccountID: acct.ID, Plan: api.PlanPro,
		Reason: "partner_program", GrantedBy: acct.ID, GrantedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if pushed, err := pusher.PushPending(ctx, 5*24*time.Hour); pushed != 0 || err != nil {
		t.Fatalf("PushPending for a complimentary account = (%d, %v), want nothing", pushed, err)
	}
	if got := gaugeValue(t, ops.Registry(), "meterd_billing_usage_held_windows", labels); got != 0 {
		t.Fatalf("held windows once complimentary = %v, want 0", got)
	}
	if calls := provider.Calls(); len(calls) != 0 {
		t.Fatalf("provider calls = %+v, want none", calls)
	}
}
