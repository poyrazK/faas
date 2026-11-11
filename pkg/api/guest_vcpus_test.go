// adr: 20261101160231

package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppCPUBaselineAndCeilingFollowPlanRAM(t *testing.T) {
	for _, tc := range []struct {
		plan              api.Plan
		baseline, ceiling int
	}{
		{api.PlanFree, 250, 1000}, {api.PlanHobby, 500, 1000}, {api.PlanPro, 1000, 1000}, {api.PlanScale, 2000, 2000},
		{api.Plan("unknown"), 0, 0},
	} {
		if got := api.AppCPUBaselineMillicores(tc.plan); got != tc.baseline {
			t.Errorf("AppCPUBaselineMillicores(%s) = %d, want %d", tc.plan, got, tc.baseline)
		}
		if got := api.AppCPUCeilingMillicores(tc.plan); got != tc.ceiling {
			t.Errorf("AppCPUCeilingMillicores(%s) = %d, want %d", tc.plan, got, tc.ceiling)
		}
	}
}

func TestValidAppCPUCeilingMillicores(t *testing.T) {
	for _, tc := range []struct {
		plan api.Plan
		v    int
		want bool
	}{
		{api.PlanFree, 250, true}, {api.PlanFree, 1000, true}, {api.PlanFree, 2000, false},
		{api.PlanPro, 2000, false}, {api.PlanScale, 2000, true}, {api.PlanScale, 1500, false},
		{api.PlanScale, 3000, false}, {api.PlanScale, 0, false}, {api.Plan("unknown"), 2000, false},
	} {
		if got := api.ValidAppCPUCeilingMillicores(tc.plan, tc.v); got != tc.want {
			t.Errorf("ValidAppCPUCeilingMillicores(%s, %d) = %t, want %t", tc.plan, tc.v, got, tc.want)
		}
	}
	for v, want := range map[int]bool{250: true, 500: true, 1000: true, 2000: true, 1500: false, 3000: false, 0: false} {
		if got := api.ValidAnyAppCPUCeilingMillicores(v); got != want {
			t.Errorf("ValidAnyAppCPUCeilingMillicores(%d) = %t, want %t", v, got, want)
		}
	}
	// Customers still choose only the shapes they could before launch.
	if api.ValidAppCPUMillicores(2000) {
		t.Error("ValidAppCPUMillicores(2000) = true; the customer shapes change only at launch")
	}
}
