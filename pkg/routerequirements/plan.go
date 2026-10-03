package routerequirements

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const PlanScope = "Read-only proposals for concrete requests on the app's platform hostname. Rechecks use current configuration and configured budget baselines, not live requests. Exact selectors limit policy selection changes to the named host, method, and path; changing throttle buckets can also affect admission for traffic remaining in a former shared bucket. Reads are not an atomic policy snapshot. Bundled plan limits are advisory; the API must revalidate ownership, entitlements, quotas, and current configuration before any write."

type PlanOptions struct {
	RequirementsRevision int64
	ConsolidateBudgets   bool
	PlanName             string
	PlanUnavailable      string
	ThrottleBurst        int
}

type PolicyPlan = api.RoutePolicyPlan

type PolicyChange = api.RoutePolicyChange

type PolicyImpact = api.RoutePolicyImpact

type PlanUnresolved = api.RoutePlanUnresolved

// BuildPlan accepts a validated Config from Parse. It never mutates context,
// sends application requests, or performs control-plane writes.
func BuildPlan(config Config, digest string, context Context, options PlanOptions) PolicyPlan {
	// Stable order also determines allocation when a plan has limited rule slots.
	config.Routes = append([]Requirement(nil), config.Routes...)
	sort.Slice(config.Routes, func(i, j int) bool {
		return config.Routes[i].Method+" "+config.Routes[i].Path < config.Routes[j].Method+" "+config.Routes[j].Path
	})
	plan := PolicyPlan{
		Version: 1, App: context.App.Slug, AppID: context.App.ID, Host: context.Host,
		PlanName: options.PlanName, Scope: PlanScope, RequirementsSHA256: digest,
		ConfigurationSHA256: configurationDigest(context, options.PlanName),
		Before:              Evaluate(config, digest, context), Changes: []PolicyChange{}, Unresolved: []PlanUnresolved{},
	}
	working := context
	working.Rules = append([]api.EdgeRuleResponse(nil), context.Rules...)
	limits, knownPlan := api.LimitsFor(api.Plan(options.PlanName))
	blockers := map[string]string{}
	for i, requirement := range config.Routes {
		for _, finding := range plan.Before.Routes[i].Checks {
			if finding.Status != "violated" || (finding.Requirement != "throttle" && finding.Requirement != "budget") {
				continue
			}
			key := planFindingKey(requirement.Method, requirement.Path, finding.Requirement)
			if !knownPlan || options.PlanUnavailable != "" {
				blockers[key] = "plan_limits_unavailable"
				continue
			}
			change, next, code := proposePolicyChange(requirement, finding, working, options, limits, len(plan.Changes)+1)
			if code != "" {
				blockers[key] = code
				continue
			}
			recheck := Evaluate(Config{Version: 1, Routes: []Requirement{requirement}}, digest, next)
			result := findRequirement(recheck.Routes[0], finding.Requirement)
			if result.Status != "satisfied" || worsenedChecks(plan.Before.Routes[i], recheck.Routes[0]) {
				blockers[key] = "proposal_does_not_satisfy"
				continue
			}
			change.After = result.Actual
			plan.Changes = append(plan.Changes, change)
			working = next
		}
	}
	plan.After = Evaluate(config, digest, working)
	plan.After.PolicyScope = "proposed_app"
	plan.After.Scope = strings.Replace(Scope, "Current app configuration", "Proposed app configuration", 1)
	for _, route := range plan.After.Routes {
		for _, finding := range route.Checks {
			if finding.Status == "satisfied" {
				continue
			}
			unresolved := PlanUnresolved{Method: route.Method, Path: route.Path, Requirement: finding.Requirement,
				Status: finding.Status, Code: finding.Code, Reason: finding.Reason, NextAction: finding.NextAction}
			if code := blockers[planFindingKey(route.Method, route.Path, finding.Requirement)]; code != "" {
				unresolved.Code, unresolved.Reason, unresolved.NextAction = code, planBlockerReason(code), planBlockerAction(code)
			} else if finding.Requirement == "authentication" && finding.Status == "violated" {
				unresolved.Code = "authentication_requires_manual_review"
				unresolved.NextAction = "Review app-wide consumer authentication or JWT configuration separately; this planner proposes only throttle and budget changes."
			}
			plan.Unresolved = append(plan.Unresolved, unresolved)
		}
	}
	switch {
	case len(plan.Changes) == 0 && len(plan.Unresolved) == 0:
		plan.Status = "no_changes"
	case len(plan.Changes) == 0:
		plan.Status = "blocked"
	case len(plan.Unresolved) > 0:
		plan.Status = "partial"
	default:
		plan.Status = "ready"
	}
	plan.SHA256 = jsonDigest(plan)
	return plan
}

