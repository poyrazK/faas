package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1307 — plan-create sends weights and the budget, plan-update keeps
// terms it was not given, and set-plan resolves the plan by name.
func TestCmdPlatformTenantPlans(t *testing.T) {
	var created, updated api.PlatformTenantPlanRequest
	var assigned api.AssignPlatformTenantPlanRequest
	existing := api.PlatformTenantPlanResponse{ID: "p1", Name: "growth", Currency: "EUR", PriceMillicentsPerUnit: 20,
		RouteWeights: map[string]int64{"POST /generate": 5}, RequestBudget: &api.PlatformTenantPlanBudget{MaxRequestsPerMinute: 600, MaxRequestsPerDay: 100000}}
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/account/platform-tenant-plans":
			_ = json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(existing)
		case "GET /v1/account/platform-tenant-plans":
			_ = json.NewEncoder(w).Encode(api.PlatformTenantPlanListResponse{Plans: []api.PlatformTenantPlanResponse{existing}})
		case "PUT /v1/account/platform-tenant-plans/p1":
			_ = json.NewDecoder(r.Body).Decode(&updated)
			_ = json.NewEncoder(w).Encode(existing)
		case "POST /v1/account/platform-tenants/t1/plan-assignments":
			_ = json.NewDecoder(r.Body).Decode(&assigned)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.PlatformTenantPlanAssignmentResponse{ID: "a1", TenantID: "t1", PlanID: "p1", PlanName: "growth",
				RateCardID: "c1", EffectiveFrom: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)})
		default:
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	if code := cmdPlatformTenants([]string{"plan-create", "--name", "growth", "--price-millicents", "20", "--weight", "POST /generate=5",
		"--max-requests-per-minute", "600", "--max-requests-per-day", "100000"}); code != 0 {
		t.Fatalf("plan-create exit %d", code)
	}
	if created.Name != "growth" || created.Currency != "EUR" || created.RouteWeights["POST /generate"] != 5 || created.RequestBudget == nil ||
		created.RequestBudget.MaxRequestsPerDay != 100000 {
		t.Fatalf("created = %+v", created)
	}
	if code := cmdPlatformTenants([]string{"plan-update", "--plan", "growth", "--price-millicents", "30"}); code != 0 {
		t.Fatalf("plan-update exit %d", code)
	}
	if updated.PriceMillicentsPerUnit != 30 || updated.RouteWeights["POST /generate"] != 5 || updated.RequestBudget == nil {
		t.Fatalf("updated = %+v, want the price changed and the rest kept", updated)
	}
	if code := cmdPlatformTenants([]string{"set-plan", "--id", "t1", "--plan", "growth"}); code != 0 {
		t.Fatalf("set-plan exit %d", code)
	}
	if assigned.PlanID != "p1" || !strings.Contains(stdout.String(), "c1") {
		t.Fatalf("assigned = %+v, out = %s", assigned, stdout)
	}
	if code := cmdPlatformTenants([]string{"plan-create", "--name", "x", "--max-requests-per-minute", "5"}); code == 0 {
		t.Fatal("plan-create with half a budget succeeded")
	}
}
