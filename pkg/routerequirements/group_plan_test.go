package routerequirements

// ADR-446: captured route-group planning, impact and transactional verification.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const groupPlanConfig = `{"version":2,"groups":[{"name":"api","path_prefix":"/api/","methods":["POST"],"require":{"throttle":{"key_by":"none","max_rps":3},"budget":{"explicit":true,"max_ms":1000}}}],"public":[{"method":"GET","path":"/health","reason":"private-rationale-never-export"}]}`

func groupInventory(routes ...CapturedRoute) CoverageInventory {
	return CoverageInventory{Status: "available", Source: "captured_candidate_contract", Deployment: "00000000-0000-4000-8000-000000000001", SHA256: strings.Repeat("a", 64), Routes: routes, RouteCount: len(routes)}
}

func TestGroupPlanFullInventoryAndOverlappingLimits(t *testing.T) {
	config := coverageConfig(t, groupPlanConfig)
	config.Groups = append(config.Groups, RouteGroup{Name: "stricter", PathPrefix: "/api/orders/", Methods: []string{"POST"}, Require: Checks{Throttle: &ThrottleRequirement{KeyBy: "none", MaxRPS: floatPtr(1)}}})
	inventory := groupInventory(CapturedRoute{Method: "GET", Path: "/health"}, CapturedRoute{Method: "POST", Path: "/api/orders/{id}"}, CapturedRoute{Method: "POST", Path: "/api/orders/{id}/items"})
	context := requirementContext()
	original, _ := json.Marshal(config)
	plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ThrottleBurst: 5}, inventory)
	if err != nil || plan.Status != "ready" || len(plan.Changes) != 2 || plan.Before.Status != "violated" || plan.After.Status != "satisfied" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if plan.Version != 3 || plan.DeploymentID != inventory.Deployment || len(plan.After.Assignments) != 3 || len(plan.After.Groups) != 2 {
		t.Fatalf("binding/coverage: %+v", plan)
	}
	for _, change := range plan.Changes {
		if change.Create.MatchPath != "/api/orders/*" || !change.Impact.BeyondCapture || len(change.Impact.Captured) != 2 {
			t.Fatalf("impact: %+v", change)
		}
		if change.Kind == "throttle" {
			var action api.EdgeRuleThrottleAction
			_ = json.Unmarshal(change.Create.Action, &action)
			if action.RequestsPerSecond != 1 {
				t.Fatal("overlapping ceiling lost")
			}
		}
	}
	body, _ := json.Marshal(plan)
	if strings.Contains(string(body), "private-rationale-never-export") {
		t.Fatal("public rationale exported")
	}
	if err := ValidateArtifact(plan, context.App.Slug); err != nil {
		t.Fatal(err)
	}
	repeated, err := BuildServerGroupPlan(*plan.Requirements, context, PlanOptions{PlanName: "pro", ThrottleBurst: 5}, inventory)
	if err != nil || repeated.SHA256 != plan.SHA256 {
		t.Fatal("normalization changed fingerprint")
	}
	current, _ := json.Marshal(config)
	if string(current) != string(original) {
		t.Fatal("config mutated")
	}
	if len(inventory.Routes) != 3 {
		t.Fatal("inventory mutated")
	}
	next := applyPlannedChanges(context, plan)
	verified, err := BuildServerGroupPlan(config, next, PlanOptions{PlanName: "pro", ThrottleBurst: 5}, inventory)
	if err != nil || verified.Status != "no_changes" || verified.Before.Status != "satisfied" {
		t.Fatalf("verification: %+v %v", verified, err)
	}
	inventory.SHA256 = strings.Repeat("b", 64)
	changed, _ := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ThrottleBurst: 5}, inventory)
	if changed.SHA256 == plan.SHA256 {
		t.Fatal("contract not fingerprinted")
	}
}

