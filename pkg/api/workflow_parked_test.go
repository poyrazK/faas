package api

// adr: 20261023215834

import "testing"

func TestWorkflowParkedAllowance(t *testing.T) {
	for plan, want := range map[Plan]int{PlanFree: 0, PlanHobby: 100, PlanPro: 1000, PlanScale: 10000} {
		if got := plan.WorkflowMaxParkedRuns(); got != want {
			t.Errorf("%s parked allowance = %d, want %d", plan, got, want)
		}
	}
	for _, c := range []struct{ active, parked, maxParked, want int }{
		{3, 0, 100, 3},
		{3, 100, 100, 3},
		{3, 101, 100, 4},
		{0, 5, 0, 5},
	} {
		if got := WorkflowAdmittedRuns(c.active, c.parked, c.maxParked); got != c.want {
			t.Errorf("WorkflowAdmittedRuns(%d, %d, %d) = %d, want %d", c.active, c.parked, c.maxParked, got, c.want)
		}
	}
}
