// adr: 049 — drift compares usage owned by the selected billing provider.
package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// deliveryStampingProvider is a provider that, like Polar, totals usage by the
// time each event was delivered.
type deliveryStampingProvider struct{ windowProvider }

func (*deliveryStampingProvider) StampsUsageAtDelivery() bool { return true }

// Polar buckets usage by when meterd sent it, so a meterd catch-up that
// delivers a day's backlog in this window, or the hours still waiting to be
// delivered, must not read as drift. Comparing Polar with local usage by the
// hour it was used did (bug hunt 2026-10-28 B22).
func TestDeliveryTimeProviderComparesTheDeliveryLedger(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "delivered@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "customer", SubscriptionID: "subscription", BillingFrom: time.Unix(0, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 20, 0, 0, time.UTC)
	start, end := billing.CompletedUsageWindow(now, DefaultWindow)

	// Usage in the window's last hour, not delivered yet: local usage by hour
	// counts it, Polar cannot.
	if err := store.AppendUsage(ctx, acct.ID, "app", "instance", end.Add(-30*time.Minute), 7*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	deliver := func(at, window time.Time, mbSeconds int64) {
		store.SetClockForTest(func() time.Time { return at })
		if err := store.RecordBillingUsageDelivery(ctx, "polar", acct.ID, window, mbSeconds); err != nil {
			t.Fatal(err)
		}
	}
	// A catch-up inside the window delivers hours used before it started.
	var delivered int64
	for h := 1; h <= 5; h++ {
		window := start.Add(-time.Duration(h) * time.Hour)
		deliver(start.Add(2*time.Hour), window, api.SecondsPerGBHour)
		delivered += api.SecondsPerGBHour
	}
	// Delivered before the window and after it: neither side counts them.
	deliver(start.Add(-time.Minute), start.Add(-48*time.Hour), 9*api.SecondsPerGBHour)
	deliver(end.Add(time.Minute), end.Add(-time.Hour), 9*api.SecondsPerGBHour)
	store.SetClockForTest(nil)

	provider := &deliveryStampingProvider{windowProvider{stubProvider: stubProvider{pushed: delivered, caps: billing.CapabilitySet(billing.CapUsageReconcile)}}}
	rec := New("polar", store, provider, nil, nil)
	rec.now = func() time.Time { return now }
	if err := rec.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || !provider.start.Equal(start) || !provider.end.Equal(end) {
		t.Fatalf("provider range = [%s,%s), calls=%d; want [%s,%s)", provider.start, provider.end, provider.calls, start, end)
	}
	if got := gaugeValue(t, rec, "meterd_billing_drift_mb_seconds"); got != 0 {
		t.Fatalf("drift = %v, want 0 when Polar counted exactly what meterd delivered", got)
	}

	// Polar missing one delivered hour is drift.
	provider.pushed = delivered - api.SecondsPerGBHour
	if err := rec.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := gaugeValue(t, rec, "meterd_billing_drift_mb_seconds"); got != float64(api.SecondsPerGBHour) {
		t.Fatalf("drift = %v, want %d for an hour Polar did not count", got, api.SecondsPerGBHour)
	}
}

func gaugeValue(t *testing.T, rec *Reconciler, name string) float64 {
	t.Helper()
	families, err := rec.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == name && len(family.Metric) > 0 {
			return family.Metric[0].GetGauge().GetValue()
		}
	}
	t.Fatalf("missing %s", name)
	return 0
}
