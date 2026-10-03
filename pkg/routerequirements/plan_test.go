package routerequirements

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildPlanExactUpdatesPreserveSelectorsAndInput(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id","max_rps":1,"missing_key_policy":"reject"},"budget":{"max_ms":1000}}`)
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{
		requirementRule("limit", "throttle", sharedThrottle, 0),
		requirementRule("budget", "budget", `{"budget":{"budget_ms":3000}}`, 0),
	}
	for i := range context.Rules {
		context.Rules[i].MatchHost, context.Rules[i].MatchPath = context.Host, "/api/checkout"
	}
	original, _ := json.Marshal(context)
	plan := BuildPlan(config, "digest", context, PlanOptions{PlanName: "hobby"})
	if plan.Status != "ready" || plan.Before.Status != "violated" || plan.After.Status != "satisfied" ||
		len(plan.Changes) != 2 || plan.After.PolicyScope != "proposed_app" || plan.Before.PolicyScope != "current_app" {
		t.Fatalf("plan = %+v", plan)
	}
	for _, change := range plan.Changes {
		if change.Operation != "update" || change.Update == nil || change.Create != nil || change.Update.Priority != nil ||
			change.Update.MatchHost != nil || change.Update.MatchMethods != nil || change.Update.MatchPath != nil {
			t.Fatalf("exact update changed selector: %+v", change)
		}
	}
	var action api.EdgeRuleThrottleAction
	if err := json.Unmarshal(*plan.Changes[0].Update.Action, &action); err != nil {
		t.Fatal(err)
	}
	if action.Burst != 4 || action.RequestsPerSecond != 1 || action.KeyBy != "consumer_id" ||
		action.MissingKeyPolicy != "reject" {
		t.Fatalf("throttle = %+v", action)
	}
	after, _ := json.Marshal(context)
	if string(original) != string(after) {
		t.Fatal("planning mutated original configuration")
	}
}

func TestBuildPlanBroadOverridesPreserveOtherRequestShapes(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id"},"budget":{"max_ms":1000}}`)
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{
		requirementRule("shared", "throttle", sharedThrottle, 10),
		requirementRule("budget", "budget", `{"budget":{"budget_ms":3000}}`, 20),
	}
	plan := BuildPlan(config, "digest", context, PlanOptions{PlanName: "pro"})
	if plan.Status != "ready" || len(plan.Changes) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	proposed := applyPlannedChanges(context, plan)
	for _, change := range plan.Changes {
		request := change.Create
		priority := map[string]int{"throttle": 9, "budget": 19}[change.Kind]
		if change.Operation != "create" || request == nil || request.MatchHost != context.Host || request.MatchPath != "/api/checkout" ||
			!reflect.DeepEqual(request.MatchMethods, []string{"POST"}) || len(request.MatchHeaders) != 0 || *request.Priority != priority ||
			len(change.DisplacedRuleIDs) != 1 || change.Impact.Method != "POST" || change.Impact.Path != "/api/checkout" {
			t.Fatalf("broader selectors were changed: %+v", change)
		}
	}
	for _, shape := range []struct{ host, path, method string }{
		{context.Host, "/api/other", "POST"}, {"other.gregale.dev", "/api/checkout", "POST"},
		{context.Host, "/api/checkout", "GET"},
	} {
		input := traceInput(context, Requirement{Method: shape.method, Path: shape.path})
		input.Host = shape.host
		for _, kind := range []string{"throttle", "budget"} {
			before, _, _ := selectRule(context.Rules, input, kind)
			after, _, _ := selectRule(proposed.Rules, input, kind)
			if (before == nil) != (after == nil) || (before != nil && (after.ID != before.ID || string(after.Action) != string(before.Action))) {
				t.Fatalf("unrelated request shape changed: %+v, kind=%s", shape, kind)
			}
		}
	}
}

func applyPlannedChanges(context Context, plan PolicyPlan) Context {
	context.Rules = append([]api.EdgeRuleResponse(nil), context.Rules...)
	for _, change := range plan.Changes {
		if request := change.Create; request != nil {
			context.Rules = append(context.Rules, api.EdgeRuleResponse{ID: change.SimulatedRuleID, AppID: context.App.ID,
				Kind: request.Kind, MatchHost: request.MatchHost, MatchPath: request.MatchPath, MatchMethods: request.MatchMethods,
				Priority: *request.Priority, Enabled: *request.Enabled, Action: wrapPolicyTestAction(request.Kind, request.Action)})
		} else {
			for i := range context.Rules {
				if context.Rules[i].ID == change.RuleID {
					context.Rules[i].Action = wrapPolicyTestAction(change.Kind, *change.Update.Action)
				}
			}
		}
	}
	return context
}

