package routerequirements

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const CoverageScope = "Captured candidate operations assigned to current app policy on the platform hostname. Family proofs cover canonical decoded paths with nonempty whole-segment parameters; encoded aliases, dot segments, separators inside parameters, framework normalization, implicit methods, custom hosts, historical deployment policy, application authorization and runtime admission are outside this check. Public exceptions declare intent and do not change authentication. Budget checks use configured baselines without request-header overrides."

type PreviewReport struct{ Report }

func WrapReport(report Report) PreviewReport { return PreviewReport{Report: report} }

type CapturedRoute = api.RouteCapturedOperation
type CoverageInventory = api.RouteCoverageInventory
type GroupResult = api.RouteGroupResult
type RouteAssignment = api.RouteAssignment
type AssignedCheck = api.RouteAssignedCheck

type coverageWork struct {
	nodes, bytes, findings int
	exceeded               bool
}

func (work *coverageWork) use(nodes int) bool {
	work.nodes += nodes
	work.exceeded = work.exceeded || work.nodes > api.RouteCoverageMaxNodes
	return !work.exceeded
}

func (work *coverageWork) bytesUsed(count int) bool {
	work.bytes += count
	work.exceeded = work.exceeded || work.bytes > api.RouteCoverageMaxWorkBytes
	return !work.exceeded
}

func (work *coverageWork) metadata(value string) bool {
	if !work.bytesUsed(len(value)) || len(value) > api.RouteCoverageMaxMetadataBytes || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		work.exceeded = true
	}
	return !work.exceeded
}

func coverageFailure(config PreviewConfig, digest string, context Context, inventory CoverageInventory, code string) PreviewReport {
	inventory.Status, inventory.Code, inventory.Routes = "unavailable", code, nil
	report := PreviewReport{Report: Report{Version: 2, SHA256: digest, Host: context.Host, PolicyScope: "current_app", Status: "unknown", Scope: CoverageScope, Routes: []RouteResult{}, Coverage: &inventory}}
	for _, group := range config.Groups {
		report.Groups = append(report.Groups, GroupResult{Name: group.Name, Status: "unknown", Code: code})
	}
	return report
}

// EvaluatePreview v2 requires an assignment for every captured candidate
// operation. Overlapping groups are conjunctive. No inventory or bounded-work
// failure can leave behind a partial successful coverage report.
func EvaluatePreview(config PreviewConfig, digest string, context Context, inventory CoverageInventory) PreviewReport {
	return evaluateCoverage(config, digest, context, inventory, &coverageWork{})
}

