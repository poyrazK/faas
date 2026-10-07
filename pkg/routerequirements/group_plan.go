package routerequirements

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const GroupPlanScope = "Server plan bound to one captured deployment contract and current app policy. Apply revalidates the capture and all overlapping requirements under transaction locks. Wildcard selectors also cover uncaptured and future paths, including descendants for trailing /*. Captured impact describes configured policy selection; shared throttle bucket admission, runtime behavior and application authorization require separate tests. Public exceptions must retain their selected policy."

// NormalizeCoverage produces a stable policy intent. Public rationale is local
// input, not policy: replace it before exporting artifacts or sending requests.
func NormalizeCoverage(config Config) (Config, string, error) {
	if config.Version == 1 {
		return Normalize(config)
	}
	body, err := json.Marshal(config)
	if err != nil {
		return Config{}, "", err
	}
	config, err = ParsePreview(body)
	if err != nil {
		return Config{}, "", err
	}
	return api.CanonicalRouteRequirements(config)
}

func BuildServerGroupPlan(config Config, context Context, options PlanOptions, inventory CoverageInventory) (PolicyPlan, error) {
	config, digest, err := NormalizeCoverage(config)
	if err != nil {
		return PolicyPlan{}, err
	}
	if config.Version != 2 || options.ThrottleBurst < 0 {
		return PolicyPlan{}, errors.New("group planning requires version 2 requirements and a nonnegative burst")
	}
	work := &coverageWork{}
	before := evaluateCoverage(config, digest, context, inventory, work).Report
	plan := PolicyPlan{Version: 3, Authority: "server", DeploymentID: inventory.Deployment, App: context.App.Slug, AppID: context.App.ID,
		RequirementsRevision: options.RequirementsRevision,
		ConsolidateBudgets:   options.ConsolidateBudgets,
		Host:                 context.Host, PlanName: options.PlanName, Scope: GroupPlanScope, Requirements: &config, ThrottleBurst: options.ThrottleBurst,
		RequirementsSHA256: digest, ConfigurationSHA256: configurationDigest(context, options.PlanName), Before: before, After: before,
		Changes: []PolicyChange{}, Unresolved: []PlanUnresolved{}}
	working := context
	working.Rules = append([]api.EdgeRuleResponse(nil), context.Rules...)
	blockers := map[string]string{}
	limits, known := api.LimitsFor(api.Plan(options.PlanName))
	if options.ConsolidateBudgets && known && options.PlanUnavailable == "" && before.Coverage != nil && before.Coverage.Status == "available" && before.Status != "satisfied" {
		working = consolidateGroupBudgets(&plan, working, options, limits, config, digest, inventory, work)
	}
	if !work.exceeded && before.Coverage != nil && before.Coverage.Status == "available" && before.Status != "satisfied" {
		for routeIndex, route := range before.Routes {
			for _, kind := range []string{"throttle", "budget"} {
				// Reuse the latest complete recheck; another family can share this winner.
				row := plan.After.Routes[routeIndex]
				if !groupPlanNeedsChange(row, kind) {
					continue
				}
				if len(plan.Changes) >= api.RouteGroupPlanMaxChanges {
					work.exceeded = true
					break
				}
				key := planFindingKey(route.Method, route.Path, kind)
				if !known || options.PlanUnavailable != "" {
					blockers[key] = "plan_limits_unavailable"
					continue
				}
				checks, code := groupPlanChecks(config, route.Method, route.Path, kind, work)
				if code != "" {
					blockers[key] = code
					continue
				}
				finding := findRequirement(row, kind)
				finding.Expected = expectedValue(checks, kind)
				change, next, code := proposeGroupChange(Requirement{Method: route.Method, Path: route.Path, Require: checks}, finding, working, options, limits, len(plan.Changes)+1, config, inventory, work)
				if code != "" {
					blockers[key] = code
					continue
				}
				after := evaluateCoverage(config, digest, next, inventory, work).Report
				if !groupPlanKindSatisfied(groupPlanRow(after, route.Method, route.Path), kind) || groupPlanWorsened(plan.After, after) {
					blockers[key] = "proposal_does_not_satisfy"
					continue
				}
				change.After = findRequirement(groupPlanRow(after, route.Method, route.Path), kind).Actual
				working, plan.After = next, after
				plan.Changes = append(plan.Changes, change)
			}
			if work.exceeded {
				break
			}
		}
	}
	if work.exceeded {
		plan.Changes = []PolicyChange{}
		plan.After = coverageFailure(config, digest, context, inventory, "group_plan_limit_exceeded").Report
	}
	if options.ConsolidateBudgets && known {
		afterCount := len(working.Rules)
		if work.exceeded {
			afterCount = len(context.Rules)
		}
		plan.RuleUsage = &api.RoutePolicyRuleUsage{Before: len(context.Rules), After: afterCount, Limit: limits.EdgeRulesPerApp}
	}
	plan.After.PolicyScope = "proposed_app"
	plan.After.Scope = strings.Replace(CoverageScope, "current app policy", "proposed app policy", 1)
	groupPlanUnresolved(&plan, blockers)
	switch {
	case len(plan.Unresolved) == 0 && len(plan.Changes) == 0:
		plan.Status = "no_changes"
	case len(plan.Unresolved) == 0:
		plan.Status = "ready"
	case len(plan.Changes) > 0:
		plan.Status = "partial"
	default:
		plan.Status = "blocked"
	}
	plan.SHA256 = jsonDigest(plan)
	return plan, nil
}

