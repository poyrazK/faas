// adr: 20261031090658 — after a paid plan ends, the Free quota counts usage
// from the switch to Free.
package meter_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A Pro subscription ends mid-month after 500 GB-h. The account is on Free
// now, but the usage it ran on Pro was billed on Pro's terms: it must not
// exhaust the Free allowance and suspend the account the moment it lands on
// Free. Usage after the switch still counts.
func TestRunQuotaOnce_FreeQuotaCountsUsageSinceThePaidPlanEnded(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	switched := month.Add(14 * 24 * time.Hour)
	now := switched.Add(2 * time.Hour)
	store.SetClockForTest(func() time.Time { return now })
	acct := makeAccount(t, ctx, store, api.PlanFree)
	for _, step := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanPro, month}, {api.PlanFree, switched}} {
		p := state.FinancialPriceSnapshot{AccountID: acct.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: step.at, Plan: step.plan, DeliveryMode: "live",
			Price: financial.Price{Version: string(step.plan) + "-v1", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: api.IncludedMBSeconds(step.plan)}}
		if _, err := store.PutFinancialPriceSnapshot(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	use := func(instance string, at time.Time, gbHours int64) {
		t.Helper()
		if err := store.AppendUsage(ctx, acct.ID, "app-1", instance, at, gbHours*api.SecondsPerGBHour, 0, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	use("inst-pro", month.Add(24*time.Hour), 500)
	use("inst-free", switched.Add(time.Hour), 5)
	cfg := &meter.Config{}
	cfg.Defaults()
	loop := meter.NewLoop(store, nil, &fakeParker{}, nil, &fakeNotifier{}, &fakeMailer{}, nil, nil, nil,
		func() time.Time { return now }, discardLog(), cfg, wire.NewOpsMetrics("meter_test_free_period"))
	status := func() state.AccountStatus {
		t.Helper()
		got, err := store.AccountByID(ctx, acct.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Status
	}

	loop.RunQuotaOnce(ctx)
	if got := status(); got != state.AccountActive {
		t.Fatalf("status after 5 GB-h on Free = %s, want active: usage on Pro counted toward the Free allowance", got)
	}

	use("inst-free-2", now.Add(-time.Minute), int64(api.PlanFree.PlanIncludedGBHours()))
	loop.RunQuotaOnce(ctx)
	if got := status(); got != state.AccountSuspended {
		t.Fatalf("status past the Free allowance since the switch = %s, want suspended", got)
	}
}
