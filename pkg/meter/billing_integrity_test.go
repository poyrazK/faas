// spec: §10
// adr: 1162
//
// Delivery finality, the hard spend cap and plan-history allowances.
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// markComputeComplete records every minute in [from, to) compute-complete,
// as meterd's sample loop does after rolling them.
func markComputeComplete(t *testing.T, ctx context.Context, s *state.MemStore, from, to time.Time) {
	t.Helper()
	for minute := from.UTC().Truncate(time.Minute); minute.Before(to); minute = minute.Add(time.Minute) {
		if err := s.RecordFinancialSamplingWindow(ctx, minute, true, false); err != nil {
			t.Fatalf("record sampling window %s: %v", minute, err)
		}
	}
}

// completeUsageThrough marks the 31 days before now compute-complete, so a
// test that is not about delivery finality sees every closed hour as final.
func completeUsageThrough(t *testing.T, ctx context.Context, s *state.MemStore, now time.Time) {
	t.Helper()
	markComputeComplete(t, ctx, s, now.Add(-31*24*time.Hour), now)
}

func putComputeGrant(t *testing.T, ctx context.Context, s *state.MemStore, acct state.Account, plan api.Plan, effectiveFrom time.Time) {
	t.Helper()
	monthStart := time.Date(effectiveFrom.Year(), effectiveFrom.Month(), 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{
		AccountID:   acct.ID,
		PeriodStart: monthStart,
		PeriodEnd:   monthStart.AddDate(0, 1, 0),
		Price: financial.Price{
			Version: "test-" + string(plan), Meter: "compute", Currency: "USD", Unit: "mb_seconds",
			UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour,
			IncludedQuantity: api.IncludedMBSeconds(plan),
		},
		Plan:          plan,
		EffectiveFrom: effectiveFrom,
		DeliveryMode:  "live",
	}); err != nil {
		t.Fatalf("put %s grant: %v", plan, err)
	}
}

func TestPushPendingHoldsHourUntilEveryMinuteIsFinal(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	hour := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	now := hour.Add(2 * time.Hour)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", hour.Add(5*time.Minute), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)

	// The sampler has not recorded minute 10:30 complete: catch-up may still
	// write it, so the hour must not be delivered.
	markComputeComplete(t, ctx, store, hour.Add(-api.MeterUsageFinalAfter), hour.Add(30*time.Minute))
	markComputeComplete(t, ctx, store, hour.Add(31*time.Minute), now)
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); pushed != 0 || err != nil {
		t.Fatalf("PushPending with an open minute = (%d, %v), want (0, nil)", pushed, err)
	}
	if calls := provider.Calls(); len(calls) != 0 {
		t.Fatalf("provider calls before the hour is final = %+v, want none", calls)
	}

	// Catch-up rolls the late minute; only now is the hour final, and the
	// late row is part of the single delivery.
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", hour.Add(30*time.Minute), 200, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	markComputeComplete(t, ctx, store, hour.Add(30*time.Minute), hour.Add(31*time.Minute))
	if pushed, err := pusher.PushPending(ctx, 24*time.Hour); pushed != 1 || err != nil {
		t.Fatalf("PushPending after the hour is final = (%d, %v), want (1, nil)", pushed, err)
	}
	calls := provider.Calls()
	if len(calls) != 1 || !calls[0].Hour.Equal(hour) || calls[0].MBSeconds != 300 {
		t.Fatalf("provider calls = %+v, want one %s delivery of 300 mb_seconds", calls, hour)
	}
}

func TestPushPendingDeliversHourOlderThanCatchUpWindow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	hour := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", hour.Add(5*time.Minute), 100, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingStripe{}
	// No minute was ever recorded complete (meterd was down), but the sampler
	// can no longer write this hour, so it is final by age.
	now := hour.Add(time.Hour + api.MeterUsageFinalAfter)
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if pushed, err := pusher.PushPending(ctx, 48*time.Hour); pushed != 1 || err != nil {
		t.Fatalf("PushPending = (%d, %v), want (1, nil)", pushed, err)
	}
}

