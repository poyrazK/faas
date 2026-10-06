package routerequirements

// ADR-447: opt-in budget consolidation within declared route-group prefixes.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func int64Ptr(value int64) *int64 { return &value }

func budgetGroupFixture(t *testing.T) (Config, Context, CoverageInventory) {
	t.Helper()
	config := coverageConfig(t, groupPlanConfig)
	config.Groups[0].Require.Throttle = nil
	return config, requirementContext(), groupInventory(CapturedRoute{Method: "GET", Path: "/health"}, CapturedRoute{Method: "POST", Path: "/api/orders"}, CapturedRoute{Method: "POST", Path: "/api/reports"})
}

func TestGroupBudgetConsolidationLargeInventoryAndQuota(t *testing.T) {
	config, context, inventory := budgetGroupFixture(t)
	for i := 0; i < api.RouteGroupPlanMaxChanges; i++ {
		inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: fmt.Sprintf("/api/item-%d", i)})
	}
	limits, _ := api.LimitsFor(api.PlanHobby)
	for i := 0; i < limits.EdgeRulesPerApp-1; i++ {
		rule := requirementRule(fmt.Sprintf("disabled-%d", i), "budget", `{"budget":{"budget_ms":2000}}`, 10)
		rule.Enabled = false
		context.Rules = append(context.Rules, rule)
	}
	configBefore, _ := json.Marshal(config)
	contextBefore, _ := json.Marshal(context)
	options := PlanOptions{PlanName: "hobby", ConsolidateBudgets: true}
	plan, err := BuildServerGroupPlan(config, context, options, inventory)
	if err != nil || plan.Status != "ready" || len(plan.Changes) != 1 || plan.After.Status != "satisfied" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	change := plan.Changes[0]
	if !plan.ConsolidateBudgets || change.BudgetGroup != "api" || change.Create.MatchPath != "/api/*" || len(change.Impact.Captured) != len(inventory.Routes)-1 || !change.Impact.BeyondCapture {
		t.Fatalf("consolidated impact: %+v", change)
	}
	if plan.RuleUsage.Before != limits.EdgeRulesPerApp-1 || plan.RuleUsage.After != limits.EdgeRulesPerApp || plan.RuleUsage.Limit != limits.EdgeRulesPerApp {
		t.Fatalf("quota: %+v", plan.RuleUsage)
	}
	if err := ValidateArtifact(plan, context.App.Slug); err != nil {
		t.Fatal(err)
	}
	repeated, _ := BuildServerGroupPlan(config, context, options, inventory)
	if repeated.SHA256 != plan.SHA256 {
		t.Fatal("unstable consolidated fingerprint")
	}
	verified, err := BuildServerGroupPlan(config, applyPlannedChanges(context, plan), options, inventory)
	if err != nil || verified.Status != "no_changes" || verified.RuleUsage.Before != verified.RuleUsage.After {
		t.Fatalf("verification: %+v %v", verified, err)
	}
	plan.ConsolidateBudgets = false
	if err := ValidateArtifact(plan, context.App.Slug); err == nil {
		t.Fatal("edited consolidation option accepted")
	}
	current, _ := json.Marshal(config)
	currentContext, _ := json.Marshal(context)
	if string(current) != string(configBefore) || string(currentContext) != string(contextBefore) {
		t.Fatal("inputs mutated")
	}
	legacy, _ := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "hobby"}, inventory)
	if legacy.Status == "ready" || legacy.ConsolidateBudgets || legacy.RuleUsage != nil {
		t.Fatal("ordinary plans unexpectedly consolidated")
	}
}

func TestGroupBudgetConsolidationFallback(t *testing.T) {
	for _, scenario := range []string{"public", "satisfied operation", "different effective actions", "priority zero", "opaque action", "header selection", "partial family", "quota full"} {
		t.Run(scenario, func(t *testing.T) {
			config, context, inventory := budgetGroupFixture(t)
			switch scenario {
			case "public":
				config.Public = append(config.Public, PublicRoute{Method: "POST", Path: "/api/status", Reason: "public"})
				inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: "/api/status"})
			case "satisfied operation":
				rule := requirementRule("keep-budget", "budget", `{"budget":{"budget_ms":500}}`, 10)
				rule.MatchPath = "/api/status"
				context.Rules = []api.EdgeRuleResponse{rule}
				inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: "/api/status"})
			case "different effective actions":
				config.Routes = []Requirement{{Method: "POST", Path: "/api/orders", Require: Checks{Budget: &BudgetRequirement{MaxMS: int64Ptr(200), Explicit: true}}}}
			case "priority zero":
				for _, path := range []string{"/api/orders", "/api/reports"} {
					rule := requirementRule(path, "budget", `{"budget":{"budget_ms":2000}}`, 0)
					rule.MatchHost, rule.MatchPath = context.Host, path
					context.Rules = append(context.Rules, rule)
				}
			case "opaque action":
				context.Rules = []api.EdgeRuleResponse{requirementRule("opaque", "budget", `{"budget":{"budget_ms":2000},"private":"secret-action"}`, 10)}
			case "header selection":
				context.Rules = []api.EdgeRuleResponse{requirementRule("conditional", "budget", `{"budget":{"budget_ms":2000}}`, 10)}
				context.Rules[0].MatchHeaders = map[string]string{"X-Private": "secret-selector"}
			case "partial family":
				inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: "/{section}/{id}"})
				config.Groups = append(config.Groups, RouteGroup{Name: "root", PathPrefix: "/", Methods: []string{"POST"}, Require: Checks{Budget: &BudgetRequirement{Explicit: true, MaxMS: int64Ptr(1000)}}})
			case "quota full":
				limits, _ := api.LimitsFor(api.PlanFree)
				for i := 0; i < limits.EdgeRulesPerApp; i++ {
					rule := requirementRule(fmt.Sprint(i), "budget", `{"budget":{"budget_ms":2000}}`, 10)
					rule.Enabled = false
					context.Rules = append(context.Rules, rule)
				}
			}
			planName := "pro"
			if scenario == "quota full" {
				planName = "free"
			}
			plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: planName, ConsolidateBudgets: true}, inventory)
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range plan.Changes {
				if change.BudgetGroup != "" {
					t.Fatalf("unsafe consolidation: %+v", plan)
				}
			}
			if scenario == "opaque action" || scenario == "header selection" || scenario == "partial family" || scenario == "quota full" {
				if plan.Status == "ready" || plan.Status == "no_changes" {
					t.Fatalf("unresolved plan accepted: %+v", plan)
				}
			} else if plan.Status != "ready" || len(plan.Changes) != 2 {
				t.Fatalf("family fallback lost: %+v", plan)
			}
			body, _ := json.Marshal(plan)
			if strings.Contains(string(body), "secret-action") || strings.Contains(string(body), "secret-selector") {
				t.Fatal("private policy exported")
			}
		})
	}
}

