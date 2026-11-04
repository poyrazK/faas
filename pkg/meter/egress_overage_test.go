package meter_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// turnOnEgressOverage records that subject turned egress overage on long
// before any test window (ADR-20261103131742 amendment 2).
func turnOnEgressOverage(t *testing.T, store state.EgressOverageStore, subjectID string) {
	t.Helper()
	if err := store.SetEgressOverage(context.Background(), subjectID, true, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("turn egress overage on: %v", err)
	}
}

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

// adr: 20261103131742 — a paid subject that never turned egress overage on is
// not billed for egress above its allowance: its apps stop there, and the few
// bytes sent before they stopped are settled at zero. Turning overage on
// later does not bill them.
func TestPushPendingEgressNotBilledWithOverageOff(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "past-allowance", now.Add(-time.Hour+time.Minute), 0, 0, 0, 0, 3*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	completeUsageThrough(t, ctx, store, now)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("PushPending = (%d, %v), want nothing billed with egress overage off", pushed, err)
	}
	if err := store.SetEgressOverage(ctx, acct.ID, true, now); err != nil {
		t.Fatal(err)
	}
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 0 {
		t.Fatalf("PushPending after turning overage on = (%d, %v), want the settled hour left alone", pushed, err)
	}
	if calls := provider.MeterCalls(); len(calls) != 0 {
		t.Fatalf("egress calls = %+v, want none", calls)
	}
}

// adr: 20261103131742 — an hour used while egress overage was on is billed,
// also when the subject turns overage off before meterd delivers the hour.
func TestPushPendingEgressBillsAnHourUsedWhileOverageWasOn(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	hour := now.Add(-time.Hour)
	const gib = int64(1 << 30)
	if err := store.SetEgressOverage(ctx, acct.ID, true, hour.Add(-3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "while-on", hour.Add(time.Minute), 0, 0, 0, 0, 3*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEgressOverage(ctx, acct.ID, false, hour.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	completeUsageThrough(t, ctx, store, now)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); err != nil || pushed != 1 {
		t.Fatalf("PushPending = (%d, %v), want the hour used while overage was on", pushed, err)
	}
	if calls := provider.MeterCalls(); len(calls) != 1 || calls[0].Quantity != 2*gib {
		t.Fatalf("egress calls = %+v, want the two GiB above the one-GiB allowance", calls)
	}
}

// adr: 20261103131742 — egress is priced by the plan the hour was used on:
// usage inside Pro's allowance stays unbilled after a move to Hobby, whose
// smaller allowance it would exceed.
func TestPushPendingEgressPricedByTheHoursPlan(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	turnOnEgressOverage(t, store, acct.ID)
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

// adr: 20261103131742 — egress past the allowance counts toward the spend cap
// only for a subject that had egress overage on this month.
func TestRunQuotaOnce_EgressWithOverageOffDoesNotCountTowardCap(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	acct := makeAccount(t, ctx, store, api.PlanHobby)
	store.SetClockForTest(func() time.Time { return now })
	store.SetOverageCapCentsForTest(acct.ID, 1)
	const gib = int64(1 << 30)
	if err := store.AppendUsage(ctx, acct.ID, "app-1", "inst-1", now.Add(-time.Minute), 0, 0, 0, 0, 2*gib, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingEgressProvider{mode: billing.MeterDeliveryLive}
	ops := wire.NewOpsMetrics("meter_test_egress_off_cap")
	cfg := &meter.Config{}
	cfg.Defaults()
	loop := meter.NewLoop(store, nil, &fakeParker{}, provider, &fakeNotifier{}, nil, nil, nil, nil,
		func() time.Time { return now }, discardLog(), cfg, ops)
	loop.RunQuotaOnce(ctx)
	if body := scrapeBody(t, ops); strings.Contains(body, `meter_test_egress_off_cap_billing_cap_exceeded_total{plan="hobby"} 1`) {
		t.Fatalf("egress with overage off counted toward the cap:\n%s", body)
	}
	if _, found, err := store.GetSpendCapHold(ctx, acct.ID); err != nil || found {
		t.Fatalf("spend cap hold found=%v err=%v; want none", found, err)
	}
	// Overage turned on earlier this month and off again still counts.
	if err := store.SetEgressOverage(ctx, acct.ID, true, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetEgressOverage(ctx, acct.ID, false, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	loop.RunQuotaOnce(ctx)
	if _, found, err := store.GetSpendCapHold(ctx, acct.ID); err != nil || !found {
		t.Fatalf("spend cap hold found=%v err=%v; want the month's overage counted", found, err)
	}
}
