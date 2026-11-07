package state

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// MonthBilledComputeMBSeconds equals what the provider pusher bills hour by
// hour, each hour above the allowance in force at it, across a downgrade and
// a later upgrade; with no plan change it is usage above the allowance.
// adr: 20261031100000
func TestMonthBilledComputeMatchesThePusher(t *testing.T) {
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	grant := func(plan api.Plan, at time.Time) FinancialPriceSnapshot {
		return FinancialPriceSnapshot{
			PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Plan: plan, EffectiveFrom: at, RecordedAt: at,
			Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(plan)},
		}
	}
	snapshots := []FinancialPriceSnapshot{
		grant(api.PlanPro, monthStart),
		grant(api.PlanHobby, monthStart.Add(10*24*time.Hour+15*time.Hour+20*time.Minute)),
		grant(api.PlanScale, monthStart.Add(20*24*time.Hour+7*time.Hour+45*time.Minute)),
	}
	// 3 GB-h an hour, with a burst, crosses every allowance in play.
	hours := 30 * 24
	perHour := make([]int64, hours)
	for h := range perHour {
		perHour[h] = 3 * api.SecondsPerGBHour
		if h >= 200 && h < 260 {
			perHour[h] = 12 * api.SecondsPerGBHour
		}
	}
	usageBefore := func(at time.Time) (int64, error) {
		var total int64
		for h := 0; h < hours && monthStart.Add(time.Duration(h)*time.Hour).Before(at); h++ {
			total += perHour[h]
		}
		return total, nil
	}
	var pushed int64
	var cumulative int64
	for h := 0; h < hours; h++ {
		hour := monthStart.Add(time.Duration(h) * time.Hour)
		included := MonthComputeIncludedMBSeconds(api.PlanScale, snapshots, hour.Add(time.Hour))
		before := api.BillableMBSecondsAbove(included, cumulative)
		cumulative += perHour[h]
		pushed += api.BillableMBSecondsAbove(included, cumulative) - before
		through := hour.Add(time.Hour)
		if (h+1)%24 != 0 {
			continue
		}
		got, err := MonthBilledComputeMBSeconds(api.PlanScale, snapshots, through, cumulative, usageBefore)
		if err != nil {
			t.Fatal(err)
		}
		if got != pushed {
			t.Fatalf("through %s: billed %d mb_seconds, the pusher bills %d", through.Format(time.RFC3339), got, pushed)
		}
	}

	// One plan all month: usage above its allowance, no usage read.
	single := []FinancialPriceSnapshot{grant(api.PlanHobby, monthStart)}
	usage := int64(250) * api.SecondsPerGBHour
	got, err := MonthBilledComputeMBSeconds(api.PlanHobby, single, monthStart.Add(15*24*time.Hour), usage, func(time.Time) (int64, error) {
		t.Fatal("usage read without a plan change")
		return 0, nil
	})
	if err != nil || got != api.BillableMBSecondsAbove(api.IncludedMBSeconds(api.PlanHobby), usage) {
		t.Fatalf("single plan billed %d (%v), want usage above the allowance", got, err)
	}
}

