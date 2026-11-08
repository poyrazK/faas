package billing

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261101113742
//
// The usage pages showed usage above today's plan allowance. A past month
// billed on Pro, for an account now on Hobby, showed Hobby overage that was
// never billed. The month's own plan history decides, as it does for the
// bill; a month without one keeps the current plan.
func TestComputeOverageForMonthFollowsTheMonthsPlan(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "usage-page@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	gbh := int64(api.SecondsPerGBHour)
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := store.PutFinancialPriceSnapshot(ctx, state.FinancialPriceSnapshot{
		AccountID: acct.ID, PeriodStart: september, PeriodEnd: september.AddDate(0, 1, 0),
		Price: financial.Price{Version: "test-pro", Meter: "compute", Currency: "USD", Unit: "mb_seconds",
			UnitQuantity: api.SecondsPerGBHour, MillicentsPerUnit: api.OverageMillicentsPerGBHour,
			IncludedQuantity: api.IncludedMBSeconds(api.PlanPro)},
		Plan: api.PlanPro, EffectiveFrom: september, DeliveryMode: "live",
	}); err != nil {
		t.Fatal(err)
	}
	usage := api.IncludedMBSeconds(api.PlanHobby) + 100*gbh
	if usage >= api.IncludedMBSeconds(api.PlanPro) {
		t.Fatalf("fixture usage %d is not inside Pro's allowance", usage)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	past, err := ComputeOverageForMonth(ctx, store, acct, september, now, usage)
	if err != nil {
		t.Fatal(err)
	}
	if past.BilledMBSeconds != 0 || past.IncludedMBSeconds != api.IncludedMBSeconds(api.PlanPro) {
		t.Fatalf("September = %+v, want Pro's allowance and nothing billed", past)
	}

	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	current, err := ComputeOverageForMonth(ctx, store, acct, october, now, api.IncludedMBSeconds(api.PlanHobby)+5*gbh)
	if err != nil {
		t.Fatal(err)
	}
	if current.BilledMBSeconds != 5*gbh || current.IncludedMBSeconds != api.IncludedMBSeconds(api.PlanHobby) {
		t.Fatalf("October = %+v, want Hobby's allowance and 5 GB-h billed", current)
	}

	future, err := ComputeOverageForMonth(ctx, store, acct, now.AddDate(0, 1, 0), now, 0)
	if err != nil || future != (MonthComputeOverage{}) {
		t.Fatalf("a month not started = %+v, %v; want nothing", future, err)
	}
}