func TestBuildPlanMissingThrottleRequiresCustomerChoices(t *testing.T) {
	context := requirementContext()
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id","max_rps":2},"budget":{"max_ms":1000}}`)
	plan := BuildPlan(config, "digest", context, PlanOptions{PlanName: "hobby"})
	if plan.Status != "partial" || len(plan.Changes) != 1 || plan.Unresolved[0].Code != "throttle_burst_required" {
		t.Fatalf("invented a burst or missed independent budget: %+v", plan)
	}
	plan = BuildPlan(config, "digest", context, PlanOptions{PlanName: "hobby", ThrottleBurst: 5})
	if plan.Status != "ready" || plan.After.Status != "satisfied" || len(plan.Changes) != 2 {
		t.Fatalf("customer choices did not complete plan: %+v", plan)
	}
	config = requirementConfig(t, `{"throttle":{"key_by":"consumer_id"}}`)
	plan = BuildPlan(config, "digest", context, PlanOptions{PlanName: "hobby", ThrottleBurst: 5})
	if plan.Status != "blocked" || plan.Unresolved[0].Code != "throttle_rate_required" {
		t.Fatal("invented a missing rate")
	}
}

func TestBuildPlanExplicitBudgetPreservesBaseline(t *testing.T) {
	config := requirementConfig(t, `{"budget":{"explicit":true}}`)
	context := requirementContext()
	context.App.RequestTimeoutS = 0
	plan := BuildPlan(config, "digest", context, PlanOptions{PlanName: "free"})
	if plan.Status != "ready" || len(plan.Changes) != 1 ||
		!strings.Contains(string(plan.Changes[0].Create.Action), `"budget_ms":10000`) {
		t.Fatalf("explicit budget did not preserve baseline: %+v", plan)
	}
	context.App.EffectiveLimits.RequestBudgetMS = 0
	plan = BuildPlan(config, "digest", context, PlanOptions{PlanName: "free"})
	if len(plan.Changes) != 0 || plan.Unresolved[0].Code != "budget_limits_unavailable" {
		t.Fatal("missing effective budget became a guessed target")
	}
}

func TestBuildPlanBlockersAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, require, code string
		mutate              func(*Context, *PlanOptions)
	}{
		{"priority zero", `{"throttle":{"key_by":"consumer_id"}}`, "priority_space_exhausted", func(c *Context, _ *PlanOptions) { c.Rules[0].Priority = 0 }},
		{"unknown plan", `{"throttle":{"key_by":"consumer_id"}}`, "plan_limits_unavailable", func(_ *Context, o *PlanOptions) { o.PlanName = "future" }},
		{"failed account read", `{"throttle":{"key_by":"consumer_id"}}`, "plan_limits_unavailable", func(_ *Context, o *PlanOptions) { o.PlanUnavailable = "account:read_failed" }},
		{"rate floor", `{"throttle":{"key_by":"none","max_rps":0.5}}`, "rate_below_gateway_minimum", func(_ *Context, _ *PlanOptions) {}},
		{"header context", `{"throttle":{"key_by":"consumer_id"}}`, "header_dependent_rule", func(c *Context, _ *PlanOptions) {
			c.Rules[0].MatchHeaders = map[string]string{"Authorization": "secret-selector"}
		}},
		{"priority tie", `{"throttle":{"key_by":"consumer_id"}}`, "equal_priority_candidates", func(c *Context, _ *PlanOptions) {
			c.Rules = append(c.Rules, requirementRule("tie", "throttle", consumerThrottle, 10))
		}},
		{"unknown action field", `{"throttle":{"key_by":"consumer_id"}}`, "unrecognized_action_fields", func(c *Context, _ *PlanOptions) {
			c.Rules[0].Action = json.RawMessage(`{"throttle":{"requests_per_second":2,"burst":4,"future_token":"secret-action"}}`)
		}},
		{"custom claim", `{"throttle":{"key_by":"consumer_id"}}`, "custom_throttle_claim_needs_review", func(c *Context, _ *PlanOptions) {
			c.Rules[0].Action = json.RawMessage(`{"throttle":{"requests_per_second":2,"burst":4,"key_by":"jwt_claim","jwt_claim_name":"secret_claim"}}`)
		}},
		{"custom budget header", `{"budget":{"max_ms":1000}}`, "custom_budget_override_needs_review", func(c *Context, _ *PlanOptions) {
			c.Rules = []api.EdgeRuleResponse{requirementRule("budget", "budget", `{"budget":{"budget_ms":3000,"allow_override_header":"x-secret-override"}}`, 10)}
		}},
		{"routing mutation", `{"throttle":{"key_by":"consumer_id"}}`, "request_context_mutation", func(c *Context, _ *PlanOptions) {
			c.Rules = append(c.Rules, requirementRule("routing", "route", `{"route":{"app_slug":"secret-target"}}`, 1))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			context := requirementContext()
			context.Rules = []api.EdgeRuleResponse{requirementRule("shared", "throttle", sharedThrottle, 10)}
			options := PlanOptions{PlanName: "pro"}
			tc.mutate(&context, &options)
			plan := BuildPlan(requirementConfig(t, tc.require), "digest", context, options)
			if plan.Status != "blocked" || len(plan.Changes) != 0 || len(plan.Unresolved) != 1 || plan.Unresolved[0].Code != tc.code {
				t.Fatalf("plan = %+v", plan)
			}
			body, _ := json.Marshal(plan)
			for _, secret := range []string{"secret-selector", "secret-action", "secret_claim", "x-secret-override", "secret-target"} {
				if strings.Contains(string(body), secret) {
					t.Fatalf("plan leaked %s", secret)
				}
			}
		})
	}
}

