package routerequirements

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/edgeruletrace"
)

const Scope = "Current app configuration for concrete method/path requests on the app's platform hostname. Results do not establish application authorization, runtime admission, endpoint existence, every value of a route template, or historical deployment policy. Budget comparisons use the configured baseline without request-header overrides; runtime overrides and end-to-end response time are outside this check. Dimensional throttles retain the platform's bounded overflow behavior."

type Report = api.RouteRequirementsReport

type RouteResult = api.RouteRequirementsResult

type Finding = api.RouteRequirementsFinding

type Context struct {
	App         api.AppResponse
	Host        string
	Unavailable string // stable reason code when an authenticated read failed
	Rules       []api.EdgeRuleResponse
}

func Evaluate(config Config, digest string, context Context) Report {
	report := Report{Version: 1, SHA256: digest, Host: context.Host, PolicyScope: "current_app", Status: "satisfied", Scope: Scope, Routes: []RouteResult{}}
	for _, requirement := range config.Routes {
		row := RouteResult{Name: requirement.Name, Method: requirement.Method, Path: requirement.Path, Status: "satisfied", Checks: []Finding{}}
		input := traceInput(context, requirement)
		if requirement.Require.Authentication != "" {
			row.Checks = append(row.Checks, checkAuthentication(requirement.Require.Authentication, context, input))
		}
		if requirement.Require.Throttle != nil {
			row.Checks = append(row.Checks, checkThrottle(*requirement.Require.Throttle, context, input))
		}
		if requirement.Require.Budget != nil {
			row.Checks = append(row.Checks, checkBudget(*requirement.Require.Budget, context, input))
		}
		for i := range row.Checks {
			finding := &row.Checks[i]
			finding.Expected = expectedValue(requirement.Require, finding.Requirement)
			row.Status = combineStatus(row.Status, finding.Status)
		}
		report.Status = combineStatus(report.Status, row.Status)
		report.Routes = append(report.Routes, row)
	}
	return report
}

func traceInput(context Context, requirement Requirement) edgeruletrace.Input {
	app := context.App
	return edgeruletrace.Input{App: app.Slug, Host: context.Host, Method: requirement.Method, Path: requirement.Path,
		AppMaintenanceLoaded: true, AppMaintenanceMode: app.MaintenanceMode,
		AppRequestBudgetLoaded: app.EffectiveLimits.RequestBudgetMS > 0 && app.EffectiveLimits.RequestBudgetMaxMS > 0,
		RequestBudgetMS:        app.EffectiveLimits.RequestBudgetMS, RequestBudgetMaxMS: app.EffectiveLimits.RequestBudgetMaxMS,
		RequestTimeoutS:          app.RequestTimeoutS,
		AppThrottleContextLoaded: true, AppRequestRateRPS: app.EffectiveLimits.AppRequestRateRPS,
		AppRequestRateBurst: app.EffectiveLimits.AppRequestBurst, AccountRequestRateRPM: app.EffectiveLimits.AccountRequestRateRPM}
}

func checkAuthentication(want string, context Context, input edgeruletrace.Input) Finding {
	if context.Unavailable != "" {
		return unknown("authentication", context.Unavailable)
	}
	if want == "application" {
		finding := unknown("authentication", "application_auth_not_established")
		finding.NextAction = "Verify application-owned authentication and authorization with customer-defined assertions."
		return finding
	}
	if code := contextChange(context.Rules, input, want != "consumer"); code != "" {
		return unknown("authentication", code)
	}
	if want == "consumer" {
		switch context.App.ConsumerAuthMode {
		case "required":
			return Finding{Requirement: "authentication", Status: "satisfied", Code: "consumer_auth_required", Actual: "consumer_auth_mode=required", Reason: "The current app configuration requires a platform consumer credential; business authorization is not established."}
		case "optional":
			return Finding{Requirement: "authentication", Status: "violated", Code: "consumer_auth_optional", Actual: "consumer_auth_mode=optional", Reason: "Platform consumer credentials are optional in the current app configuration.", NextAction: "Review setting consumer_auth_mode to required for this app; this affects every app route."}
		default:
			return unknown("authentication", "consumer_auth_mode_unavailable")
		}
	}
	rule, code, ids := selectRule(context.Rules, input, "jwt")
	if code != "" {
		return selectionFinding("authentication", code, ids)
	}
	if rule == nil {
		return Finding{Requirement: "authentication", Status: "violated", Code: "missing_jwt_rule", Reason: "No enabled JWT rule matches this host, method, and path.", NextAction: "Review a matching JWT rule or declare the application's actual authentication mechanism."}
	}
	var action api.EdgeRuleJWTAction
	if !decodeAction(*rule, "jwt", &action) || action.Validate() != nil {
		return selectionFinding("authentication", "invalid_rule_action", ids)
	}
	return Finding{Requirement: "authentication", Status: "satisfied", Code: "jwt_rule_configured", Actual: "jwt_verification_configured", RuleIDs: ids, Reason: "A valid JWT verification configuration matches this request shape; live signature verification and application authorization are not established."}
}

