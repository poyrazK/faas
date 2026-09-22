// spec: §10 — provider cutover does not reset calendar-month allowances.
package meter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPushPendingMidHourIdentityPreservesAllowance(t *testing.T) {
	for _, kind := range []state.BillingMeter{state.BillingMeterCompute, state.BillingMeterEgress} {
		for _, capped := range []bool{false, true} {
			name := string(kind) + "/allowance"
			if capped {
				name = string(kind) + "/cap"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				store := state.NewMemStore()
				acct := makeAccount(t, ctx, store, api.PlanHobby)
				hour := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
				activation := hour.Add(30 * time.Minute)
				if err := store.UpdateAccountProviderCustomerID(ctx, acct.ID, "customer"); err != nil {
					t.Fatal(err)
				}
				if err := store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, "subscription"); err != nil {
					t.Fatal(err)
				}
				if err := store.UpsertBillingIdentity(ctx, state.BillingIdentity{AccountID: acct.ID, Provider: "stripe", CustomerID: "customer", SubscriptionID: "subscription", BillingFrom: activation}); err != nil {
					t.Fatal(err)
				}
				unit := int64(api.SecondsPerGBHour)
				included := int64(api.PlanHobby.PlanIncludedGBHours()) * unit
				var provider billing.Provider = &recordingOverage{}
				capCents := int64(1)
				if kind == state.BillingMeterEgress {
					unit = 1 << 30
					included = unit
					provider = &recordingEgressProvider{mode: billing.MeterDeliveryLive}
					capCents = 2
				}
				prefix := included
				if capped {
					// The old provider's usage has already exhausted the cap.
					prefix += unit
					store.SetOverageCapCentsForTest(acct.ID, capCents)
				}
				for i, quantity := range []int64{prefix, unit, unit} {
					at := []time.Time{hour.Add(10 * time.Minute), activation, hour.Add(time.Hour)}[i]
					compute, egress := quantity, int64(0)
					if kind == state.BillingMeterEgress {
						compute, egress = 0, quantity
					}
					if err := store.AppendUsage(ctx, acct.ID, "app", "instance", at, compute, 0, 0, 0, egress, 0, 0, 0); err != nil {
						t.Fatal(err)
					}
				}
				pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return hour.Add(2 * time.Hour) }, nil)
				want := 2
				if capped {
					want = 0
				}
				if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != want {
					t.Fatalf("PushPending = (%d, %v), want %d post-switch windows", pushed, err, want)
				}
				if !capped {
					if p, ok := provider.(*recordingOverage); ok {
						for _, call := range p.Calls() {
							if call.MBSeconds != unit {
								t.Errorf("compute = %d, want %d; do not charge the pre-switch prefix", call.MBSeconds, unit)
							}
						}
					} else {
						for _, call := range provider.(*recordingEgressProvider).MeterCalls() {
							if call.Quantity != unit {
								t.Errorf("egress = %d, want %d; do not charge the pre-switch prefix", call.Quantity, unit)
							}
						}
					}
				}
				if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
					t.Fatalf("replay = (%d, %v), want no new deliveries", pushed, err)
				}
				pending, err := store.PendingBillingMeterUsageWindows(ctx, "stripe", kind, hour, hour.Add(2*time.Hour))
				wantPending := 0
				if capped {
					wantPending = 2
				}
				if err != nil || len(pending) != wantPending {
					t.Fatalf("pending = (%+v, %v), want %d; capped windows must not get zero-usage receipts", pending, err, wantPending)
				}
			})
		}
	}
}

type failingBoundaryStore struct {
	state.Store
	err error
}

func (s *failingBoundaryStore) BillingIdentity(ctx context.Context, accountID, provider string) (state.BillingIdentity, error) {
	if s.err != nil {
		return state.BillingIdentity{}, s.err
	}
	return s.Store.BillingIdentity(ctx, accountID, provider)
}

func TestPushPendingRetriesFailedBoundaryRead(t *testing.T) {
	for _, kind := range []state.BillingMeter{state.BillingMeterCompute, state.BillingMeterEgress} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
			hour := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
			compute, egress := int64(api.PlanHobby.PlanIncludedGBHours()+1)*api.SecondsPerGBHour, int64(0)
			var provider billing.Provider = &recordingOverage{}
			if kind == state.BillingMeterEgress {
				compute, egress = 0, 2<<30
				provider = &recordingEgressProvider{mode: billing.MeterDeliveryLive}
			}
			if err := store.AppendUsage(ctx, acct.ID, "app", "instance", hour, compute, 0, 0, 0, egress, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
			readErr := errors.New("identity lookup unavailable")
			wrapped := &failingBoundaryStore{Store: store, err: readErr}
			pusher := meter.NewPusher(wrapped, provider, discardLog(), func() time.Time { return hour.Add(time.Hour) }, nil)
			if pushed, err := pusher.PushPending(ctx, 24*time.Hour); pushed != 0 || !errors.Is(err, readErr) {
				t.Fatalf("failed boundary read = (%d, %v), want no deliveries and original error", pushed, err)
			}
			wrapped.err = nil
			if pushed, err := pusher.PushPending(ctx, 24*time.Hour); pushed != 1 || err != nil {
				t.Fatalf("retry = (%d, %v), want one delivery after recovery", pushed, err)
			}
		})
	}
}