func TestBuildPlanChecksQuotasAndNamedPlanLimits(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id","max_rps":2}}`)
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("shared", "throttle", sharedThrottle, 10)}
	plan := BuildPlan(config, "digest", context, PlanOptions{PlanName: "free"})
	if plan.Status != "blocked" || plan.Unresolved[0].Code != "throttle_rule_quota_reached" {
		t.Fatalf("ignored free-plan per-kind quota: %+v", plan)
	}
	context.Rules[0].MatchHost, context.Rules[0].MatchPath = context.Host, "/api/checkout"
	plan = BuildPlan(config, "digest", context, PlanOptions{PlanName: "free"})
	if plan.Status != "ready" || plan.Changes[0].Operation != "update" {
		t.Fatal("exact update unnecessarily consumed quota")
	}
	context.Rules = nil
	config = requirementConfig(t, `{"throttle":{"key_by":"none","max_rps":100000}}`)
	plan = BuildPlan(config, "digest", context, PlanOptions{PlanName: "free", ThrottleBurst: 2})
	if plan.Unresolved[0].Code != "throttle_plan_limits" || len(plan.Changes) != 0 {
		t.Fatal("used platform maxima instead of account plan")
	}
	limits, _ := api.LimitsFor(api.PlanFree)
	for i := 0; i < limits.EdgeRulesPerApp; i++ {
		rule := requirementRule("other"+string(rune('a'+i)), "headers", `{"headers":{}}`, 10)
		rule.MatchPath = "/unrelated"
		context.Rules = append(context.Rules, rule)
	}
	config = requirementConfig(t, `{"budget":{"max_ms":1000}}`)
	plan = BuildPlan(config, "digest", context, PlanOptions{PlanName: "free"})
	if plan.Unresolved[0].Code != "app_rule_quota_reached" {
		t.Fatal("ignored total rule quota")
	}
}

func TestBuildPlanAuthenticationRemainsManual(t *testing.T) {
	context := requirementContext()
	context.App.ConsumerAuthMode = "optional"
	plan := BuildPlan(requirementConfig(t, `{"authentication":"consumer","budget":{"max_ms":1000}}`), "digest", context, PlanOptions{PlanName: "hobby"})
	if plan.Status != "partial" || len(plan.Changes) != 1 || plan.Unresolved[0].Code != "authentication_requires_manual_review" {
		t.Fatal("route budget was treated as an authentication fix")
	}
}

func TestBuildPlanDeterministicFingerprintBindsConfiguration(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id"}}`)
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("shared", "throttle", sharedThrottle, 10),
		requirementRule("unrelated", "jwt", `{"jwt":{"issuer":"secret-issuer"}}`, 99)}
	context.Rules[1].Enabled = false
	options := PlanOptions{PlanName: "pro"}
	before := BuildPlan(config, "digest", context, options)
	context.Rules[0], context.Rules[1] = context.Rules[1], context.Rules[0]
	context.Rules[0].UpdatedAt = time.Now()
	context.Rules[1].Action = json.RawMessage(` { "throttle": { "burst":4, "requests_per_second":2 } } `)
	after := BuildPlan(config, "digest", context, options)
	if before.SHA256 != after.SHA256 || before.ConfigurationSHA256 != after.ConfigurationSHA256 || len(before.SHA256) != 64 {
		t.Fatal("order, JSON formatting, or non-policy timestamps changed identity")
	}
	context.Rules[0].MatchHeaders = map[string]string{"X-Private": "secret-value"}
	changed := BuildPlan(config, "digest", context, options)
	if changed.ConfigurationSHA256 == before.ConfigurationSHA256 || changed.SHA256 == before.SHA256 {
		t.Fatal("plan identity did not bind selector changes")
	}
	body, _ := json.Marshal(changed)
	if strings.Contains(string(body), "secret-value") || strings.Contains(string(body), "secret-issuer") {
		t.Fatal("fingerprint exported raw configuration")
	}
}

func TestBuildPlanSatisfiedRequirementsNeedNoChanges(t *testing.T) {
	plan := BuildPlan(requirementConfig(t, `{"budget":{"max_ms":2000}}`), "digest", requirementContext(), PlanOptions{})
	if plan.Status != "no_changes" || len(plan.Changes) != 0 || len(plan.Unresolved) != 0 {
		t.Fatalf("already satisfied requirement was changed: %+v", plan)
	}
}

func wrapPolicyTestAction(kind string, body json.RawMessage) json.RawMessage {
	wrapped, _ := json.Marshal(map[string]json.RawMessage{kind: body})
	return wrapped
}
