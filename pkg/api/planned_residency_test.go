package api_test

import (
	"math"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The planned resident GB per paying customer is the financial model's
// resident concurrency (spec §1: 0.02 / 0.15 / 0.60 / 3.00) times the plan's
// billable instance size. FaasResidentGbPerCustomerHigh compares each plan with
// it (ADR-20261117070932).
func TestPlannedResidentGBPerCustomer(t *testing.T) {
	cases := []struct {
		plan        api.Plan
		concurrency float64
		wantGB      float64
	}{
		{api.PlanFree, 0.02, 0.02 * 264 / 1024},
		{api.PlanHobby, 0.15, 0.15 * 520 / 1024},
		{api.PlanPro, 0.60, 0.60 * 1032 / 1024},
		{api.PlanScale, 3.00, 3.00 * 2056 / 1024},
	}
	if len(cases) != len(api.Plans) {
		t.Fatalf("cases cover %d plans, api.Plans has %d", len(cases), len(api.Plans))
	}
	for _, tc := range cases {
		t.Run(string(tc.plan), func(t *testing.T) {
			c, ok := api.PlannedResidentConcurrency(tc.plan)
			if !ok || c != tc.concurrency {
				t.Fatalf("PlannedResidentConcurrency = %v, %v; want %v, true", c, ok, tc.concurrency)
			}
			gb, ok := api.PlannedResidentGBPerCustomer(tc.plan)
			if !ok || math.Abs(gb-tc.wantGB) > 1e-12 {
				t.Fatalf("PlannedResidentGBPerCustomer = %v, %v; want %v, true", gb, ok, tc.wantGB)
			}
		})
	}
	if _, ok := api.PlannedResidentGBPerCustomer(api.Plan("enterprise")); ok {
		t.Fatal("an unknown plan has a planned residency")
	}
}