func checkThrottle(want ThrottleRequirement, context Context, input edgeruletrace.Input) Finding {
	if context.Unavailable != "" {
		return unknown("throttle", context.Unavailable)
	}
	if code := contextChange(context.Rules, input, true); code != "" {
		return unknown("throttle", code)
	}
	rule, code, ids := selectRule(context.Rules, input, "throttle")
	if code != "" {
		return selectionFinding("throttle", code, ids)
	}
	if rule == nil {
		return Finding{Requirement: "throttle", Status: "violated", Code: "missing_throttle_rule", Reason: "No enabled route throttle matches this host, method, and path; outer app/account limits do not satisfy a route throttle requirement.", NextAction: "Review adding a matching throttle rule with the requested key dimension."}
	}
	var action api.EdgeRuleThrottleAction
	limits, _ := api.LimitsFor(api.PlanScale)
	// Structural validation uses platform maxima, not a guessed account plan.
	if !decodeAction(*rule, "throttle", &action) || action.Validate(api.ThrottleValidationContext{PlanMaxRPS: float64(limits.RateLimitRPS), PlanMaxBurst: limits.RateLimitBurst, PlanMaxKeysPerRule: limits.ThrottleMaxKeysPerRule}) != nil {
		return selectionFinding("throttle", "invalid_rule_action", ids)
	}
	trace, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{*rule})
	if err != nil || len(trace.Rules) != 1 || trace.Rules[0].ActionPreview == nil || trace.Rules[0].ActionPreview.ThrottlePolicy == nil {
		return selectionFinding("throttle", "policy_unavailable", ids)
	}
	policy := trace.Rules[0].ActionPreview.ThrottlePolicy
	actual := fmt.Sprintf("key_by=%s; gateway_rps=%g; missing_key_policy=%s", policy.KeyBy, policy.GatewayRateRPS, policy.MissingKeyPolicy)
	finding := Finding{Requirement: "throttle", Status: "satisfied", Code: "throttle_matches", Actual: actual, RuleIDs: ids, Reason: "The selected throttle configuration satisfies the declared dimension and bounds; live bucket admission is not established."}
	if policy.KeyBy != want.KeyBy || (want.MaxRPS != nil && policy.GatewayRateRPS > *want.MaxRPS) || (want.MissingKeyPolicy != "" && policy.MissingKeyPolicy != want.MissingKeyPolicy) {
		finding.Status, finding.Code = "violated", "throttle_mismatch"
		finding.Reason = "The highest-precedence throttle has a different key dimension, excessive configured rate, or different missing-key policy."
		finding.NextAction = "Review the selected rule's key_by, rate, missing-key policy, and priority; a lower-priority matching rule does not override it."
	}
	return finding
}

func checkBudget(want BudgetRequirement, context Context, input edgeruletrace.Input) Finding {
	if context.Unavailable != "" {
		return unknown("budget", context.Unavailable)
	}
	if code := contextChange(context.Rules, input, true); code != "" {
		return unknown("budget", code)
	}
	rule, code, ids := selectRule(context.Rules, input, "budget")
	if code != "" {
		return selectionFinding("budget", code, ids)
	}
	if rule != nil {
		var action api.EdgeRuleBudgetAction
		if !decodeAction(*rule, "budget", &action) || action.Validate() != nil {
			return selectionFinding("budget", "invalid_rule_action", ids)
		}
	}
	if want.Explicit && rule == nil && context.App.RequestTimeoutS <= 0 {
		return Finding{Requirement: "budget", Status: "violated", Code: "missing_explicit_budget", Reason: "There is no matching budget rule or explicit app request_timeout_s; the plan baseline alone does not satisfy explicit: true.", NextAction: "Review a route budget rule or an explicit app execution timeout."}
	}
	policy := edgeruletrace.ConfiguredBudget(input, rule)
	if policy.Source == "unavailable" {
		return selectionFinding("budget", "budget_limits_unavailable", ids)
	}
	finding := Finding{Requirement: "budget", Status: "satisfied", Code: "budget_matches", Actual: fmt.Sprintf("baseline_budget_ms=%d; source=%s", policy.BudgetMS, policy.Source), RuleIDs: ids, Reason: "The configured guest-execution budget baseline satisfies the requirement. Request-header overrides and end-to-end latency are outside this check."}
	if want.MaxMS != nil && policy.BudgetMS > *want.MaxMS {
		finding.Status, finding.Code = "violated", "budget_exceeds_requirement"
		finding.Reason = "The resolved configured budget baseline exceeds max_ms."
		finding.NextAction = "Review a narrower budget rule or app execution timeout; the report does not change it."
	}
	return finding
}

