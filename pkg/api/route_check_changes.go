package api

import (
	"encoding/json"
	"sort"
)

// The bounded baseline keeps the last known verdict for each currently
// observed finding, independently of history eviction and transient unknowns.
type RouteFindingObservation struct {
	Method  string                   `json:"method"`
	Path    string                   `json:"path"`
	CheckID string                   `json:"check_id"`
	Finding RouteRequirementsFinding `json:"finding"`
}

type RouteCheckFindingBaseline struct {
	CheckID              string                    `json:"check_id,omitempty"`
	RequirementsRevision int64                     `json:"requirements_revision"`
	RequirementsSHA256   string                    `json:"requirements_sha256"`
	Findings             []RouteFindingObservation `json:"findings"`
}

func routeFindingKey(method, path string, finding RouteRequirementsFinding) string {
	// A JSON tuple avoids collisions and includes the check's declared
	// expectation, so different overlapping checks cannot resolve one another.
	body, _ := json.Marshal([]string{method, path, finding.Requirement, finding.Expected})
	return string(body)
}

func routeCheckObservations(check RouteRequirementsCheck, id string) (map[string]RouteFindingObservation, bool) {
	observations := map[string]RouteFindingObservation{}
	for _, route := range check.Report.Routes {
		for _, finding := range route.Checks {
			key := routeFindingKey(route.Method, route.Path, finding)
			if _, exists := observations[key]; exists || len(observations) >= RouteCoverageMaxFindings {
				return nil, false
			}
			observations[key] = RouteFindingObservation{route.Method, route.Path, id, finding}
		}
	}
	return observations, true
}

// CompareRouteChecks is shared by persistence, notifications and result views.
// Only complete captured evidence with unchanged saved intent can resolve a
// violation. Unknown findings retain their last known observation. Disappeared
// findings are removed from observation, never counted as resolved.
func CompareRouteChecks(previous RouteCheckFindingBaseline, check RouteRequirementsCheck, id string, eligible bool) (RouteCheckChanges, RouteCheckFindingBaseline) {
	delta := RouteCheckChanges{Version: 1, CheckID: id, ComparedToCheckID: previous.CheckID, Status: "unavailable", Findings: []RouteFindingChange{}}
	if !eligible || check.Report.Coverage == nil || check.Report.Coverage.Status != "available" {
		return delta, previous
	}
	current, valid := routeCheckObservations(check, id)
	if !valid {
		delta.Status = "ambiguous"
		return delta, previous
	}
	before := map[string]RouteFindingObservation{}
	delta.Status = "comparable"
	if previous.CheckID == "" {
		delta.Status = "initial"
	} else if previous.RequirementsRevision != check.RequirementsRevision || previous.RequirementsSHA256 != check.RequirementsSHA256 {
		delta.Status = "requirements_changed"
	} else {
		for _, observation := range previous.Findings {
			before[routeFindingKey(observation.Method, observation.Path, observation.Finding)] = observation
		}
	}
	next := RouteCheckFindingBaseline{CheckID: id, RequirementsRevision: check.RequirementsRevision, RequirementsSHA256: check.RequirementsSHA256, Findings: []RouteFindingObservation{}}
	keys := make([]string, 0, len(current)+len(before))
	for key := range current {
		keys = append(keys, key)
	}
	for key := range before {
		if _, exists := current[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		old, hadOld := before[key]
		observation, exists := current[key]
		if !exists {
			delta.Findings = append(delta.Findings, RouteFindingChange{Method: old.Method, Path: old.Path, Requirement: old.Finding.Requirement, Kind: "removed", BeforeCheckID: old.CheckID, Before: &old.Finding})
			delta.Summary.Removed++
			continue
		}
		change := RouteFindingChange{Method: observation.Method, Path: observation.Path, Requirement: observation.Finding.Requirement, After: &observation.Finding}
		if hadOld {
			change.Before, change.BeforeCheckID = &old.Finding, old.CheckID
		}
		known := observation.Finding.Status == "satisfied" || observation.Finding.Status == "violated"
		switch {
		case !known:
			change.Kind = "unknown"
			delta.Summary.Unknown++
			if hadOld {
				next.Findings = append(next.Findings, old)
			}
		case delta.Status != "comparable":
			change.Kind = "observed"
			delta.Summary.Observed++
		case observation.Finding.Status == "violated" && (!hadOld || old.Finding.Status != "violated"):
			change.Kind = "newly_violated"
			delta.Summary.NewlyViolated++
		case hadOld && old.Finding.Status == "violated" && observation.Finding.Status == "satisfied":
			change.Kind = "resolved"
			delta.Summary.Resolved++
		case !hadOld || !sameRouteFinding(old.Finding, observation.Finding):
			change.Kind = "changed"
			delta.Summary.Changed++
		}
		if known {
			next.Findings = append(next.Findings, observation)
		}
		if change.Kind != "" {
			delta.Findings = append(delta.Findings, change)
		}
	}
	encoded, _ := json.Marshal(next)
	if len(encoded) > RouteCheckMaxResultBytes {
		delta.Status, delta.Summary, delta.Findings = "tracking_limit", RouteCheckChangeSummary{}, []RouteFindingChange{}
		return delta, previous
	}
	boundRouteCheckChanges(&delta)
	return delta, next
}

func sameRouteFinding(a, b RouteRequirementsFinding) bool {
	// Rule order and explanatory copy do not represent policy changes.
	left, right := append([]string(nil), a.RuleIDs...), append([]string(nil), b.RuleIDs...)
	sort.Strings(left)
	sort.Strings(right)
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	return a.Status == b.Status && a.Code == b.Code && a.Expected == b.Expected && a.Actual == b.Actual && string(x) == string(y)
}

func boundRouteCheckChanges(delta *RouteCheckChanges) {
	body, _ := json.Marshal(delta)
	if len(body) <= RouteCheckChangesMaxBytes {
		return
	}
	delta.Truncated = true
	// Keep exact aggregate counts, but make omitted detail explicit.
	used, keep := 1024, 0
	for _, finding := range delta.Findings {
		body, _ := json.Marshal(finding)
		if used+len(body)+1 > RouteCheckChangesMaxBytes/2 {
			break
		}
		used += len(body) + 1
		keep++
	}
	delta.Findings = delta.Findings[:keep]
}