func groupPlanRow(report Report, method, path string) RouteResult {
	for _, row := range report.Routes {
		if row.Method == method && row.Path == path {
			return row
		}
	}
	return RouteResult{}
}

func groupPlanNeedsChange(row RouteResult, kind string) bool {
	violated := false
	for _, check := range row.Checks {
		if check.Requirement == kind {
			if check.Status == "unknown" {
				return false
			}
			violated = violated || check.Status == "violated"
		}
	}
	return violated
}

func groupPlanKindSatisfied(row RouteResult, kind string) bool {
	found := false
	for _, check := range row.Checks {
		if check.Requirement == kind {
			found = true
			if check.Status != "satisfied" {
				return false
			}
		}
	}
	return found
}

// Compare aligned checks, including each overlapping group's requirement.
func groupPlanWorsened(before, after Report) bool {
	if after.Coverage == nil || after.Coverage.Status != "available" {
		return true
	}
	if len(before.Routes) != len(after.Routes) {
		return true
	}
	for i, row := range before.Routes {
		next := after.Routes[i]
		if next.Method != row.Method || next.Path != row.Path || len(next.Checks) != len(row.Checks) {
			return true
		}
		for i, check := range row.Checks {
			if check.Status == "satisfied" && next.Checks[i].Status != "satisfied" {
				return true
			}
		}
	}
	return false
}

func groupPlanUnresolved(plan *PolicyPlan, blockers map[string]string) {
	if plan.After.Coverage == nil || plan.After.Coverage.Status != "available" {
		code := "candidate_inventory_unavailable"
		if plan.After.Coverage != nil {
			code = plan.After.Coverage.Code
		}
		plan.Unresolved = append(plan.Unresolved, PlanUnresolved{Requirement: "coverage", Status: "unknown", Code: code, Reason: "Complete captured route inventory or bounded planning work is unavailable.", NextAction: "Select a complete captured deployment contract or reduce the planning scope."})
	}
	for _, group := range plan.After.Groups {
		if group.Code == "group_no_routes" {
			plan.Unresolved = append(plan.Unresolved, PlanUnresolved{Requirement: "assignment", Status: group.Status, Code: group.Code, Reason: "A policy group matches no captured operations.", NextAction: "Correct the group selector or selected deployment contract."})
		}
	}
	for _, row := range plan.After.Routes {
		for _, check := range row.Checks {
			if check.Status == "satisfied" {
				continue
			}
			finding := PlanUnresolved{Method: row.Method, Path: row.Path, Requirement: check.Requirement, Status: check.Status, Code: check.Code, Reason: check.Reason, NextAction: check.NextAction}
			if code := blockers[planFindingKey(row.Method, row.Path, check.Requirement)]; code != "" {
				finding.Code, finding.Reason, finding.NextAction = code, planBlockerReason(code), planBlockerAction(code)
			}
			if finding.Requirement == "authentication" && finding.Status == "violated" {
				finding.Code = "authentication_requires_manual_review"
			}
			plan.Unresolved = append(plan.Unresolved, finding)
		}
	}
}

// Combine all applicable checks before proposing, so stricter overlapping
// ceilings produce one change and incompatible identity dimensions stay manual.
func groupPlanChecks(config Config, method, path, kind string, work *coverageWork) (Checks, string) {
	family, code := parseFamily(path)
	if code != "" {
		return Checks{}, code
	}
	var all []Checks
	for _, group := range config.Groups {
		if !work.use(1 + len(family.segments) + len(group.Methods)) {
			return Checks{}, "group_plan_limit_exceeded"
		}
		if groupMethod(group.Methods, method) && prefixRelation(group.PathPrefix, family) == pathAll {
			all = append(all, group.Require)
		}
	}
	for _, exact := range config.Routes {
		if !work.use(1) {
			return Checks{}, "group_plan_limit_exceeded"
		}
		if exact.Method == method && exact.Path == path {
			all = append(all, exact.Require)
		}
	}
	combined := Checks{}
	for _, checks := range all {
		if kind == "throttle" && checks.Throttle != nil {
			want := *checks.Throttle
			if combined.Throttle == nil {
				combined.Throttle = &want
				continue
			}
			previous := combined.Throttle
			if previous.KeyBy != want.KeyBy || previous.MissingKeyPolicy != "" && want.MissingKeyPolicy != "" && previous.MissingKeyPolicy != want.MissingKeyPolicy {
				return Checks{}, "overlapping_requirements_conflict"
			}
			if previous.MissingKeyPolicy == "" {
				previous.MissingKeyPolicy = want.MissingKeyPolicy
			}
			if want.MaxRPS != nil && (previous.MaxRPS == nil || *want.MaxRPS < *previous.MaxRPS) {
				previous.MaxRPS = want.MaxRPS
			}
		}
		if kind == "budget" && checks.Budget != nil {
			want := *checks.Budget
			if combined.Budget == nil {
				combined.Budget = &want
				continue
			}
			previous := combined.Budget
			previous.Explicit = previous.Explicit || want.Explicit
			if want.MaxMS != nil && (previous.MaxMS == nil || *want.MaxMS < *previous.MaxMS) {
				previous.MaxMS = want.MaxMS
			}
		}
	}
	return combined, ""
}