func unknown(requirement, code string) Finding {
	reason := "The available configuration and request context cannot establish this requirement."
	switch code {
	case "header_dependent_rule":
		reason = "A highest-precedence candidate depends on request headers; this file does not supply header context."
	case "equal_priority_candidates":
		reason = "Matching candidates have equal priority and no guaranteed selection order."
	case "request_context_mutation", "request_context_ambiguous":
		reason = "Routing, rewriting, or request-header rules may change the app, path, or later policy selection."
	case "invalid_rule_action", "invalid_path_selector":
		reason = "A candidate rule has invalid configuration; its effective coverage cannot be established."
	case "budget_limits_unavailable":
		reason = "The app's effective request-budget baseline or ceiling was not available."
	case "application_auth_not_established":
		reason = "Application-owned authentication and authorization cannot be established from platform configuration."
	}
	return Finding{Requirement: requirement, Status: "unknown", Code: code, Reason: reason, NextAction: "Inspect current configuration and use a concrete edge-rules trace or application test to resolve the missing evidence."}
}

func expectedValue(checks Checks, kind string) string {
	switch kind {
	case "authentication":
		return "authentication=" + checks.Authentication
	case "throttle":
		value := "key_by=" + checks.Throttle.KeyBy
		if checks.Throttle.MaxRPS != nil {
			value += fmt.Sprintf("; max_rps=%g", *checks.Throttle.MaxRPS)
		}
		if checks.Throttle.MissingKeyPolicy != "" {
			value += "; missing_key_policy=" + checks.Throttle.MissingKeyPolicy
		}
		return value
	case "budget":
		value := ""
		if checks.Budget.Explicit {
			value = "explicit=true"
		}
		if checks.Budget.MaxMS != nil {
			if value != "" {
				value += "; "
			}
			value += fmt.Sprintf("max_baseline_budget_ms=%d", *checks.Budget.MaxMS)
		}
		return value
	default:
		return ""
	}
}

func selectionFinding(requirement, code string, ids []string) Finding {
	finding := unknown(requirement, code)
	finding.RuleIDs = ids
	return finding
}

func combineStatus(left, right string) string {
	if left == "violated" || right == "violated" {
		return "violated"
	}
	if left == "unknown" || right == "unknown" {
		return "unknown"
	}
	return "satisfied"
}

func decodeAction(rule api.EdgeRuleResponse, kind string, into any) bool {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(rule.Action, &envelope) != nil || string(envelope[kind]) == "null" || len(envelope[kind]) == 0 {
		return false
	}
	return json.Unmarshal(envelope[kind], into) == nil
}

// selectRule shares host/method/path semantics with edge-rule tracing, while
// retaining uncertainty about unspecified headers and equal priorities.
func selectRule(rules []api.EdgeRuleResponse, input edgeruletrace.Input, kind string) (*api.EdgeRuleResponse, string, []string) {
	var candidates []api.EdgeRuleResponse
	invalidGlob := false
	for _, rule := range rules {
		if !rule.Enabled || rule.Kind != kind || !edgeruletrace.HostMatches(rule.MatchHost, input.Host) || !edgeruletrace.MethodMatches(rule.MatchMethods, input.Method) {
			continue
		}
		matches, err := api.MatchEdgeRulePath(rule.MatchPath, input.Path)
		if err != nil {
			invalidGlob = true
		} else if matches {
			candidates = append(candidates, rule)
		}
	}
	if invalidGlob {
		return nil, "invalid_path_selector", nil
	}
	if len(candidates) == 0 {
		return nil, "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority == candidates[j].Priority {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Priority < candidates[j].Priority
	})
	ids := []string{candidates[0].ID}
	for _, candidate := range candidates[1:] {
		if candidate.Priority == candidates[0].Priority {
			ids = append(ids, candidate.ID)
		}
	}
	if len(ids) > 1 {
		return nil, "equal_priority_candidates", ids
	}
	if len(candidates[0].MatchHeaders) > 0 {
		return nil, "header_dependent_rule", ids
	}
	return &candidates[0], "", ids
}

func contextChange(rules []api.EdgeRuleResponse, input edgeruletrace.Input, requestMutations bool) string {
	for _, kind := range []string{"route", "rewrite", "headers"} {
		if !requestMutations && kind != "route" {
			continue
		}
		rule, code, _ := selectRule(rules, input, kind)
		if code != "" {
			return "request_context_ambiguous"
		}
		if rule == nil {
			continue
		}
		if kind == "headers" {
			trace, err := edgeruletrace.Simulate(input, []api.EdgeRuleResponse{*rule})
			if err == nil && len(trace.Rules) == 1 && trace.Rules[0].ActionPreview != nil && len(trace.Rules[0].ActionPreview.RequestHeaderOps) == 0 {
				continue
			}
		}
		return "request_context_mutation"
	}
	return ""
}
