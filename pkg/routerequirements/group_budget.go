package routerequirements

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type groupBudgetCandidate struct {
	requirement Requirement
	finding     Finding
	selected    *api.EdgeRuleResponse
	action      json.RawMessage
}

// Consolidation is opt-in scope expansion to an existing declared prefix. It
// never deletes rules, merges throttle buckets, or widens an existing selector.
// Unsafe candidates fall back to the ordinary family planner.
func consolidateGroupBudgets(plan *PolicyPlan, context Context, options PlanOptions, limits api.Limits, config Config, digest string, inventory CoverageInventory, work *coverageWork) Context {
	for _, group := range config.Groups {
		if group.Require.Budget == nil {
			continue
		}
		for _, method := range group.Methods {
			candidates := groupBudgetCandidates(group, method, plan.After, context, options, limits, config, work)
			if work.exceeded {
				return context
			}
			if len(candidates) < 2 || groupBudgetAlreadyShared(candidates, work) {
				if work.exceeded {
					return context
				}
				continue
			}
			change, next, ok := proposeConsolidatedBudget(group, method, candidates, context, options, limits, len(plan.Changes)+1, config, inventory, work)
			if !ok {
				continue
			}
			if len(plan.Changes) >= api.RouteGroupPlanMaxChanges {
				work.exceeded = true
				return context
			}
			after := evaluateCoverage(config, digest, next, inventory, work).Report
			if groupPlanWorsened(plan.After, after) || !groupBudgetCandidatesSatisfied(candidates, after, work) {
				continue
			}
			change.After = findRequirement(groupPlanRow(after, method, candidates[0].requirement.Path), "budget").Actual
			plan.Changes = append(plan.Changes, change)
			context, plan.After = next, after
		}
	}
	return context
}

// Avoid broadening scope when one ordinary family selector already covers the
// whole batch. This proof also uses the shared work budget.
func groupBudgetAlreadyShared(candidates []groupBudgetCandidate, work *coverageWork) bool {
	for _, candidate := range candidates {
		family, _ := parseFamily(candidate.requirement.Path)
		segments := append([]string(nil), family.segments...)
		for i, parameter := range family.parameters {
			if parameter {
				segments[i] = "*"
			}
		}
		selector := "/" + strings.Join(segments, "/")
		all := true
		for _, other := range candidates {
			if !work.use(1 + len(family.segments)) {
				return true
			}
			otherFamily, _ := parseFamily(other.requirement.Path)
			if selectorRelation(selector, otherFamily) != pathAll {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func groupBudgetCandidates(group RouteGroup, method string, report Report, context Context, options PlanOptions, limits api.Limits, config Config, work *coverageWork) []groupBudgetCandidate {
	var candidates []groupBudgetCandidate
	for _, row := range report.Routes {
		if !work.use(1) {
			return nil
		}
		if row.Method != method || !groupPlanNeedsChange(row, "budget") {
			continue
		}
		family, code := parseFamily(row.Path)
		if code != "" || prefixRelation(group.PathPrefix, family) != pathAll {
			continue
		}
		for _, check := range row.Checks {
			if check.Status == "unknown" || check.Requirement == "assignment" && check.Status != "satisfied" {
				return nil
			}
		}
		checks, code := groupPlanChecks(config, method, row.Path, "budget", work)
		if code != "" || checks.Budget == nil {
			return nil
		}
		selected, code := familyWinner(context, method, "budget", family, work)
		if code != "" || selected != nil && strings.HasPrefix(selected.ID, "planned:create:") {
			return nil
		}
		requirement := Requirement{Method: method, Path: row.Path, Require: checks}
		action, code := proposedAction(checks, "budget", traceInput(context, Requirement{Method: method, Path: family.sample}), selected, options, limits)
		if code != "" || !work.bytesUsed(len(action)) {
			return nil
		}
		if len(candidates) > 0 && !bytes.Equal(candidates[0].action, action) {
			return nil
		}
		finding := findRequirement(row, "budget")
		finding.Expected = expectedValue(checks, "budget")
		candidates = append(candidates, groupBudgetCandidate{requirement, finding, selected, action})
	}
	return candidates
}

func proposeConsolidatedBudget(group RouteGroup, method string, candidates []groupBudgetCandidate, context Context, options PlanOptions, limits api.Limits, index int, config Config, inventory CoverageInventory, work *coverageWork) (PolicyChange, Context, bool) {
	selected := candidates[0].selected
	for _, candidate := range candidates {
		if candidate.selected != nil && (selected == nil || candidate.selected.Priority < selected.Priority) {
			selected = candidate.selected
		}
	}
	requirement := candidates[0].requirement
	requirement.Path = group.PathPrefix + "*"
	change, next, code := proposeSelectedPolicyChange(requirement, candidates[0].finding, context, options, limits, index, selected)
	if code != "" {
		return PolicyChange{}, context, false
	}
	// The chosen precedence winner must generate the very same allowlisted
	// action independently proposed for every candidate (including tighter
	// overlaps and existing baselines); equal requirements alone are insufficient.
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(candidates[0].action, &envelope)
	if change.Create != nil && !bytes.Equal(change.Create.Action, envelope["budget"]) || change.Update != nil && !bytes.Equal(*change.Update.Action, envelope["budget"]) {
		return PolicyChange{}, context, false
	}
	affected, code := groupPlanImpact(context, next, method, requirement.Path, "budget", config, inventory, work)
	if code != "" || !groupBudgetImpactConfined(candidates, affected) {
		return PolicyChange{}, context, false
	}
	change.BudgetGroup = group.Name
	change.Impact.Captured, change.Impact.BeyondCapture = affected, true
	change.Impact.Scope = GroupPlanScope
	change.Before = "Individual captured budget violations; see before coverage."
	change.Reason = "Consolidate identical budget proposals within the declared group prefix for this host and method. The prefix also covers uncaptured and future paths; existing rules are retained."
	change.DisplacedRuleIDs = nil
	seen := map[string]bool{}
	for _, route := range affected {
		if !route.Public && route.BeforeRuleID != "" && route.BeforeRuleID != route.AfterRuleID && !seen[route.BeforeRuleID] {
			seen[route.BeforeRuleID] = true
			change.DisplacedRuleIDs = append(change.DisplacedRuleIDs, route.BeforeRuleID)
		}
	}
	sort.Strings(change.DisplacedRuleIDs)
	return change, next, true
}

// A consolidation may only change its known violations; satisfied, unassigned
// or unknown operations must retain their selected policy. Public exceptions
// are separately proved invariant by groupPlanImpact, including rewrites.
func groupBudgetImpactConfined(candidates []groupBudgetCandidate, affected []api.RoutePolicyAffectedOperation) bool {
	paths := map[string]bool{}
	for _, candidate := range candidates {
		paths[candidate.requirement.Path] = true
	}
	changed := 0
	for _, route := range affected {
		if route.Public {
			continue
		}
		if !paths[route.Path] || route.Relation != "all" {
			return false
		}
		changed++
	}
	return changed == len(candidates)
}

func groupBudgetCandidatesSatisfied(candidates []groupBudgetCandidate, report Report, work *coverageWork) bool {
	if !work.use(len(report.Routes) + len(candidates)) {
		return false
	}
	rows := make(map[string]RouteResult, len(report.Routes))
	for _, row := range report.Routes {
		rows[row.Method+" "+row.Path] = row
	}
	for _, candidate := range candidates {
		row := rows[candidate.requirement.Method+" "+candidate.requirement.Path]
		if !work.use(len(row.Checks)) || !groupPlanKindSatisfied(row, "budget") {
			return false
		}
	}
	return true
}