func proposeGroupChange(requirement Requirement, finding Finding, context Context, options PlanOptions, limits api.Limits, index int, config Config, inventory CoverageInventory, work *coverageWork) (PolicyChange, Context, string) {
	family, code := parseFamily(requirement.Path)
	if code != "" {
		return PolicyChange{}, context, code
	}
	selected, code := familyWinner(context, requirement.Method, finding.Requirement, family, work)
	if code != "" {
		return PolicyChange{}, context, code
	}
	if selected != nil && strings.HasPrefix(selected.ID, "planned:create:") {
		return PolicyChange{}, context, "overlapping_proposals_conflict"
	}
	segments := append([]string(nil), family.segments...)
	for i, parameter := range family.parameters {
		if parameter {
			segments[i] = "*"
		}
	}
	selector := "/" + strings.Join(segments, "/")
	wireRequirement := requirement
	wireRequirement.Path = selector
	change, next, code := proposeSelectedPolicyChange(wireRequirement, finding, context, options, limits, index, selected)
	if code != "" {
		return change, context, code
	}
	change.Impact.Path = requirement.Path
	change.Impact.BeyondCapture = family.variable
	change.Impact.Scope = GroupPlanScope
	change.Reason = "Propose a host and method scoped selector for this captured family; recheck every captured operation and overlapping requirement."
	affected, code := groupPlanImpact(context, next, requirement.Method, selector, finding.Requirement, config, inventory, work)
	if code != "" {
		return PolicyChange{}, context, code
	}
	change.Impact.Captured = affected
	return change, next, ""
}

func groupPlanImpact(before, after Context, method, selector, kind string, config Config, inventory CoverageInventory, work *coverageWork) ([]api.RoutePolicyAffectedOperation, string) {
	var impact []api.RoutePolicyAffectedOperation
	if !work.use(len(config.Public)) {
		return nil, "impact_unavailable"
	}
	public := map[string]bool{}
	for _, exception := range config.Public {
		public[exception.Method+" "+exception.Path] = true
	}
	for _, route := range inventory.Routes {
		if !work.use(1) {
			return nil, "impact_unavailable"
		}
		if route.Method != method {
			continue
		}
		family, code := parseFamily(route.Path)
		if code != "" {
			return nil, "impact_unavailable"
		}
		isPublic := public[method+" "+route.Path]
		if isPublic && !groupPublicContextInvariant(before, route, family, work) {
			return nil, "impact_unavailable"
		}
		relation := selectorRelation(selector, family)
		if relation == pathNone {
			continue
		}
		old, oldCode := familyWinner(before, method, kind, family, work)
		next, nextCode := familyWinner(after, method, kind, family, work)
		if oldCode != "" || nextCode != "" || relation == pathUnknown {
			return nil, "impact_unavailable"
		}
		if !groupImpactBytes(work, old, next) {
			return nil, "impact_unavailable"
		}
		changed := old == nil && next != nil || old != nil && next == nil || old != nil && next != nil && (old.ID != next.ID || !bytes.Equal(old.Action, next.Action))
		if isPublic && changed {
			return nil, "public_exception_would_change"
		}
		if !changed && !isPublic {
			continue
		}
		affected := api.RoutePolicyAffectedOperation{Method: method, Path: route.Path, Relation: "all", Public: isPublic}
		if relation == pathPartial {
			affected.Relation = "partial"
		}
		if old != nil {
			affected.BeforeRuleID = old.ID
		}
		if next != nil {
			affected.AfterRuleID = next.ID
		}
		impact = append(impact, affected)
	}
	return impact, ""
}

// Public intent suppresses requirement findings, so establish request-context
// invariance independently before claiming an exception keeps its policy.
func groupPublicContextInvariant(context Context, route CapturedRoute, family pathFamily, work *coverageWork) bool {
	projected := context
	projected.Rules = nil
	for _, kind := range []string{"route", "rewrite", "headers"} {
		rule, code := familyWinner(context, route.Method, kind, family, work)
		if code != "" {
			return false
		}
		if rule != nil {
			projected.Rules = append(projected.Rules, *rule)
		}
	}
	return contextChange(projected.Rules, traceInput(context, Requirement{Method: route.Method, Path: family.sample}), true) == ""
}

// Public exceptions do not run policy checks; charge their selected actions
// here as well so impact comparisons cannot bypass the shared byte budget.
func groupImpactBytes(work *coverageWork, rules ...*api.EdgeRuleResponse) bool {
	for _, rule := range rules {
		if rule != nil && !work.bytesUsed(len(rule.ID)+len(rule.Action)) {
			return false
		}
	}
	return true
}
