package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// `release traffic rollback` undoes a split in one environment: it aborts a
// canary rolling out there, or gives the release with the largest share of a
// manual split all traffic.
func TestPlanTrafficRollback(t *testing.T) {
	live := func(id string, revision, percent int) api.DeploymentResponse {
		return api.DeploymentResponse{ID: id, Revision: revision, Status: "live", TrafficPercent: percent}
	}
	v5, v6, v7 := live("d5", 5, 0), live("d6", 6, 90), live("d7", 7, 10)

	plan, err := planTrafficRollback([]api.DeploymentResponse{v7, v6, v5}, "", "")
	if err != nil || plan.abort || plan.target.ID != "d6" {
		t.Fatalf("canary-style 90/10 split = %+v, %v; want v6", plan, err)
	}
	// Review finding: some traffic sent back to an OLDER release must not
	// promote that older release.
	back := []api.DeploymentResponse{live("d7", 7, 90), live("d5", 5, 10)}
	if plan, err := planTrafficRollback(back, "", ""); err != nil || plan.target.ID != "d7" {
		t.Fatalf("10%% sent back to v5 = %+v, %v; want v7, the serving release", plan, err)
	}
	if _, err := planTrafficRollback([]api.DeploymentResponse{live("a", 1, 50), live("b", 2, 50)}, "", ""); err == nil || !strings.Contains(err.Error(), "--to") {
		t.Fatalf("50/50 tie = %v, want --to required", err)
	}
	if plan, err := planTrafficRollback([]api.DeploymentResponse{v7, v6, v5}, "", "v5"); err != nil || plan.target.ID != "d5" {
		t.Fatalf("--to v5 = %+v, %v", plan, err)
	}
	// An exact ID wins over a revision with the same digits.
	digits := []api.DeploymentResponse{live("x", 7, 60), live("7", 3, 40)}
	if plan, err := planTrafficRollback(digits, "", "7"); err != nil || plan.target.ID != "7" {
		t.Fatalf("--to 7 = %+v, %v; want the deployment whose ID is 7", plan, err)
	}

	canary := v7
	canary.CanaryTotalSteps, canary.RolloutState = 4, "rolling_out"
	if plan, err := planTrafficRollback([]api.DeploymentResponse{canary, v6}, "", ""); err != nil || !plan.abort {
		t.Fatalf("running canary = %+v, %v; want an abort", plan, err)
	}

	// A canary in staging must not decide production's rollback.
	staging := canary
	staging.ID, staging.Scope = "s8", "staging"
	prod := []api.DeploymentResponse{staging, v6, v7}
	if plan, err := planTrafficRollback(prod, "default", ""); err != nil || plan.abort || plan.target.ID != "d6" {
		t.Fatalf("production split with a staging canary = %+v, %v; want v6, no abort", plan, err)
	}
	if plan, err := planTrafficRollback(prod, "default", "v7"); err != nil || plan.target.ID != "d7" {
		t.Fatalf("--env default --to v7 = %+v, %v", plan, err)
	}
	if _, err := planTrafficRollback(prod, "", ""); err == nil || !strings.Contains(err.Error(), "--env") {
		t.Fatalf("two environments without --env = %v", err)
	}
	prodCanary := canary
	if _, err := planTrafficRollback([]api.DeploymentResponse{staging, prodCanary, v6}, "default", ""); err == nil || !strings.Contains(err.Error(), "more than one environment") {
		t.Fatalf("canaries in two environments = %v, want a refusal", err)
	}

	whole := v6
	whole.TrafficPercent = 100
	for name, tc := range map[string]struct {
		live []api.DeploymentResponse
		to   string
		want string
	}{
		"not split":        {[]api.DeploymentResponse{whole, v5}, "", "gregale rollback"},
		"unknown --to":     {[]api.DeploymentResponse{v7, v6}, "v9", "not a live release"},
		"--to with canary": {[]api.DeploymentResponse{canary, v6}, "v6", "--to does not apply"},
	} {
		if _, err := planTrafficRollback(tc.live, "", tc.to); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestSameTrafficWeightsNoticesAChange(t *testing.T) {
	a := []api.DeploymentResponse{{ID: "d6", TrafficPercent: 90}, {ID: "d7", TrafficPercent: 10}}
	if !sameTrafficWeights(a, []api.DeploymentResponse{{ID: "d7", TrafficPercent: 10}, {ID: "d6", TrafficPercent: 90}}) {
		t.Fatal("the same weights in another order differed")
	}
	if sameTrafficWeights(a, []api.DeploymentResponse{{ID: "d6", TrafficPercent: 0}, {ID: "d7", TrafficPercent: 100}}) {
		t.Fatal("a promotion between the reads went unnoticed")
	}
	if sameTrafficWeights(a, a[:1]) {
		t.Fatal("a release leaving the live set went unnoticed")
	}
}
