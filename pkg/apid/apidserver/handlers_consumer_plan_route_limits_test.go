package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 20261123192254 — plans carry per-route (MCP tool) limits.
func TestAPIConsumerPlanRouteLimits(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "tool-caps")
	plans := "/v1/apps/tool-caps/consumer-plans"
	bad := api.CreateAPIConsumerPlanRequest{Name: "bad", RouteLimits: map[string]api.APIConsumerPlanRouteLimit{"generate": {MaxRequestsPerMonth: 1}}}
	if res := e.Do(t, http.MethodPost, plans, bad, nil); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a non-route key: %d %s, want 422", res.Code, res.Body)
	}
	res := e.Do(t, http.MethodPost, plans, api.CreateAPIConsumerPlanRequest{Name: "free",
		RouteLimits: map[string]api.APIConsumerPlanRouteLimit{"MCP /tools/generate": {MaxRequestsPerMonth: 100}}}, nil)
	var plan api.APIConsumerPlanResponse
	if err := json.Unmarshal(res.Body.Bytes(), &plan); err != nil || res.Code != http.StatusCreated ||
		plan.RouteLimits["MCP /tools/generate"].MaxRequestsPerMonth != 100 {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	res = e.Do(t, http.MethodPut, plans+"/"+plan.ID, api.UpdateAPIConsumerPlanLimitsRequest{MaxRequestsPerMinute: 60}, nil)
	if err := json.Unmarshal(res.Body.Bytes(), &plan); err != nil || res.Code != http.StatusOK || len(plan.RouteLimits) != 1 {
		t.Fatalf("an update without route_limits must keep them: %d %s", res.Code, res.Body)
	}
	cleared := map[string]api.APIConsumerPlanRouteLimit{}
	res = e.Do(t, http.MethodPut, plans+"/"+plan.ID, api.UpdateAPIConsumerPlanLimitsRequest{MaxRequestsPerMinute: 60, RouteLimits: &cleared}, nil)
	var clearedPlan api.APIConsumerPlanResponse
	if err := json.Unmarshal(res.Body.Bytes(), &clearedPlan); err != nil || res.Code != http.StatusOK || clearedPlan.RouteLimits == nil || len(clearedPlan.RouteLimits) != 0 {
		t.Fatalf("an empty route_limits must clear them: %d %s", res.Code, res.Body)
	}
}
