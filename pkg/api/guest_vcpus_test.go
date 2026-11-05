// adr: 20261101160231

package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGuestVCPUsForCPUMillicores(t *testing.T) {
	for _, tc := range []struct{ millicores, want int }{
		{0, 1}, {250, 1}, {500, 1}, {api.DefaultAppCPUMillicores, 1}, {1001, 2}, {2000, 2}, {2500, 3},
	} {
		if got := api.GuestVCPUsForCPUMillicores(tc.millicores); got != tc.want {
			t.Errorf("GuestVCPUsForCPUMillicores(%d) = %d, want %d", tc.millicores, got, tc.want)
		}
	}
}

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

// adr: 20261101215228 — the zero CPUShape is the compiled shape; config can
// lower a ceiling but never raise it past what vmmd accepts for the plan.
func TestCPUShape(t *testing.T) {
	compiled := api.CPUShape{}
	for _, p := range api.Plans {
		l := api.MustLimitsFor(p)
		if got, want := compiled.BaselineForPlanRAM(l.RAMMB), api.AppCPUBaselineMillicoresForPlanRAM(l.RAMMB); got != want {
			t.Errorf("%s compiled baseline = %d, want %d", p, got, want)
		}
		ceiling := compiled.CeilingForPlanRAM(l.RAMMB)
		if want := api.AppCPUCeilingMillicoresForPlanRAM(l.RAMMB); ceiling != want {
			t.Errorf("%s compiled ceiling = %d, want %d", p, ceiling, want)
		}
		if got, want := compiled.VCPUs(ceiling, l.VCPU), api.GuestVCPUsForCPUMillicores(ceiling); got != want {
			t.Errorf("%s compiled vCPUs = %d, want %d", p, got, want)
		}
	}
	for _, tc := range []struct {
		name                     string
		shape                    api.CPUShape
		plan                     api.Plan
		baseline, ceiling, vcpus int
	}{
		{"floor lowered", api.CPUShape{CeilingFloorMillicores: 500}, api.PlanFree, 250, 500, 1},
		{"floor at 250", api.CPUShape{CeilingFloorMillicores: 250}, api.PlanHobby, 500, 500, 1},
		{"baseline halved", api.CPUShape{BaselineMillicoresPerGB: 500, CeilingFloorMillicores: 250}, api.PlanScale, 1000, 1000, 1},
		{"baseline rounds up to a shape", api.CPUShape{BaselineMillicoresPerGB: 750, CeilingFloorMillicores: 250}, api.PlanScale, 1500, 2000, 2},
		{"raise capped at compiled", api.CPUShape{BaselineMillicoresPerGB: 2000}, api.PlanPro, 2000, 1000, 1},
		{"floor raise capped", api.CPUShape{CeilingFloorMillicores: 2000}, api.PlanHobby, 500, 1000, 1},
		{"half-core vCPUs", api.CPUShape{VCPUMillicores: 500}, api.PlanPro, 1000, 1000, 2},
		{"vCPUs capped at plan", api.CPUShape{VCPUMillicores: 500}, api.PlanScale, 2000, 2000, 4},
	} {
		l := api.MustLimitsFor(tc.plan)
		baseline, ceiling := tc.shape.BaselineForPlanRAM(l.RAMMB), tc.shape.CeilingForPlanRAM(l.RAMMB)
		vcpus := tc.shape.VCPUs(ceiling, l.VCPU)
		if baseline != tc.baseline || ceiling != tc.ceiling || vcpus != tc.vcpus {
			t.Errorf("%s: baseline/ceiling/vCPUs = %d/%d/%d, want %d/%d/%d", tc.name, baseline, ceiling, vcpus, tc.baseline, tc.ceiling, tc.vcpus)
		}
		if !api.ValidAppCPUCeilingMillicores(tc.plan, ceiling) {
			t.Errorf("%s: ceiling %d is one vmmd refuses on %s", tc.name, ceiling, tc.plan)
		}
	}
}