func evaluateCoverage(config PreviewConfig, digest string, context Context, inventory CoverageInventory, work *coverageWork) PreviewReport {
	if config.Version == 1 {
		return WrapReport(Evaluate(Config{Version: 1, Routes: config.Routes}, digest, context))
	}
	if inventory.Status != "available" || len(inventory.Routes) == 0 {
		code := inventory.Code
		if code == "" {
			code = "candidate_inventory_unavailable"
		}
		return coverageFailure(config, digest, context, inventory, code)
	}
	if len(inventory.Routes) > api.RouteCoverageMaxInventoryRoutes || len(context.Rules) > api.RouteCoverageMaxRules {
		return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
	}
	for _, rule := range context.Rules {
		if !work.use(1+len(rule.MatchHeaders)+len(rule.MatchMethods)) || !work.metadata(rule.ID) || !work.metadata(rule.MatchHost) || !work.metadata(rule.MatchPath) || !work.bytesUsed(len(rule.Action)) {
			return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
		}
		for key, value := range rule.MatchHeaders {
			if !work.metadata(key) || !work.metadata(value) {
				return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
			}
		}
		for _, method := range rule.MatchMethods {
			if !work.metadata(method) {
				return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
			}
		}
	}
	visibleInventory := inventory
	visibleInventory.Routes, visibleInventory.RouteCount = nil, len(inventory.Routes)
	report := PreviewReport{Report: Report{Version: 2, SHA256: digest, Host: context.Host, PolicyScope: "current_app", Status: "satisfied", Scope: CoverageScope, Routes: []RouteResult{}, Coverage: &visibleInventory}}
	for _, group := range config.Groups {
		report.Groups = append(report.Groups, GroupResult{Name: group.Name, Status: "satisfied"})
	}
	public, exact, found := map[string]bool{}, map[string]Requirement{}, map[string]bool{}
	for _, exception := range config.Public {
		public[exception.Method+" "+exception.Path] = true
	}
	for _, route := range config.Routes {
		exact[route.Method+" "+route.Path] = route
	}
	for _, route := range inventory.Routes {
		work.use(1)
		if !work.metadata(route.Path) || !work.metadata(route.Method) || !coverageMethod(route.Method) {
			return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
		}
		key := route.Method + " " + route.Path
		if found[key] {
			return coverageFailure(config, digest, context, inventory, "candidate_inventory_ambiguous")
		}
		found[key] = true
		family, familyCode := parseFamily(route.Path)
		row := RouteResult{Method: route.Method, Path: route.Path, Status: "satisfied", Checks: []Finding{}}
		assignment := RouteAssignment{Method: route.Method, Path: route.Path, Scope: "route_family"}
		if familyCode == "" && !family.variable {
			assignment.Scope = "concrete_path"
		}
		assigned := false
		for i, group := range config.Groups {
			if !work.use(1 + len(family.segments)) {
				return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
			}
			if !groupMethod(group.Methods, route.Method) {
				continue
			}
			relation := pathUnknown
			if familyCode == "" {
				relation = prefixRelation(group.PathPrefix, family)
			}
			if relation == pathNone {
				continue
			}
			assigned = true
			assignment.Groups = append(assignment.Groups, group.Name)
			report.Groups[i].MatchedRoutes++
			if public[key] {
				report.Groups[i].ExemptRoutes++
				continue
			}
			var checks []Finding
			if familyCode != "" || relation != pathAll {
				code := familyCode
				if code == "" {
					code = "group_membership_varies_with_path"
				}
				checks = []Finding{coverageUnknown(code)}
			} else {
				checks = evaluateFamily(group.Require, route, family, context, work)
			}
			for _, check := range checks {
				assignment.Checks = append(assignment.Checks, AssignedCheck{Group: group.Name, CheckIndex: len(row.Checks)})
				row.Checks = append(row.Checks, check)
				report.Groups[i].Status = combineStatus(report.Groups[i].Status, check.Status)
			}
		}
		if requirement, exists := exact[key]; exists {
			assigned, row.Name = true, requirement.Name
			row.Checks = append(row.Checks, evaluateFamily(requirement.Require, route, family, context, work)...)
			assignment.Scope = "concrete_path"
		}
		switch {
		case public[key]:
			assignment.Scope = "public_exception"
			assignment.Checks = nil
			row.Checks = []Finding{{Requirement: "assignment", Status: "satisfied", Code: "public_exception", Expected: "explicit_public_assignment", Reason: "This captured operation has an explicit public exception. This does not establish or change its runtime access."}}
		case !assigned && familyCode != "":
			row.Checks = append(row.Checks, coverageUnknown(familyCode))
		case !assigned:
			assignment.Scope = "unassigned"
			row.Checks = append(row.Checks, Finding{Requirement: "assignment", Status: "violated", Code: "route_uncovered", Expected: "group_or_exact_or_public_assignment", Reason: "The captured candidate operation has no policy assignment.", NextAction: "Assign the operation to a policy group, exact route requirement, or explicit public exception."})
		}
		for _, finding := range row.Checks {
			work.findings++
			row.Status = combineStatus(row.Status, finding.Status)
		}
		if work.exceeded || work.findings > api.RouteCoverageMaxFindings {
			return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
		}
		report.Status = combineStatus(report.Status, row.Status)
		report.Routes = append(report.Routes, row)
		report.Assignments = append(report.Assignments, assignment)
	}
	for i := range report.Groups {
		if report.Groups[i].MatchedRoutes == 0 {
			report.Groups[i].Status, report.Groups[i].Code = "violated", "group_no_routes"
		}
		report.Status = combineStatus(report.Status, report.Groups[i].Status)
	}
	// Stale exact assignments must also be visible and fail the coverage gate.
	for _, route := range config.Routes {
		if !found[route.Method+" "+route.Path] {
			work.findings++
			report.Routes = append(report.Routes, missingAssignment(route.Method, route.Path))
			report.Status = combineStatus(report.Status, "unknown")
		}
	}
	for _, exception := range config.Public {
		if !found[exception.Method+" "+exception.Path] {
			work.findings++
			report.Routes = append(report.Routes, missingAssignment(exception.Method, exception.Path))
			report.Status = combineStatus(report.Status, "unknown")
		}
	}
	if work.findings > api.RouteCoverageMaxFindings {
		return coverageFailure(config, digest, context, inventory, "coverage_limit_exceeded")
	}
	return report
}

