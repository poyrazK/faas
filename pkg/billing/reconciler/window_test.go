// adr: 049 §B.1 — compare the same completed hours that billing can deliver.
package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type windowProvider struct {
	stubProvider
	start, end time.Time
	calls      int
}

func (p *windowProvider) ReconcileUsage(_ context.Context, _ state.Account, start, end time.Time) (int64, error) {
	p.start, p.end = start, end
	p.calls++
	return p.pushed, nil
}

func TestReconciliationUsesOnlyCompletedBillingHours(t *testing.T) {
	for _, window := range []time.Duration{24 * time.Hour, 90 * time.Minute} {
		t.Run(window.String(), func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct, err := store.CreateAccount(ctx, "window@example.com", api.PlanHobby)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "stripe", CustomerID: "customer", SubscriptionID: "subscription", BillingFrom: time.Unix(0, 0).UTC()}); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 9, 15, 9, 25, 0, 0, time.UTC)
			end := now.Truncate(time.Hour)
			start := end.Add(-window.Truncate(time.Hour))
			for i, at := range []time.Time{start.Add(-time.Minute), start.Add(10 * time.Minute), end.Add(-time.Minute), end.Add(10 * time.Minute)} {
				if err := store.AppendUsage(ctx, acct.ID, "app", "instance", at, int64(100*(i+1)), 0, 0, 0, 0, 0, 0, 0); err != nil {
					t.Fatal(err)
				}
			}
			provider := &windowProvider{stubProvider: stubProvider{pushed: 500, caps: billing.CapabilitySet(billing.CapUsageReconcile)}}
			rec := New("stripe", store, provider, nil, nil)
			rec.now = func() time.Time { return now }
			rec.Window = window
			if err := rec.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if provider.calls != 1 || !provider.start.Equal(start) || !provider.end.Equal(end) {
				t.Errorf("provider range = [%s, %s), want [%s, %s)", provider.start, provider.end, start, end)
			}
			families, err := rec.Registry.Gather()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, family := range families {
				if family.GetName() == "meterd_billing_drift_mb_seconds" {
					found = true
					if got := family.Metric[0].GetGauge().GetValue(); got != 0 {
						t.Errorf("drift = %v, want zero; incomplete/out-of-window usage is not due", got)
					}
				}
			}
			if !found {
				t.Fatal("missing reconciliation result")
			}
		})
	}
}
