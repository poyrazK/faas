// adr: 20261103121407
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
)

func gaugeValue(t *testing.T, reg prometheus.Gatherer, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
	metrics:
		for _, m := range family.GetMetric() {
			for _, pair := range m.GetLabel() {
				if want, ok := labels[pair.GetName()]; ok && want != pair.GetValue() {
					continue metrics
				}
			}
			return m.GetGauge().GetValue()
		}
	}
	t.Fatalf("metric %s%v not found", name, labels)
	return 0
}

// TestPushPendingReportsHeldUsageBeforeItLeavesTheLookback pins
// ADR-20261103121407: a suspended subject's usage is held, not settled, and
// is dropped for good once it is older than the billing lookback. meterd
// reports held windows, and those leaving the lookback within 72 h, so an
// operator can act before revenue is silently lost.
func TestPushPendingReportsHeldUsageBeforeItLeavesTheLookback(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	lookback := 5 * 24 * time.Hour
	// One window two days from leaving the lookback, one recent.
	for i, at := range []time.Time{now.Add(-lookback + 48*time.Hour), now.Add(-2 * time.Hour)} {
		if err := store.AppendUsage(ctx, acct.ID, "app-a", []string{"old", "new"}[i], at, 1000, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("meter_test")
	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, ops)
	completeUsageThrough(t, ctx, store, now)
	if pushed, err := pusher.PushPending(ctx, lookback); pushed != 0 || err != nil {
		t.Fatalf("PushPending = (%d, %v), want nothing delivered for a suspended subject", pushed, err)
	}
	if calls := provider.Calls(); len(calls) != 0 {
		t.Fatalf("provider calls = %+v, want none", calls)
	}
	labels := map[string]string{"meter": string(state.BillingMeterCompute), "reason": meter.UsageHeldSuspended}
	if got := gaugeValue(t, ops.Registry(), "meterd_billing_usage_held_windows", labels); got != 2 {
		t.Fatalf("held windows = %v, want 2", got)
	}
	if got := gaugeValue(t, ops.Registry(), "meterd_billing_usage_held_expiring_windows", labels); got != 1 {
		t.Fatalf("expiring windows = %v, want 1 (the old window)", got)
	}

	// Once the subject is active again both windows are delivered and the
	// gauges clear.
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountActive); err != nil {
		t.Fatal(err)
	}
	if pushed, err := pusher.PushPending(ctx, lookback); pushed != 2 || err != nil {
		t.Fatalf("PushPending after reactivation = (%d, %v), want 2", pushed, err)
	}
	if got := gaugeValue(t, ops.Registry(), "meterd_billing_usage_held_windows", labels); got != 0 {
		t.Fatalf("held windows after delivery = %v, want 0", got)
	}
}
