package egressoverage_test

// adr: 20261104204500

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/egressoverage"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

type egressPolicy struct {
	billing.Provider
	policy billing.MeterUsagePolicy
}

func (p egressPolicy) MeterUsagePolicy(plan api.Plan, meter state.BillingMeter) (billing.MeterUsagePolicy, bool) {
	return p.policy, meter == state.BillingMeterEgress && plan != api.PlanFree
}

func TestViewReportsBillingAndPrice(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	on := state.EgressOverageElection{EnabledAt: &start}
	live := egressPolicy{policy: billing.MeterUsagePolicy{Mode: billing.MeterDeliveryLive, EffectiveFrom: now.Add(-24 * time.Hour), UnitQuantity: 1 << 30, MillicentsPerUnit: 3000}}
	perByte := egressPolicy{policy: billing.MeterUsagePolicy{Mode: billing.MeterDeliveryLive, UnitQuantity: 3, MillicentsPerUnit: 1}}
	shadow := egressPolicy{policy: billing.MeterUsagePolicy{Mode: billing.MeterDeliveryShadow, UnitQuantity: 1 << 30, MillicentsPerUnit: 3000}}
	for _, tc := range []struct {
		name     string
		provider billing.Provider
		plan     api.Plan
		billing  string
		price    int64
	}{
		{"live", live, api.PlanPro, api.EgressOverageBillingLive, 3000},
		{"price rounds up", perByte, api.PlanPro, api.EgressOverageBillingLive, (1<<30 + 2) / 3},
		{"shadow", shadow, api.PlanPro, api.EgressOverageBillingOff, 0},
		{"no provider", nil, api.PlanPro, api.EgressOverageBillingOff, 0},
		{"free", live, api.PlanFree, api.EgressOverageBillingOff, 0},
	} {
		got := egressoverage.View(tc.provider, tc.plan, on, now)
		if got.Billing != tc.billing || got.PriceMillicentsPerGiB != tc.price || !got.Enabled || got.Electable != (tc.plan != api.PlanFree) {
			t.Errorf("%s: view = %+v", tc.name, got)
		}
	}
}

func TestSetRefusesOnlyTurningOnForFree(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "egress-overage@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	if _, err := egressoverage.Set(ctx, store, acct.ID, api.PlanFree, true, now); !errors.Is(err, egressoverage.ErrNotElectable) {
		t.Fatalf("free on: err = %v, want ErrNotElectable", err)
	}
	if got, err := egressoverage.Set(ctx, store, acct.ID, api.PlanFree, false, now); err != nil || got.On() {
		t.Fatalf("free off = %+v, %v", got, err)
	}
	if got, err := egressoverage.Set(ctx, store, acct.ID, api.PlanHobby, true, now); err != nil || !got.On() {
		t.Fatalf("hobby on = %+v, %v", got, err)
	}
	if p := egressoverage.NotElectableProblem(api.PlanFree); p.Status != http.StatusPaymentRequired || p.Code != api.CodePlanEgressOverageNotAllowed || p.DocsURL == "" {
		t.Fatalf("problem = %+v", p)
	}
}
