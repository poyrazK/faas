package apidserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Every alias keeps its deployment's layers out of retention, and layer
// storage is not billed, so an app could pin any number of layers (deploy
// review 2026-11-10, F1). A new alias above the plan's DeploymentAliasesMax is
// refused; re-pointing an existing one is not.
func TestDeploymentAliases_PlanCap(t *testing.T) {
	e := apidserver.Setup(t, api.PlanFree)
	dep := apidserver.MustSeedDeployment(t, e, "alias-cap")
	limit := api.MustLimitsFor(api.PlanFree).DeploymentAliasesMax
	if limit <= 0 {
		t.Fatalf("Free DeploymentAliasesMax = %d, want a cap", limit)
	}
	put := func(name string) int {
		rec := e.Do(t, http.MethodPut, "/v1/apps/alias-cap/deployment-aliases/"+name,
			api.SetDeploymentAliasRequest{DeploymentID: dep.ID}, nil)
		return rec.Code
	}
	for i := 0; i < limit; i++ {
		if code := put(fmt.Sprintf("a%d", i)); code != http.StatusOK {
			t.Fatalf("alias %d status=%d, want 200", i, code)
		}
	}

	over := e.Do(t, http.MethodPut, "/v1/apps/alias-cap/deployment-aliases/one-more",
		api.SetDeploymentAliasRequest{DeploymentID: dep.ID}, nil)
	var problem api.Problem
	if over.Code != http.StatusForbidden || json.Unmarshal(over.Body.Bytes(), &problem) != nil ||
		problem.Code != api.CodePlanLimitDeploymentAliases || problem.Limit == nil || *problem.Limit != int64(limit) ||
		problem.Observed == nil || *problem.Observed != int64(limit) {
		t.Fatalf("alias over the cap = %d %s; want 403 plan_limit_deployment_aliases with limit and observed %d", over.Code, over.Body, limit)
	}

	if code := put("a0"); code != http.StatusOK {
		t.Fatalf("re-pointing an existing alias at the cap = %d, want 200", code)
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/apps/alias-cap/deployment-aliases/a1", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if code := put("one-more"); code != http.StatusOK {
		t.Fatalf("alias after freeing a slot = %d, want 200", code)
	}
}