func proposePolicyChange(requirement Requirement, finding Finding, context Context, options PlanOptions, limits api.Limits, index int) (PolicyChange, Context, string) {
	input := traceInput(context, requirement)
	selected, code, _ := selectRule(context.Rules, input, finding.Requirement)
	if code != "" {
		return PolicyChange{}, context, code
	}
	return proposeSelectedPolicyChange(requirement, finding, context, options, limits, index, selected)
}

// Selection is proved by the caller; the requirement path is the wire selector.
func proposeSelectedPolicyChange(requirement Requirement, finding Finding, context Context, options PlanOptions, limits api.Limits, index int, selected *api.EdgeRuleResponse) (PolicyChange, Context, string) {
	input := traceInput(context, requirement)
	if selected != nil && (selected.Priority < 0 || selected.Priority > api.RoutePlanPriorityMax) {
		return PolicyChange{}, context, "invalid_rule_priority"
	}
	action, code := proposedAction(requirement.Require, finding.Requirement, input, selected, options, limits)
	if code != "" {
		return PolicyChange{}, context, code
	}
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(action, &envelope)
	wireAction := envelope[finding.Requirement]
	change := PolicyChange{Kind: finding.Requirement, Expected: finding.Expected, Before: finding.Actual,
		Impact: PolicyImpact{Host: context.Host, Method: requirement.Method, Path: requirement.Path,
			Scope: "Policy selection for all requests on this exact host, method, and path. Other paths, methods, and hosts keep their configured selectors."}}
	next := context
	next.Rules = append([]api.EdgeRuleResponse(nil), context.Rules...)
	if selected != nil && exactSelector(*selected, context.Host, requirement) {
		change.Operation, change.RuleID = "update", selected.ID
		change.Update = &api.UpdateEdgeRuleRequest{Action: &wireAction}
		change.Reason = "Update only the action of the selected rule, whose selector already covers exactly this request shape."
		for i := range next.Rules {
			if next.Rules[i].ID == selected.ID {
				next.Rules[i].Action = action
			}
		}
		return change, next, ""
	}
	if selected != nil && selected.Priority == 0 {
		return PolicyChange{}, context, "priority_space_exhausted"
	}
	if code := createQuota(context.Rules, finding.Requirement, limits); code != "" {
		return PolicyChange{}, context, code
	}
	priority, enabled := 0, true
	if selected != nil {
		priority = selected.Priority - 1
		change.DisplacedRuleIDs = []string{selected.ID}
	}
	change.Operation = "create"
	change.SimulatedRuleID = fmt.Sprintf("planned:create:%d", index)
	for containsRuleID(context.Rules, change.SimulatedRuleID) {
		change.SimulatedRuleID += ":new"
	}
	change.Create = &api.CreateEdgeRuleRequest{Kind: finding.Requirement, MatchHost: context.Host, MatchPath: requirement.Path,
		MatchMethods: []string{requirement.Method}, Priority: &priority, Enabled: &enabled, Action: wireAction}
	change.Reason = "Create an exact-selector rule before the current winner, preserving its broader configuration for other request shapes."
	if selected == nil {
		change.Reason = "Create an exact-selector rule where no enabled rule of this kind currently matches."
	}
	next.Rules = append(next.Rules, api.EdgeRuleResponse{ID: change.SimulatedRuleID, AppID: context.App.ID,
		Kind: finding.Requirement, Enabled: true, MatchHost: context.Host, MatchPath: requirement.Path,
		MatchMethods: []string{requirement.Method}, Priority: priority, Action: action})
	return change, next, ""
}

func exactSelector(rule api.EdgeRuleResponse, host string, requirement Requirement) bool {
	return rule.MatchHost == host && rule.MatchPath == requirement.Path && len(rule.MatchMethods) == 1 &&
		strings.EqualFold(rule.MatchMethods[0], requirement.Method) && len(rule.MatchHeaders) == 0
}

func createQuota(rules []api.EdgeRuleResponse, kind string, limits api.Limits) string {
	if len(rules) >= limits.EdgeRulesPerApp {
		return "app_rule_quota_reached"
	}
	if kind == "throttle" {
		count := 0
		for _, rule := range rules {
			if rule.Kind == kind {
				count++
			}
		}
		if count >= limits.EdgeRulesThrottlePerApp {
			return "throttle_rule_quota_reached"
		}
	}
	return ""
}

func containsRuleID(rules []api.EdgeRuleResponse, id string) bool {
	for _, rule := range rules {
		if rule.ID == id {
			return true
		}
	}
	return false
}

func planFindingKey(method, path, kind string) string { return method + " " + path + " " + kind }

func findRequirement(route RouteResult, kind string) Finding {
	for _, finding := range route.Checks {
		if finding.Requirement == kind {
			return finding
		}
	}
	return Finding{}
}

func worsenedChecks(before, after RouteResult) bool {
	for _, finding := range before.Checks {
		if finding.Status == "satisfied" && findRequirement(after, finding.Requirement).Status != "satisfied" {
			return true
		}
	}
	return false
}
