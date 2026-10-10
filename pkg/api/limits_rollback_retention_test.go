// adr: 972

package api

import "testing"

func TestRollbackRetentionDeploymentsPerPlan(t *testing.T) {
	want := map[Plan]int{PlanFree: 2, PlanHobby: 3, PlanPro: 3, PlanScale: 5}
	for _, plan := range Plans {
		if got := MustLimitsFor(plan).RollbackRetentionDeployments; got != want[plan] {
			t.Errorf("%s RollbackRetentionDeployments = %d, want %d", plan, got, want[plan])
		}
		if got := RollbackRetentionDeploymentsFor(plan); got != want[plan] {
			t.Errorf("RollbackRetentionDeploymentsFor(%s) = %d, want %d", plan, got, want[plan])
		}
	}
	if got := MaxRollbackRetentionDeployments(); got != 5 {
		t.Errorf("MaxRollbackRetentionDeployments = %d, want 5", got)
	}
	if got := RollbackRetentionDeploymentsFor(Plan("unknown")); got != MaxRollbackRetentionDeployments() {
		t.Errorf("unknown plan window = %d, want deepest %d", got, MaxRollbackRetentionDeployments())
	}
}
