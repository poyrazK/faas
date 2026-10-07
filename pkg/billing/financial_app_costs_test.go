package billing

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func TestScopeFinancialCostsToAppFiltersStableIdentityAndReaggregates(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	attribution := financial.Attribution{AppID: "app-1", Name: "api", DeploymentID: "dep-1"}
	report := api.FinancialCostsResponse{
		Currency: "EUR", PeriodStart: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), AsOf: now,
		KnownUsageMillicents: 9999, MissingBillComponents: []string{"tax"},
		Meters: []api.FinancialMeterCosts{{
			Meter:    "compute",
			Coverage: api.FinancialMeterCoverage{Complete: true, Fresh: true, ExpectedMinutes: 100, CompleteMinutes: 100},
			Accrued: financial.ContractCosts{Meter: "compute", Contracts: []financial.MeterCost{
				{Price: financial.Price{Version: "v1", Meter: "compute", Unit: "GB-hour"}, Allocations: []financial.Allocation{
					{Attribution: attribution, Quantity: 10, NetMillicents: 100},
					{Attribution: financial.Attribution{AppID: "other-app", Name: "api"}, Quantity: 90, NetMillicents: 900},
					{Attribution: financial.Attribution{JobID: "job-1", Name: "api"}, Quantity: 30, NetMillicents: 300},
				}},
				{Price: financial.Price{Version: "v2", Meter: "compute", Unit: "GB-hour"}, Allocations: []financial.Allocation{
					{Attribution: attribution, Quantity: 7, NetMillicents: 70},
				}},
			}},
		}},
	}

	got, err := ScopeFinancialCostsToApp(report, "app-1", "api")
	if err != nil {
		t.Fatal(err)
	}
	if got.KnownUsageMillicents != 170 || got.BillEstimateAvailable || got.Scope != "application_attributed_retained_compute_and_interface_egress" {
		t.Fatalf("unexpected app summary: %+v", got)
	}
	if len(got.Meters) != 1 || got.Meters[0].Quantity != 17 || got.Meters[0].NetMillicents != 170 || len(got.Meters[0].Allocations) != 1 {
		t.Fatalf("unexpected scoped meter: %+v", got.Meters)
	}
	if got.Meters[0].Allocations[0].Attribution != attribution || got.Meters[0].Allocations[0].Quantity != 17 || got.Meters[0].Allocations[0].NetMillicents != 170 {
		t.Fatalf("allocations were not reaggregated by app workload: %+v", got.Meters[0].Allocations)
	}
	if len(got.MissingBillComponents) != 1 || got.MissingBillComponents[0] != "tax" {
		t.Fatalf("missing bill components lost: %+v", got.MissingBillComponents)
	}
	got.MissingBillComponents[0] = "changed"
	if report.MissingBillComponents[0] != "tax" {
		t.Fatal("scoped response aliases the account report")
	}
}