func TestGroupBudgetConsolidationPreservesHigherPriorityExceptions(t *testing.T) {
	config, context, inventory := budgetGroupFixture(t)
	context.Rules = []api.EdgeRuleResponse{requirementRule("broad", "budget", `{"budget":{"budget_ms":2000}}`, 10)}
	config.Public = append(config.Public, PublicRoute{Method: "POST", Path: "/api/status", Reason: "public"})
	public := requirementRule("public", "budget", `{"budget":{"budget_ms":300}}`, 1)
	public.MatchPath = "/api/status"
	context.Rules = append(context.Rules, public)
	inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "POST", Path: "/api/status"})
	plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ConsolidateBudgets: true}, inventory)
	if err != nil || plan.Status != "ready" || len(plan.Changes) != 1 || plan.Changes[0].BudgetGroup == "" || *plan.Changes[0].Create.Priority != 9 {
		t.Fatalf("safe exception: %+v %v", plan, err)
	}
	for _, affected := range plan.Changes[0].Impact.Captured {
		if affected.Public && (affected.BeforeRuleID != "public" || affected.AfterRuleID != "public") {
			t.Fatalf("exception changed: %+v", affected)
		}
	}
}

func TestGroupBudgetConsolidationDoesNotMergeThrottles(t *testing.T) {
	config, context, inventory := budgetGroupFixture(t)
	config.Groups[0].Require.Throttle = &ThrottleRequirement{KeyBy: "none", MaxRPS: floatPtr(1)}
	plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ThrottleBurst: 2, ConsolidateBudgets: true}, inventory)
	if err != nil || plan.Status != "ready" || len(plan.Changes) != 3 {
		t.Fatalf("mixed plan: %+v %v", plan, err)
	}
	for _, change := range plan.Changes {
		if change.Kind == "throttle" && (change.Create.MatchPath == "/api/*" || change.BudgetGroup != "") {
			t.Fatal("throttle buckets consolidated")
		}
	}
}

func TestGroupBudgetConsolidationMethodsAndExistingPrefixUpdate(t *testing.T) {
	for _, scenario := range []string{"separate methods", "existing prefix", "already shared family"} {
		t.Run(scenario, func(t *testing.T) {
			config, context, inventory := budgetGroupFixture(t)
			wantChanges := 1
			switch scenario {
			case "separate methods":
				config.Groups[0].Methods = []string{"POST", "GET"}
				inventory.Routes = append(inventory.Routes, CapturedRoute{Method: "GET", Path: "/api/orders"}, CapturedRoute{Method: "GET", Path: "/api/reports"})
				wantChanges = 2
			case "existing prefix":
				rule := requirementRule("group-prefix", "budget", `{"budget":{"budget_ms":2000}}`, 0)
				rule.MatchHost, rule.MatchPath = context.Host, "/api/*"
				context.Rules = []api.EdgeRuleResponse{rule}
			case "already shared family":
				inventory.Routes[1].Path, inventory.Routes[2].Path = "/api/orders/{id}", "/api/orders/{id}/items"
			}
			plan, err := BuildServerGroupPlan(config, context, PlanOptions{PlanName: "pro", ConsolidateBudgets: true}, inventory)
			if err != nil || plan.Status != "ready" || len(plan.Changes) != wantChanges {
				t.Fatalf("plan: %+v %v", plan, err)
			}
			if scenario == "existing prefix" && (plan.Changes[0].Operation != "update" || plan.Changes[0].RuleID != "group-prefix" || plan.RuleUsage.Before != plan.RuleUsage.After) {
				t.Fatal("existing selector was not reused")
			}
			if scenario == "already shared family" && (plan.Changes[0].BudgetGroup != "" || plan.Changes[0].Create.MatchPath != "/api/orders/*") {
				t.Fatal("unnecessarily broadened an already shared family")
			}
		})
	}
}

func TestGroupBudgetConsolidationRejectsConcretePlan(t *testing.T) {
	_, err := BuildServerPlan(Config{Version: 1, Routes: []Requirement{{Method: "GET", Path: "/", Require: Checks{Budget: &BudgetRequirement{Explicit: true}}}}}, requirementContext(), PlanOptions{PlanName: "pro", ConsolidateBudgets: true})
	if err == nil {
		t.Fatal("concrete plan accepted consolidation")
	}
}
