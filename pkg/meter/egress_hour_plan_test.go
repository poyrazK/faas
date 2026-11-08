package meter_test

// adr: 20261103131742 — amendment 2: each egress hour is priced by the plan
// held in that hour.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// planEgressProvider bills egress live with a different allowance per plan.
type planEgressProvider struct {
	*recordingEgressProvider
	included map[api.Plan]int64
}

func (p planEgressProvider) MeterUsagePolicy(plan api.Plan, m state.BillingMeter) (billing.MeterUsagePolicy, bool) {
	policy, ok := p.recordingEgressProvider.MeterUsagePolicy(plan, m)
	if ok {
		policy.IncludedQuantity = p.included[plan]
	}
	return policy, ok
}

// Egress used on Pro is priced against Pro's allowance even after a
// downgrade to Hobby: 3 GiB fits Pro's 5 GiB, so nothing is billed although
// it is past Hobby's 1 GiB.
func TestPushPendingEgressPricedByTheHoursPlan(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	electEgressOverage(t, ctx, store, acct.ID)
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	hour := now.Add(-2 * time.Hour)
	for _, step := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanPro, month}, {api.PlanHobby, hour.Add(time.Hour)}} {
		snapshot := state.FinancialPriceSnapshot{AccountID: acct.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: step.at, Plan: step.plan, DeliveryMode: "live",
			Price: financial.Price{Version: string(step.plan) + "-v1", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: api.IncludedMBSeconds(step.plan)}}
		if _, err := store.PutFinancialPriceSnapshot(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "on-pro", hour.Add(time.Minute), 0, 0, 0, 0, 3*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := planEgressProvider{
		recordingEgressProvider: &recordingEgressProvider{mode: billing.MeterDeliveryLive},
		included:                map[api.Plan]int64{api.PlanHobby: gib, api.PlanPro: 5 * gib, api.PlanScale: 20 * gib},
	}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	completeUsageThrough(t, ctx, store, now)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("PushPending = (%d, %v), want nothing billed inside Pro's allowance", pushed, err)
	}
	if calls := provider.MeterCalls(); len(calls) != 0 {
		t.Fatalf("egress calls = %+v, want none", calls)
	}
}
