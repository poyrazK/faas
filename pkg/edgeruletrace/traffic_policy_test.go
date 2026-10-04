// adr: 531
package edgeruletrace_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

func trafficTraceRule(t *testing.T, id, kind, path string, priority int, action any) api.EdgeRuleResponse {
	t.Helper()
	raw, err := json.Marshal(map[string]any{kind: action})
	if err != nil {
		t.Fatal(err)
	}
	return api.EdgeRuleResponse{ID: id, Kind: kind, MatchHost: "example.com", MatchPath: path,
		Priority: priority, Enabled: true, Action: raw}
}

func TestTrafficPreviewPinsIngressBudgetAcrossRewrite(t *testing.T) {
	input := budgetTraceInput()
	input.Path = "/public"
	original := trafficTraceRule(t, "original", "budget", "/public", 20, api.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: 4000})
	rewritten := trafficTraceRule(t, "rewritten", "budget", "/backend", 10, api.EdgeRuleBudgetAction{BudgetMs: 3000, TotalDeadlineMs: 2000})
	rewrite := trafficTraceRule(t, "rewrite", "rewrite", "*", 1, api.EdgeRuleRewriteAction{From: "/public", To: "/backend"})
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{original, rewritten, rewrite})
	if err != nil {
		t.Fatal(err)
	}
	deadline := result.Simulation.TotalDeadlinePolicy
	if deadline == nil || deadline.RuleID != "original" || deadline.SelectionPath != "/public" || deadline.DeadlineMS != 4000 || deadline.Status != "configured_candidate" || deadline.EnforcementStatus != "unverified" {
		t.Fatalf("ingress policy = %+v", deadline)
	}
	last := result.Simulation.Steps[len(result.Simulation.Steps)-1]
	if result.Simulation.FinalPath != "/backend" || last.RuleID != "original" || last.BudgetPolicy == nil || last.BudgetPolicy.BudgetMS != 1200 || last.BudgetPolicy.TotalDeadlineMS != 4000 {
		t.Fatalf("rewritten execution policy = %+v", last)
	}
}

func TestTrafficPreviewDoesNotStartTotalDeadlineAfterRewrite(t *testing.T) {
	input := budgetTraceInput()
	input.Path = "/public"
	for _, originalExecutionOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_ingress_rule", true: "execution_only_ingress_rule"}[originalExecutionOnly], func(t *testing.T) {
			rules := []api.EdgeRuleResponse{
				trafficTraceRule(t, "rewrite", "rewrite", "*", 1, api.EdgeRuleRewriteAction{From: "/public", To: "/backend"}),
				trafficTraceRule(t, "rewritten", "budget", "/backend", 10, api.EdgeRuleBudgetAction{BudgetMs: 3000, TotalDeadlineMs: 2000}),
			}
			if originalExecutionOnly {
				rules = append(rules, trafficTraceRule(t, "original", "budget", "/public", 20, api.EdgeRuleBudgetAction{BudgetMs: 1200}))
			}
			result, err := edgeruletrace.Simulate(input, rules)
			if err != nil {
				t.Fatal(err)
			}
			last := result.Simulation.Steps[len(result.Simulation.Steps)-1]
			if result.Simulation.TotalDeadlinePolicy != nil || last.RuleID != "rewritten" || last.BudgetPolicy.BudgetMS != 3000 || last.BudgetPolicy.TotalDeadlineMS != 0 {
				t.Fatalf("late rule manufactured an ingress timer: %+v", result.Simulation)
			}
		})
	}
}

func TestTrafficPreviewHeaderActionsCannotChangeSelectors(t *testing.T) {
	for _, operation := range []string{"set", "remove"} {
		t.Run(operation, func(t *testing.T) {
			input := budgetTraceInput()
			input.Headers = make(http.Header)
			if operation == "remove" {
				input.Headers.Add("X-Selector", "original")
			}
			budget := trafficTraceRule(t, "selected", "budget", "*", 10, api.EdgeRuleBudgetAction{BudgetMs: 1200})
			budget.MatchHeaders = map[string]string{"X-Selector": "original"}
			headers := trafficTraceRule(t, "headers", "headers", "*", 1, api.EdgeRuleHeadersAction{RequestHeaders: []api.EdgeRuleHeaderOp{
				{Name: "X-Selector", Action: operation, Value: "original"},
				{Name: api.RequestBudgetDefaultOverrideHeader, Action: "set", Value: "2400"},
			}})
			result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{headers, budget})
			if err != nil {
				t.Fatal(err)
			}
			last := result.Simulation.Steps[len(result.Simulation.Steps)-1]
			wantID, wantBudget := "", int64(3000)
			if operation == "remove" {
				wantID, wantBudget = "selected", 2400
			}
			if last.RuleID != wantID || last.BudgetPolicy == nil || last.BudgetPolicy.BudgetMS != wantBudget {
				t.Fatalf("original selector or live override mismatch: %+v", last)
			}
		})
	}
}