func TestGroupPlanPublicExceptionCannotChange(t *testing.T) {
	config := coverageConfig(t, groupPlanConfig)
	config.Public = append(config.Public, PublicRoute{Method: "POST", Path: "/api/orders/status", Reason: "public"})
	inventory := groupInventory(CapturedRoute{Method: "GET", Path: "/health"}, CapturedRoute{Method: "POST", Path: "/api/orders/{id}"}, CapturedRoute{Method: "POST", Path: "/api/orders/status"})
	plan, err := BuildServerGroupPlan(config, requirementContext(), PlanOptions{PlanName: "pro", ThrottleBurst: 5}, inventory)
	if err != nil || plan.Status != "blocked" || len(plan.Changes) != 0 {
		t.Fatalf("public changed: %+v %v", plan, err)
	}
	found := false
	for _, unresolved := range plan.Unresolved {
		found = found || unresolved.Code == "public_exception_would_change"
	}
	if !found {
		t.Fatalf("missing public blocker: %+v", plan.Unresolved)
	}
}

func TestGroupPlanConflictsAndCoverageAreUnresolved(t *testing.T) {
	for _, scenario := range []string{"conflicting groups", "empty group", "uncovered route", "unknown selector", "unavailable inventory"} {
		t.Run(scenario, func(t *testing.T) {
			config := coverageConfig(t, groupPlanConfig)
			inventory := groupInventory(CapturedRoute{Method: "GET", Path: "/health"}, CapturedRoute{Method: "POST", Path: "/api/orders/{id}"})
			context := requirementContext()
			switch scenario {
			case "conflicting groups":
				config.Groups = append(config.Groups, RouteGroup{Name: "consumer", PathPrefix: "/api/", Methods: []string{"POST"}, Require: Checks{Throttle: &ThrottleRequirement{KeyBy: "consumer_id", MaxRPS: floatPtr(1)}}})
			case "empty group":
				config.Groups = append(config.Groups, RouteGroup{Name: "empty", PathPrefix: "/absent/", Methods: []string{"POST"}, Require: Checks{Budget: &BudgetRequirement{Explicit: true}}})
			case "uncovered route":
				inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "DELETE", Path: "/api/orders/{id}"})
			case "unknown selector":
				context.Rules = []api.EdgeRuleResponse{requirementRule("partial", "throttle", sharedThrottle, 0)}
				context.Rules[0].MatchPath = "/api/orders/admin"
			case "unavailable inventory":
				inventory.Status, inventory.Code = "unavailable", "candidate_contract_truncated"
			}
			plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ThrottleBurst: 5}, inventory)
			if err != nil || (plan.Status != "blocked" && plan.Status != "partial") || len(plan.Unresolved) == 0 {
				t.Fatalf("accepted unresolved: %+v %v", plan, err)
			}
			if err := ValidateArtifact(plan, context.App.Slug); err == nil {
				t.Fatal("unresolved artifact accepted")
			}
		})
	}
}

func TestGroupPlanUpdatesFamilyRuleAndPreservesSatisfiedOverlaps(t *testing.T) {
	config := coverageConfig(t, groupPlanConfig)
	inventory := groupInventory(CapturedRoute{Method: "GET", Path: "/health"}, CapturedRoute{Method: "POST", Path: "/api/orders/{id}"})
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("family-budget", "budget", `{"budget":{"budget_ms":2000}}`, 0)}
	context.Rules[0].MatchHost, context.Rules[0].MatchPath = context.Host, "/api/orders/*"
	config.Groups[0].Require.Throttle = nil
	plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro"}, inventory)
	if err != nil || plan.Status != "ready" || len(plan.Changes) != 1 || plan.Changes[0].Operation != "update" || plan.Changes[0].RuleID != "family-budget" {
		t.Fatalf("family update: %+v %v", plan, err)
	}
	config.Groups = append(config.Groups, RouteGroup{Name: "satisfied", PathPrefix: "/api/", Methods: []string{"POST"}, Require: Checks{Throttle: &ThrottleRequirement{KeyBy: "none"}}})
	config.Groups[0].Require.Throttle = &ThrottleRequirement{KeyBy: "consumer_id", MaxRPS: floatPtr(1)}
	context.Rules = append(context.Rules, requirementRule("shared", "throttle", sharedThrottle, 10))
	plan, err = BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro"}, inventory)
	if err != nil || plan.Status != "partial" {
		t.Fatalf("conflict=%+v %v", plan, err)
	}
	for _, change := range plan.Changes {
		if change.Kind == "throttle" {
			t.Fatal("satisfied overlap was changed")
		}
	}
}

