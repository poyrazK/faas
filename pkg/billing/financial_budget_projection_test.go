package billing_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
)

// projectionReport is a ten-day compute month: 100 units observed, 300
// projected, an allowance of 120, priced at 10 millicents a unit. App a used
// 75 units and app b 25.
func projectionReport() api.FinancialCostsResponse {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	projectedQuantity, projectedNet := int64(300), int64((300-120)*10)
	price := financial.Price{Version: "v1", Meter: "compute", Currency: "USD", Unit: "mb_seconds", UnitQuantity: 1, MillicentsPerUnit: 10, IncludedQuantity: 120}
	return api.FinancialCostsResponse{PeriodStart: start, PeriodEnd: start.AddDate(0, 0, 30), Meters: []api.FinancialMeterCosts{{
		Meter:    "compute",
		Coverage: api.FinancialMeterCoverage{Complete: true, Fresh: true},
		Accrued: financial.ContractCosts{Meter: "compute", Quantity: 100, IncludedQuantity: 120, Contracts: []financial.MeterCost{{Price: price, Quantity: 100, GrossMillicents: 1000, Allocations: []financial.Allocation{
			{Attribution: financial.Attribution{AppID: "a"}, Quantity: 75, GrossMillicents: 750},
			{Attribution: financial.Attribution{AppID: "b"}, Quantity: 25, GrossMillicents: 250},
		}}}},
		Forecast: financial.Forecast{Available: true, Meter: "compute", ProjectedQuantity: &projectedQuantity, ProjectedNetMillicents: &projectedNet},
	}}}
}

// adr: 20261019193500 — gross budgets scale by the run rate; net budgets take
// their usage share of the account's projected net charge, as the ledger
// allocates it; any missing forecast leaves the projection unavailable.
func TestProjectFinancialBudget(t *testing.T) {
	spec := func(scope financial.BudgetScope, basis string) financial.BudgetSpec {
		return financial.BudgetSpec{Name: "p", Scope: scope, Currency: "USD", Meters: []string{"compute"}, Basis: basis, LimitMillicents: 1000, NotifyMillicents: []int64{}, Mode: "monitored", Action: "notify", ResumeRule: "manual", Enabled: true}
	}
	app := financial.BudgetScope{Kind: "app", ID: "a"}
	for name, tc := range map[string]struct {
		spec financial.BudgetSpec
		want int64
	}{
		"account net":   {spec(financial.BudgetScope{Kind: "account"}, "net_usage"), 1800},
		"account gross": {spec(financial.BudgetScope{Kind: "account"}, "gross_usage"), 3000},
		"app net":       {spec(app, "net_usage"), 1350},
		"app gross":     {spec(app, "gross_usage"), 2250},
	} {
		got, reason, err := billing.ProjectFinancialBudget(projectionReport(), tc.spec)
		if err != nil || got == nil || *got != tc.want || reason != "" {
			t.Errorf("%s: %v %q %v, want %d", name, got, reason, err, tc.want)
		}
	}

	unavailable := projectionReport()
	unavailable.Meters[0].Forecast = financial.Forecast{Reason: "insufficient_history"}
	if got, reason, err := billing.ProjectFinancialBudget(unavailable, spec(app, "net_usage")); err != nil || got != nil || reason != "compute:insufficient_history" {
		t.Fatalf("unavailable forecast: %v %q %v", got, reason, err)
	}
	egress := spec(financial.BudgetScope{Kind: "account"}, "net_usage")
	egress.Meters = []string{"compute", "egress"}
	if got, reason, _ := billing.ProjectFinancialBudget(projectionReport(), egress); got != nil || reason != "egress:missing_meter" {
		t.Fatalf("missing meter: %v %q", got, reason)
	}
	idle := projectionReport()
	idle.Meters[0].Accrued = financial.ContractCosts{Meter: "compute"}
	if got, _, err := billing.ProjectFinancialBudget(idle, spec(app, "gross_usage")); err != nil || got == nil || *got != 0 {
		t.Fatalf("idle month: %v %v", got, err)
	}
}