func TestTrafficPreviewRetainsIngressPolicyBeforeLaterStop(t *testing.T) {
	for _, kind := range []string{"respond", "cache", "throttle"} {
		t.Run(kind, func(t *testing.T) {
			input := budgetTraceInput()
			input.Method = http.MethodGet
			var action any
			switch kind {
			case "respond":
				action = api.EdgeRuleRespondAction{StatusCode: 200, Body: json.RawMessage(`{"ok":true}`)}
			case "cache":
				action = api.EdgeRuleCacheAction{MaxAgeSeconds: 10}
			case "throttle":
				action = api.EdgeRuleThrottleAction{RequestsPerSecond: 2, Burst: 2}
			}
			result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{
				trafficTraceRule(t, "ingress", "budget", "*", 10, api.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: 9000}),
				trafficTraceRule(t, "stop", kind, "*", 1, action),
			})
			if err != nil {
				t.Fatal(err)
			}
			policy := result.Simulation.TotalDeadlinePolicy
			if policy == nil || policy.ConfiguredMS != 9000 || policy.DeadlineMS != 6000 || policy.EnforcementStatus != "unverified" || result.Simulation.StoppedAt != kind {
				t.Fatalf("early stop lost deadline candidate: %+v", result.Simulation)
			}
			for _, step := range result.Simulation.Steps {
				if step.Phase == "budget" {
					t.Fatal("execution budget was reached after a stopping gate")
				}
			}
		})
	}
}

func TestTrafficPreviewRejectsAmbiguousIngressPin(t *testing.T) {
	result, err := edgeruletrace.Simulate(budgetTraceInput(), []api.EdgeRuleResponse{
		trafficTraceRule(t, "execution-only", "budget", "*", 1, api.EdgeRuleBudgetAction{BudgetMs: 1200}),
		trafficTraceRule(t, "total", "budget", "*", 1, api.EdgeRuleBudgetAction{BudgetMs: 2400, TotalDeadlineMs: 4000}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Simulation.Status != "incomplete" || result.Simulation.StoppedAt != "total_deadline" || result.Simulation.Outcome != "ambiguous" || result.Simulation.TotalDeadlinePolicy != nil {
		t.Fatalf("ambiguous ingress rule claimed a deadline: %+v", result.Simulation)
	}
}

func TestTrafficPreviewDistinguishesDroppedRetryAndBudgetCompileFailure(t *testing.T) {
	for _, kind := range []string{"budget", "retry"} {
		t.Run(kind, func(t *testing.T) {
			var invalid, valid any
			if kind == "budget" {
				invalid = api.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: -1}
				valid = api.EdgeRuleBudgetAction{BudgetMs: 2400, TotalDeadlineMs: 4000}
			} else {
				invalid = api.EdgeRuleRetryAction{MaxAttempts: 1}
				valid = api.EdgeRuleRetryAction{MaxAttempts: 2}
			}
			input := budgetTraceInput()
			if kind == "retry" {
				input.AppRequestBudgetLoaded = false
			}
			result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{
				trafficTraceRule(t, "invalid", kind, "*", 1, invalid),
				trafficTraceRule(t, "valid", kind, "*", 2, valid),
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Rules[0].Outcome != "unavailable" || result.Rules[1].Status != "first_candidate" {
				t.Fatalf("uncompilable rule shadowed the valid policy: %+v", result)
			}
			if kind == "budget" {
				if result.Simulation.Status != "incomplete" || result.Simulation.ProblemCode != api.CodeTrafficPolicyUnavailable || result.Simulation.StatusCode != http.StatusServiceUnavailable || result.Simulation.StoppedAt != "policy" {
					t.Fatalf("invalid total deadline bypassed owner verification: %+v", result.Simulation)
				}
			} else if result.Simulation.Steps[0].RuleID != "valid" {
				t.Fatalf("dropped retry intercepted selection: %+v", result.Simulation)
			}
		})
	}
}

func TestTrafficPreviewCompileRefusalPrecedesRequestSelectors(t *testing.T) {
	for _, path := range []string{"*", "/another-path"} {
		t.Run(path, func(t *testing.T) {
			input := budgetTraceInput()
			rule := trafficTraceRule(t, "invalid", "budget", path, 1, api.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: -1})
			rule.MatchMethods = []string{http.MethodGet}
			rule.MatchHeaders = map[string]string{"X-Absent": "required"}
			result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{rule})
			if err != nil {
				t.Fatal(err)
			}
			if result.Simulation.ProblemCode != api.CodeTrafficPolicyUnavailable || result.Simulation.StatusCode != http.StatusServiceUnavailable || result.Simulation.StoppedAt != "policy" {
				t.Fatalf("request selector hid a host compilation failure: %+v", result.Simulation)
			}
		})
	}
}

func TestTrafficPreviewDoesNotGuessOtherOwnerOrMissingCeiling(t *testing.T) {
	input := budgetTraceInput()
	budget := trafficTraceRule(t, "budget", "budget", "*", 10, api.EdgeRuleBudgetAction{BudgetMs: 1200, TotalDeadlineMs: 4000})
	route := trafficTraceRule(t, "route", "route", "*", 1, api.EdgeRuleRouteAction{TargetAppSlug: "other-app"})
	result, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{budget, route})
	if err != nil {
		t.Fatal(err)
	}
	if result.Simulation.Outcome != "route" || result.Simulation.Status != "incomplete" || result.Simulation.TotalDeadlinePolicy != nil {
		t.Fatalf("named app plan was applied to unresolved target: %+v", result.Simulation)
	}
	input.AppRequestBudgetLoaded = false
	result, err = edgeruletrace.Simulate(input, []api.EdgeRuleResponse{budget})
	if err != nil {
		t.Fatal(err)
	}
	policy := result.Simulation.TotalDeadlinePolicy
	if result.Simulation.Outcome != "needs_app_request_budget" || policy == nil || policy.DeadlineMS != 0 || policy.Status != "needs_app_request_budget" || policy.EnforcementStatus != "unverified" {
		t.Fatalf("missing ceiling was inferred: %+v", result.Simulation)
	}
}
