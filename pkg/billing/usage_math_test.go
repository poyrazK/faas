package billing

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1162
//
// The range follows the month's plan history hour by hour, exactly
// as the provider pusher delivers it. Hobby's 50 GB-h applies until the Pro
// upgrade activates at 10:30; from the 10:00 hour on, Pro's 250 GB-h does.
// Hobby for the whole range would bill 30 GB-h, Pro for the whole range 0.
func TestOverageMBSecondsForRangeFollowsPlanHistory(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "usage-history@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	upgrade := time.Date(2026, 9, 10, 10, 30, 0, 0, time.UTC)
	for _, g := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanHobby, month}, {api.PlanPro, upgrade}} {
		if _, err := store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{
			AccountID: acct.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0),
			Price: financial.Price{Version: "test-" + string(g.plan), Meter: "compute", Currency: "USD", Unit: "mb_seconds",
				UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour,
				IncludedQuantity: api.IncludedMBSeconds(g.plan)},
			Plan: g.plan, EffectiveFrom: g.at, DeliveryMode: "live",
		}); err != nil {
			t.Fatal(err)
		}
	}
	gbh := api.SecondsPerGBHour
	for i, row := range []struct {
		at time.Time
		mb int64
	}{
		{month.Add(time.Hour), 250 * gbh},                     // 50 GB-h of Hobby overage before the range
		{upgrade.Add(-80 * time.Minute), 10 * gbh},            // 09:10, Hobby: 10 GB-h billable
		{upgrade.Add(-20 * time.Minute), 10 * gbh},            // 10:10, Pro applies to the 10:00 hour
		{upgrade.Add(2*time.Hour + 10*time.Minute), 10 * gbh}, // 12:40, Pro
	} {
		if err := store.AppendUsage(ctx, acct.ID, "app", "instance-"+string(rune('a'+i)), row.at, row.mb, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := OverageMBSecondsForRange(ctx, store, acct, upgrade.Add(-90*time.Minute), upgrade.Add(150*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if want := 10 * gbh; got != want {
		t.Fatalf("overage = %d mb_seconds, want %d (only the pre-upgrade hour)", got, want)
	}
}

func TestOverageMBSecondsForRangeResetsAtUTCMonth(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "usage-math@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	august := time.Date(2026, 8, 1, 0, 5, 0, 0, time.UTC)
	windowStart := time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	// Exhaust August before the measured range, then use two billable
	// August GB-hours and three billable September GB-hours.
	rows := []struct {
		at time.Time
		mb int64
	}{
		{august, 200 * api.SecondsPerGBHour},
		{windowStart.Add(5 * time.Minute), 2 * api.SecondsPerGBHour},
		{windowStart.Add(time.Hour + 5*time.Minute), 203 * api.SecondsPerGBHour},
	}
	for i, row := range rows {
		if err := store.AppendUsage(ctx, acct.ID, "app-"+string(rune('a'+i)), "instance", row.at, row.mb, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	got, err := OverageMBSecondsForRange(ctx, store, acct, windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(5) * api.SecondsPerGBHour; got != want {
		t.Fatalf("overage range = %d, want %d (5 GB-hours)", got, want)
	}
}
