package routerequirements

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func requirementConfig(t *testing.T, require string) Config {
	t.Helper()
	config, err := Parse([]byte(`{"version":1,"routes":[{"method":"POST","path":"/api/checkout","require":` + require + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func requirementContext() Context {
	return Context{Host: "pr-42-api.gregale.dev", App: api.AppResponse{
		ID: "app-id", Slug: "pr-42-api", ConsumerAuthMode: "required", RequestTimeoutS: 2,
		EffectiveLimits: api.AppEffectiveLimits{RequestBudgetMS: 10000, RequestBudgetMaxMS: 20000},
	}}
}

func requirementRule(id, kind, body string, priority int) api.EdgeRuleResponse {
	return api.EdgeRuleResponse{ID: id, AppID: "app-id", Enabled: true, MatchHost: "*.gregale.dev", MatchPath: "/api/*", MatchMethods: []string{"POST"}, Kind: kind, Priority: priority, Action: json.RawMessage(body)}
}

const consumerThrottle = `{"throttle":{"requests_per_second":2,"burst":4,"key_by":"consumer_id","missing_key_policy":"reject"}}`
const sharedThrottle = `{"throttle":{"requests_per_second":2,"burst":4}}`

// ADR-436: report configured scope, precedence, and uncertainty without claiming runtime admission.
func TestEvaluateRequirementsUsesSelectedPolicyAndAppBudget(t *testing.T) {
	config := requirementConfig(t, `{"authentication":"consumer","throttle":{"key_by":"consumer_id","max_rps":2,"missing_key_policy":"reject"},"budget":{"max_ms":2000,"explicit":true}}`)
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("consumer-limit", "throttle", consumerThrottle, 10)}
	report := Evaluate(config, "digest", context)
	if report.Status != "satisfied" || len(report.Routes[0].Checks) != 3 || !strings.Contains(report.Routes[0].Checks[2].Actual, "baseline_budget_ms=2000; source=app") {
		t.Fatalf("report = %+v", report)
	}
	context.Rules = append(context.Rules, requirementRule("shared-limit", "throttle", sharedThrottle, 1))
	report = Evaluate(config, "digest", context)
	finding := report.Routes[0].Checks[1]
	if report.Status != "violated" || finding.Code != "throttle_mismatch" || finding.RuleIDs[0] != "shared-limit" || !strings.Contains(finding.Actual, "key_by=none") {
		t.Fatalf("lower-priority consumer rule incorrectly hid shared limit: %+v", report)
	}
}

func TestEvaluateThrottleSelectorsAndUncertainty(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id"}}`)
	for _, tc := range []struct {
		name, status, code string
		mutate             func(*Context)
	}{
		{"disabled", "violated", "missing_throttle_rule", func(c *Context) { c.Rules[0].Enabled = false }},
		{"different host", "violated", "missing_throttle_rule", func(c *Context) { c.Rules[0].MatchHost = "production.example.com" }},
		{"different method", "violated", "missing_throttle_rule", func(c *Context) { c.Rules[0].MatchMethods = []string{"GET"} }},
		{"different path", "violated", "missing_throttle_rule", func(c *Context) { c.Rules[0].MatchPath = "/admin/*" }},
		{"headers", "unknown", "header_dependent_rule", func(c *Context) { c.Rules[0].MatchHeaders = map[string]string{"Authorization": "Bearer secret-token"} }},
		{"priority tie", "unknown", "equal_priority_candidates", func(c *Context) { c.Rules = append(c.Rules, requirementRule("shared", "throttle", sharedThrottle, 10)) }},
		{"invalid glob", "unknown", "invalid_path_selector", func(c *Context) { c.Rules[0].MatchPath = "[" }},
		{"invalid action", "unknown", "invalid_rule_action", func(c *Context) { c.Rules[0].Action = json.RawMessage(`{"throttle":null}`) }},
		{"invalid rate", "unknown", "invalid_rule_action", func(c *Context) {
			c.Rules[0].Action = json.RawMessage(`{"throttle":{"requests_per_second":0,"burst":4,"key_by":"consumer_id"}}`)
		}},
		{"rewrite", "unknown", "request_context_mutation", func(c *Context) {
			c.Rules = append(c.Rules, requirementRule("rewrite", "rewrite", `{"rewrite":{"path":"/other"}}`, 1))
		}},
		{"route", "unknown", "request_context_mutation", func(c *Context) {
			c.Rules = append(c.Rules, requirementRule("route", "route", `{"route":{"app_slug":"other"}}`, 1))
		}},
		{"request headers", "unknown", "request_context_mutation", func(c *Context) {
			c.Rules = append(c.Rules, requirementRule("headers", "headers", `{"headers":{"request_headers":[{"op":"set","name":"X-Mode","value":"secret-header"}]}}`, 1))
		}},
		{"failed reads", "unknown", "rules:read_failed", func(c *Context) { c.Unavailable = "rules:read_failed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			context := requirementContext()
			context.Rules = []api.EdgeRuleResponse{requirementRule("consumer", "throttle", consumerThrottle, 10)}
			tc.mutate(&context)
			report := Evaluate(config, "digest", context)
			finding := report.Routes[0].Checks[0]
			if report.Status != tc.status || finding.Code != tc.code {
				t.Fatalf("finding = %+v", finding)
			}
			body, _ := json.Marshal(report)
			if strings.Contains(string(body), "secret-token") || strings.Contains(string(body), "secret-header") {
				t.Fatal("report leaked selector or action values")
			}
		})
	}
}

func TestEvaluateIgnoresLowerPriorityConditionalRule(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"consumer_id"}}`)
	context := requirementContext()
	conditional := requirementRule("conditional", "throttle", sharedThrottle, 20)
	conditional.MatchHeaders = map[string]string{"X-Mode": "secret"}
	context.Rules = []api.EdgeRuleResponse{conditional, requirementRule("consumer", "throttle", consumerThrottle, 10)}
	if report := Evaluate(config, "digest", context); report.Status != "satisfied" {
		t.Fatalf("lower-priority rule displaced unconditional selection: %+v", report)
	}
	context.Rules[0].Priority = 1
	if report := Evaluate(config, "digest", context); report.Status != "unknown" {
		t.Fatal("higher-priority conditional rule needs request context")
	}
}

func TestEvaluateThrottleUsesGatewayRateFloor(t *testing.T) {
	config := requirementConfig(t, `{"throttle":{"key_by":"none","max_rps":0.5}}`)
	context := requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("limit", "throttle", `{"throttle":{"requests_per_second":0.2,"burst":1}}`, 10)}
	report := Evaluate(config, "digest", context)
	if report.Status != "violated" || !strings.Contains(report.Routes[0].Checks[0].Actual, "gateway_rps=1") {
		t.Fatalf("configured fraction bypassed gateway floor: %+v", report)
	}
}

func TestEvaluateAuthenticationKeepsMechanismsDistinct(t *testing.T) {
	consumer := requirementConfig(t, `{"authentication":"consumer"}`)
	context := requirementContext()
	context.App.ConsumerAuthMode, context.App.RequireAuthn = "optional", true
	if report := Evaluate(consumer, "digest", context); report.Status != "violated" {
		t.Fatal("account-token auth became consumer identity enforcement")
	}
	context.App.ConsumerAuthMode = ""
	if report := Evaluate(consumer, "digest", context); report.Status != "unknown" {
		t.Fatal("missing consumer metadata became a definitive finding")
	}
	if report := Evaluate(requirementConfig(t, `{"authentication":"application"}`), "digest", requirementContext()); report.Status != "unknown" {
		t.Fatal("configuration established business authorization")
	}
	jwt := requirementConfig(t, `{"authentication":"jwt"}`)
	context = requirementContext()
	context.Rules = []api.EdgeRuleResponse{requirementRule("jwt", "jwt", `{"jwt":{"issuer":"secret-issuer","jwks_url":"https://keys.example.com/secret-jwks","algorithms":["RS256"],"audience":["secret-audience"],"required_claims":{"role":"secret-role"}}}`, 10)}
	report := Evaluate(jwt, "digest", context)
	body, _ := json.Marshal(report)
	if report.Status != "satisfied" || strings.Contains(string(body), "secret-") {
		t.Fatalf("JWT not verified as configuration or leaked claims: %s", body)
	}
	context.Rules[0].Action = json.RawMessage(`{"jwt":{"issuer":"secret"}}`)
	if report := Evaluate(jwt, "digest", context); report.Status != "unknown" {
		t.Fatal("invalid JWT configuration became satisfied")
	}
}

func TestEvaluateBudgetDefaultsCeilingsAndMissingMetadata(t *testing.T) {
	config := requirementConfig(t, `{"budget":{"explicit":true,"max_ms":2500}}`)
	context := requirementContext()
	context.App.RequestTimeoutS = 0
	if report := Evaluate(config, "digest", context); report.Routes[0].Checks[0].Code != "missing_explicit_budget" {
		t.Fatal("plan default satisfied explicit budget requirement")
	}
	context.Rules = []api.EdgeRuleResponse{requirementRule("budget", "budget", `{"budget":{"budget_ms":10000,"allow_override_header":"x-secret-override"}}`, 10)}
	context.App.EffectiveLimits.RequestBudgetMaxMS = 2500
	report := Evaluate(config, "digest", context)
	if report.Status != "satisfied" || !strings.Contains(report.Routes[0].Checks[0].Actual, "baseline_budget_ms=2500; source=ceiling_clamp") {
		t.Fatalf("did not reuse effective plan clamp: %+v", report)
	}
	body, _ := json.Marshal(report)
	if strings.Contains(string(body), "x-secret-override") {
		t.Fatal("report leaked rule action metadata")
	}
	context.App.EffectiveLimits.RequestBudgetMaxMS = 20000
	if report := Evaluate(config, "digest", context); report.Status != "violated" {
		t.Fatal("excessive configured budget satisfied max_ms")
	}
	context.App.EffectiveLimits.RequestBudgetMS = 0
	if report := Evaluate(config, "digest", context); report.Status != "unknown" {
		t.Fatal("unavailable budget envelope became satisfied")
	}
}

func TestEvaluateBudgetMaximumAllowsDefaultsAndExplicitPolicies(t *testing.T) {
	config := requirementConfig(t, `{"budget":{"max_ms":10000}}`)
	for _, explicit := range []bool{false, true} {
		context := requirementContext()
		if !explicit {
			context.App.RequestTimeoutS = 0
		}
		report := Evaluate(config, "digest", context)
		finding := report.Routes[0].Checks[0]
		if report.Status != "satisfied" || finding.Expected != "max_baseline_budget_ms=10000" {
			t.Fatalf("max-only budget requirement changed with explicit=%t: %+v", explicit, finding)
		}
	}
}
