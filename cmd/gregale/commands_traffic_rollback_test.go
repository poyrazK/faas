package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// `release traffic rollback` undoes a split: it aborts a running canary, or
// gives the oldest release in a manual split all traffic.
func TestPlanTrafficRollback(t *testing.T) {
	v6 := api.DeploymentResponse{ID: "d6", Revision: 6, Status: "live", TrafficPercent: 90}
	v7 := api.DeploymentResponse{ID: "d7", Revision: 7, Status: "live", TrafficPercent: 10}
	v5 := api.DeploymentResponse{ID: "d5", Revision: 5, Status: "live", TrafficPercent: 0}

	plan, err := planTrafficRollback([]api.DeploymentResponse{v7, v6, v5}, "")
	if err != nil || plan.abort || plan.target.ID != "d6" {
		t.Fatalf("manual split = %+v, %v; want v6 (the oldest serving release)", plan, err)
	}
	plan, err = planTrafficRollback([]api.DeploymentResponse{v7, v6, v5}, "v5")
	if err != nil || plan.target.ID != "d5" {
		t.Fatalf("--to v5 = %+v, %v", plan, err)
	}
	plan, err = planTrafficRollback([]api.DeploymentResponse{v7, v6}, "d7")
	if err != nil || plan.target.ID != "d7" {
		t.Fatalf("--to by id = %+v, %v", plan, err)
	}

	canary := v7
	canary.CanaryTotalSteps, canary.RolloutState = 4, "rolling_out"
	plan, err = planTrafficRollback([]api.DeploymentResponse{canary, v6}, "")
	if err != nil || !plan.abort {
		t.Fatalf("running canary = %+v, %v; want an abort", plan, err)
	}

	whole := v6
	whole.TrafficPercent = 100
	for name, tc := range map[string]struct {
		live []api.DeploymentResponse
		to   string
		want string
	}{
		"not split":          {[]api.DeploymentResponse{whole, v5}, "", "gregale rollback"},
		"unknown --to":       {[]api.DeploymentResponse{v7, v6}, "v9", "not a live release"},
		"--to with canary":   {[]api.DeploymentResponse{canary, v6}, "v6", "--to does not apply"},
		"legacy no revision": {[]api.DeploymentResponse{{ID: "old", Status: "live", TrafficPercent: 50}, v7}, "", "--to"},
		"two environments":   {[]api.DeploymentResponse{v6, {ID: "s", Revision: 8, Status: "live", TrafficPercent: 100, Scope: "staging"}}, "", "more than one environment"},
	} {
		if _, err := planTrafficRollback(tc.live, tc.to); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}