func groupMethod(methods []string, method string) bool {
	for _, candidate := range methods {
		if candidate == method {
			return true
		}
	}
	return false
}

func coverageUnknown(code string) Finding {
	return Finding{Requirement: "coverage", Status: "unknown", Code: code, Expected: "whole_family_coverage", Reason: "Available selectors or path syntax cannot establish one policy across this route family.", NextAction: "Review path-family syntax, rule precedence, conditional selectors, and explicit route assignments."}
}

func missingAssignment(method, path string) RouteResult {
	return RouteResult{Method: method, Path: path, Status: "unknown", Checks: []Finding{{Requirement: "assignment", Status: "unknown", Code: "assignment_not_in_candidate", Expected: "captured_candidate_operation", Reason: "This exact assignment is absent from the captured candidate inventory.", NextAction: "Correct or remove the stale assignment, or capture the missing candidate operation."}}}
}

func evaluateFamily(checks Checks, route CapturedRoute, family pathFamily, context Context, work *coverageWork) []Finding {
	var result []Finding
	for _, kind := range []string{"authentication", "throttle", "budget"} {
		one := Checks{}
		switch kind {
		case "authentication":
			one.Authentication = checks.Authentication
		case "throttle":
			one.Throttle = checks.Throttle
		case "budget":
			one.Budget = checks.Budget
		}
		if one.Authentication == "" && one.Throttle == nil && one.Budget == nil {
			continue
		}
		projected := context
		projected.Rules = nil
		code := ""
		if context.Unavailable == "" && one.Authentication != "application" {
			kinds := []string{"route"}
			if one.Authentication != "consumer" {
				kinds = append(kinds, "rewrite", "headers")
			}
			if kind != "authentication" {
				kinds = append(kinds, kind)
			} else if one.Authentication == "jwt" {
				kinds = append(kinds, "jwt")
			}
			for _, ruleKind := range kinds {
				rule, reason := familyWinner(context, route.Method, ruleKind, family, work)
				if reason != "" {
					code = reason
					break
				}
				if rule != nil {
					work.bytesUsed(len(rule.Action))
					projected.Rules = append(projected.Rules, *rule)
				}
			}
		}
		if code != "" {
			finding := coverageUnknown(code)
			finding.Requirement, finding.Expected = kind, expectedValue(checks, kind)
			result = append(result, finding)
			continue
		}
		// Only now is the representative used: every selected policy has been
		// proven invariant, so the legacy action and baseline checks apply.
		single := Evaluate(Config{Version: 1, Routes: []Requirement{{Method: route.Method, Path: family.sample, Require: one}}}, "", projected)
		result = append(result, single.Routes[0].Checks...)
	}
	return result
}