// After a downgrade the cap's figure is what is billed: usage the larger
// allowance covered is not overage, only usage after the downgrade above the
// smaller allowance is.
// adr: 20261031100000
func TestCurrentMonthOverageCentsAfterADowngrade(t *testing.T) {
	ctx := t.Context()
	m := NewMemStore()
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	downgrade := monthStart.Add(15 * 24 * time.Hour)
	acct, err := m.CreateAccount(ctx, "downgrade@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanPro, monthStart}, {api.PlanHobby, downgrade}} {
		if _, err := m.PutFinancialPriceSnapshot(ctx, FinancialPriceSnapshot{
			AccountID: acct.ID, PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Plan: g.plan, EffectiveFrom: g.at,
			DeliveryMode: "live",
			Price: financial.Price{Version: "test-" + string(g.plan), Meter: "compute", Currency: "USD", Unit: "mb_seconds",
				UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: api.IncludedMBSeconds(g.plan)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.UpdateAccountPlan(ctx, acct.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	gbh := api.SecondsPerGBHour
	if err := m.AppendUsage(ctx, acct.ID, "app", "before", downgrade.Add(-time.Hour), 800*gbh, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	m.SetClockForTest(func() time.Time { return downgrade.Add(2 * time.Hour) })
	if cents, err := m.CurrentMonthOverageCents(ctx, acct.ID); err != nil || cents != 0 {
		t.Fatalf("overage right after the downgrade = %d (%v), want 0: Pro's allowance covered the 800 GB-h", cents, err)
	}
	if err := m.AppendUsage(ctx, acct.ID, "app", "after", downgrade.Add(time.Hour), 7*gbh, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if cents, err := m.CurrentMonthOverageCents(ctx, acct.ID); err != nil || cents != 7 {
		t.Fatalf("overage after 7 GB-h more = %d (%v), want 7 cents", cents, err)
	}
}

// Free hours are never billed (ADR-20261101143527), so they never count as
// billed compute: a subject that subscribes mid-month is billed only usage
// after it subscribes above the allowance then in force, and its cap is not
// spent on its Free hours. A month on Free throughout bills nothing.
// adr: 20261101143527, 20261031100000
func TestMonthBilledComputeSkipsFreeHours(t *testing.T) {
	monthStart := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	subscribed := monthStart.Add(11 * 24 * time.Hour) // the 12th, 00:00
	grant := func(plan api.Plan, at time.Time) FinancialPriceSnapshot {
		return FinancialPriceSnapshot{
			PeriodStart: monthStart, PeriodEnd: monthStart.AddDate(0, 1, 0), Plan: plan, EffectiveFrom: at, RecordedAt: at,
			Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(plan)},
		}
	}
	gbh := api.SecondsPerGBHour
	// 140 GB-h on Free before subscribing, 210 GB-h on Hobby after.
	usageBefore := func(time.Time) (int64, error) { return 140 * gbh, nil }
	snapshots := []FinancialPriceSnapshot{grant(api.PlanFree, monthStart), grant(api.PlanHobby, subscribed)}
	end := monthStart.AddDate(0, 1, 0)
	got, err := MonthBilledComputeMBSeconds(api.PlanHobby, snapshots, end, 350*gbh, usageBefore)
	if err != nil {
		t.Fatal(err)
	}
	// After subscribing the allowance is Free's share of the 11 days plus
	// Hobby's of the other 20; the pusher bills each Hobby hour above it.
	included := MonthComputeIncludedMBSeconds(api.PlanHobby, snapshots, subscribed.Add(time.Hour))
	want := api.BillableMBSecondsAbove(included, 350*gbh) - api.BillableMBSecondsAbove(included, 140*gbh)
	if got != want || want <= 0 {
		t.Fatalf("billed %d mb_seconds, want %d: only Hobby hours above the allowance", got, want)
	}
	// Through the subscription the month was Free: nothing is billed.
	if got, err := MonthBilledComputeMBSeconds(api.PlanHobby, snapshots, subscribed, 140*gbh, usageBefore); err != nil || got != 0 {
		t.Fatalf("billed through the subscription = %d (%v), want 0", got, err)
	}
	free := []FinancialPriceSnapshot{grant(api.PlanFree, monthStart)}
	if got, err := MonthBilledComputeMBSeconds(api.PlanFree, free, end, 90*gbh, usageBefore); err != nil || got != 0 {
		t.Fatalf("a Free month billed %d (%v), want 0", got, err)
	}
	// Without recorded activations the current plan applies, Free included.
	if got, err := MonthBilledComputeMBSeconds(api.PlanFree, nil, end, 90*gbh, usageBefore); err != nil || got != 0 {
		t.Fatalf("a Free month without activations billed %d (%v), want 0", got, err)
	}
}
