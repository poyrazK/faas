package meter_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCostControlLegacyProviderHonorsZeroSpendCap(t *testing.T) {
	store := state.NewMemStore()
	account := makeBillableAccount(t, t.Context(), store, api.PlanHobby)
	store.SetOverageCapCentsForTest(account.ID, 0)
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	usage := api.IncludedMBSeconds(api.PlanHobby) + 50*api.SecondsPerGBHour
	if err := store.AppendUsage(t.Context(), account.ID, "audit-app", "audit-instance", now.Add(-time.Hour), usage, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	completeUsageThrough(t, t.Context(), store, now)
	provider := &recordingStripe{}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	pushed, err := pusher.PushPending(t.Context(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.Calls()) != 0 {
		t.Errorf("zero spend cap still sent %d raw-provider billing window(s), first quantity=%d MB-seconds; pushed=%d", len(provider.Calls()), provider.Calls()[0].MBSeconds, pushed)
	}
}

type costControlPlanEgressProvider struct{ recordingEgressProvider }

func (p *costControlPlanEgressProvider) MeterUsagePolicy(plan api.Plan, meter state.BillingMeter) (billing.MeterUsagePolicy, bool) {
	policy, ok := p.recordingEgressProvider.MeterUsagePolicy(plan, meter)
	limits, valid := api.LimitsFor(plan)
	if !valid {
		return policy, false
	}
	policy.IncludedQuantity = limits.MonthlyEgressGiB * (1 << 30)
	return policy, ok
}

func TestCostControlHistoricalEgressKeepsRecordedAllowance(t *testing.T) {
	store := state.NewMemStore()
	account := makeBillableAccount(t, t.Context(), store, api.PlanHobby)
	electEgressOverage(t, t.Context(), store, account.ID)
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(15*24*time.Hour + 12*time.Hour)
	change := now.Add(-time.Hour)
	for _, term := range []struct {
		plan api.Plan
		at   time.Time
	}{{api.PlanScale, month}, {api.PlanHobby, change}} {
		limits, valid := api.LimitsFor(term.plan)
		if !valid {
			t.Fatal("invalid fixture plan")
		}
		// Plan history is carried by compute activations; the egress
		// snapshot below retains the financial reporting contract.
		if _, err := store.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{
			AccountID: account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: term.at,
			Plan: term.plan, DeliveryMode: "live", Price: financial.Price{Version: "audit-compute-" + string(term.plan),
				Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: api.SecondsPerGBHour,
				MillicentsPerUnit: api.OverageMillicentsPerGBHour, IncludedQuantity: api.IncludedMBSeconds(term.plan)},
		}); err != nil {
			t.Fatal(err)
		}
		_, err := store.PutFinancialPriceSnapshot(t.Context(), state.FinancialPriceSnapshot{AccountID: account.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: term.at, Plan: term.plan, DeliveryMode: "live", Price: financial.Price{Version: "audit-egress-" + string(term.plan), Meter: "egress", Currency: "USD", Unit: "interface_bytes", UnitQuantity: 1 << 30, MillicentsPerUnit: 2000, IncludedQuantity: limits.MonthlyEgressGiB * (1 << 30)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AppendUsage(t.Context(), account.ID, "audit-app", "audit-egress", now.Add(-2*time.Hour+time.Minute), 0, 0, 0, 0, 150*(1<<30), 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	completeUsageThrough(t, t.Context(), store, now)
	provider := &costControlPlanEgressProvider{recordingEgressProvider: recordingEgressProvider{mode: billing.MeterDeliveryLive}}
	pusher := meter.NewPusher(store, provider, discardLog(), func() time.Time { return now }, nil)
	if _, err := pusher.PushPending(t.Context(), 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	report, err := billing.ReadFinancialCosts(t.Context(), store, account.ID, month, now)
	if err != nil {
		t.Fatal(err)
	}
	var known int64
	for _, m := range report.Meters {
		if m.Meter == "egress" {
			known = m.Accrued.NetMillicents
		}
	}
	for _, call := range provider.MeterCalls() {
		if call.Quantity > 0 {
			t.Errorf("historical Scale egress replayed with current Hobby allowance: provider quantity=%d GiB (charge=%d millicents); retained ledger charge=%d millicents", call.Quantity/(1<<30), call.Quantity/(1<<30)*2000, known)
		}
	}
}
