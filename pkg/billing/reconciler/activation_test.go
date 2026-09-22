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

func TestReconciliationHonorsProviderActivation(t *testing.T) {
	for _, mode := range []billing.UsageMode{billing.UsageModeRaw, billing.UsageModeOverage} {
		for _, minute := range []int{0, 30, 60, 90} {
			activation := time.Date(2026, 9, 15, 10, minute, 0, 0, time.UTC)
			t.Run(string(mode)+"/"+activation.Format("15:04"), func(t *testing.T) {
				ctx := context.Background()
				store := state.NewMemStore()
				acct, err := store.CreateAccount(ctx, "activation@example.com", api.PlanHobby)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "polar", CustomerID: "customer", SubscriptionID: "subscription", BillingFrom: activation}); err != nil {
					t.Fatal(err)
				}
				now := time.Date(2026, 9, 15, 11, 10, 0, 0, time.UTC)
				allowance := int64(api.PlanHobby.PlanIncludedGBHours()) * api.SecondsPerGBHour
				for i, at := range []time.Time{activation.Add(-time.Minute), activation} {
					quantity := allowance
					if i == 1 {
						quantity = api.SecondsPerGBHour
					}
					if err := store.AppendUsage(ctx, acct.ID, "app", "instance", at, quantity, 0, 0, 0, 0, 0, 0, 0); err != nil {
						t.Fatal(err)
					}
				}
				provider := &windowProvider{stubProvider: stubProvider{pushed: api.SecondsPerGBHour, caps: billing.CapabilitySet(billing.CapUsageReconcile), mode: mode}}
				rec := New("polar", store, provider, nil, nil)
				rec.now = func() time.Time { return now }
				if err := rec.RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
				end := now.Truncate(time.Hour)
				if !activation.Before(end) {
					if provider.calls != 0 {
						t.Fatal("queried provider before its first completed billing hour")
					}
					return
				}
				if provider.calls != 1 || !provider.start.Equal(activation.Truncate(time.Hour)) || !provider.end.Equal(end) {
					t.Fatalf("provider range = [%s,%s), calls=%d; must include event stamped at activation hour start", provider.start, provider.end, provider.calls)
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
							t.Errorf("drift = %v, want zero for fully delivered post-switch usage", got)
						}
					}
				}
				if !found {
					t.Fatal("missing reconciliation result")
				}
			})
		}
	}
}