func TestPushPendingBillsUpToSpendCapAndNeverBackBills(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanHobby)
	gbh := api.SecondsPerGBHour
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	usage := func(hour time.Time, gbHours int64) {
		t.Helper()
		if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-"+hour.Format("15"), hour, gbHours*gbh, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	usage(monthStart, int64(api.PlanHobby.PlanIncludedGBHours())) // the whole Hobby allowance
	usage(monthStart.Add(time.Hour), 3)                           // crosses a 1-cent (1 GB-h) cap
	usage(monthStart.Add(2*time.Hour), 2)                         // entirely above the cap
	capCents := int64(1)
	if err := store.UpdateAccountOverageCapCents(ctx, acct.ID, &capCents); err != nil {
		t.Fatal(err)
	}
	provider := &recordingOverage{}
	now := monthStart.Add(4 * time.Hour)
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	markComputeComplete(t, ctx, store, now.Add(-api.MeterUsageFinalAfter), now)
	if _, err := pusher.PushPending(ctx, 24*time.Hour); err != nil {
		t.Fatalf("PushPending under the cap: %v", err)
	}
	calls := provider.Calls()
	if len(calls) != 1 || !calls[0].Hour.Equal(monthStart.Add(time.Hour)) || calls[0].MBSeconds != gbh {
		t.Fatalf("capped deliveries = %+v, want exactly 1 GB-h in the crossing hour", calls)
	}

	// Raising the cap resumes billing for new usage only; the 4 GB-h the cap
	// excluded stay unbilled.
	capCents = 1_000
	if err := store.UpdateAccountOverageCapCents(ctx, acct.ID, &capCents); err != nil {
		t.Fatal(err)
	}
	usage(monthStart.Add(4*time.Hour), 1)
	now = monthStart.Add(6 * time.Hour)
	markComputeComplete(t, ctx, store, monthStart.Add(4*time.Hour), now)
	if _, err := pusher.PushPending(ctx, 24*time.Hour); err != nil {
		t.Fatalf("PushPending after raising the cap: %v", err)
	}
	calls = provider.Calls()
	if len(calls) != 2 || !calls[1].Hour.Equal(monthStart.Add(4*time.Hour)) || calls[1].MBSeconds != gbh {
		t.Fatalf("deliveries after raising the cap = %+v, want one new 1 GB-h delivery and no back-billing", calls)
	}
}

func TestPushPendingKeepsLargestRecordedGrantAfterDowngrade(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct := makeBillableAccount(t, ctx, store, api.PlanPro)
	gbh := api.SecondsPerGBHour
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	downgrade := monthStart.Add(10 * 24 * time.Hour)
	putComputeGrant(t, ctx, store, acct, api.PlanPro, monthStart)
	putComputeGrant(t, ctx, store, acct, api.PlanHobby, downgrade)
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	// 200 GB-h on Pro, then 10 GB-h after the downgrade: 210 GB-h stays within
	// the month's largest grant (Pro, 250 GB-h), so nothing is overage.
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-a", monthStart.Add(time.Hour), 200*gbh, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	after := downgrade.Add(time.Hour)
	if err := store.AppendUsage(ctx, acct.ID, "app-a", "instance-b", after, 10*gbh, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	provider := &recordingOverage{}
	now := after.Add(2 * time.Hour)
	markComputeComplete(t, ctx, store, now.Add(-api.MeterUsageFinalAfter), now)
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if _, err := pusher.PushPending(ctx, 30*24*time.Hour); err != nil {
		t.Fatalf("PushPending: %v", err)
	}
	if calls := provider.Calls(); len(calls) != 0 {
		t.Fatalf("deliveries after a downgrade = %+v, want none within the Pro grant", calls)
	}
}