func TestGroupPlanBoundedWorkDiscardsPartialChanges(t *testing.T) {
	config := coverageConfig(t, groupPlanConfig)
	config.Groups[0].Require.Throttle = nil
	inventory := groupInventory(CapturedRoute{Method: "GET", Path: "/health"})
	for i := 0; i < api.RouteGroupPlanMaxChanges+1; i++ {
		inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: fmt.Sprintf("/api/%d", i)})
	}
	plan, err := BuildServerGroupPlan(config, requirementContext(), PlanOptions{PlanName: "pro"}, inventory)
	if err != nil || len(plan.Changes) != 0 || plan.Status != "blocked" || plan.After.Status != "unknown" || len(plan.Unresolved) == 0 {
		t.Fatalf("partial success after bound: %+v %v", plan, err)
	}
}

func floatPtr(value float64) *float64 { return &value }

func TestGroupPlanPublicMutationCannotHideImpact(t *testing.T) {
	config := coverageConfig(t, groupPlanConfig)
	config.Public = append(config.Public, PublicRoute{Method: "POST", Path: "/public", Reason: "public"})
	inventory := groupInventory(CapturedRoute{Method: "GET", Path: "/health"}, CapturedRoute{Method: "POST", Path: "/public"}, CapturedRoute{Method: "POST", Path: "/api/orders/{id}"})
	context := requirementContext()
	rewrite := requirementRule("public-rewrite", "rewrite", `{"rewrite":{"path":"/api/orders/x"}}`, 0)
	rewrite.MatchPath = "/public"
	context.Rules = []api.EdgeRuleResponse{rewrite}
	plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ThrottleBurst: 5}, inventory)
	if err != nil || len(plan.Changes) != 0 || plan.Status != "blocked" {
		t.Fatalf("unproven public context: %+v %v", plan, err)
	}
	for _, finding := range plan.Unresolved {
		if finding.Code != "impact_unavailable" {
			t.Fatalf("unexpected reason: %+v", finding)
		}
	}
}

func TestGroupPlanPublicImpactCannotBypassWorkBudget(t *testing.T) {
	config := coverageConfig(t, groupPlanConfig)
	config.Groups[0].Require.Throttle = nil
	inventory := groupInventory(CapturedRoute{Method: "GET", Path: "/health"}, CapturedRoute{Method: "POST", Path: "/api/orders/{id}"})
	for i := 0; i < 200; i++ {
		path := fmt.Sprintf("/api/orders/x/public-%d", i)
		config.Public = append(config.Public, PublicRoute{Method: "POST", Path: path, Reason: "public"})
		inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: path})
	}
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("broad", "budget", `{"budget":{"budget_ms":3000}}`, 10)}
	// An existing opaque public policy is preserved, never copied into a patch.
	// Repeated impact comparisons must still count its bytes toward plan bounds.
	body, _ := json.Marshal(map[string]any{"budget": map[string]int{"budget_ms": 500}, "private": strings.Repeat("private-opaque-policy", 4096)})
	public := requirementRule("public-policy", "budget", string(body), 0)
	public.MatchPath = "/api/orders/*/*"
	context.Rules = append(context.Rules, public)
	inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: "/api/other"})
	for _, consolidate := range []bool{false, true} {
		plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ConsolidateBudgets: consolidate}, inventory)
		if err != nil || plan.Status != "blocked" || len(plan.Changes) != 0 || plan.After.Coverage.Code != "group_plan_limit_exceeded" {
			t.Fatalf("unbounded public impact (consolidate=%t): %+v %v", consolidate, plan, err)
		}
		exported, _ := json.Marshal(plan)
		if strings.Contains(string(exported), "private-opaque-policy") {
			t.Fatal("opaque policy exported")
		}
		if consolidate && plan.RuleUsage.Before != plan.RuleUsage.After {
			t.Fatal("discarded work counted as proposed rules")
		}
	}
}
