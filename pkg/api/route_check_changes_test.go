package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func changeCheck(status string) RouteRequirementsCheck {
	return RouteRequirementsCheck{RequirementsRevision: 1, RequirementsSHA256: "intent", Report: RouteRequirementsReport{Status: status, Coverage: &RouteCoverageInventory{Status: "available"}, Routes: []RouteRequirementsResult{{Method: "POST", Path: "/checkout/{id}", Checks: []RouteRequirementsFinding{{Requirement: "budget", Expected: "max_ms=500", Status: status, Actual: "budget_ms=500", RuleIDs: []string{"b", "a"}}}}}}}
}

func TestRouteCheckChangesKnownEvidenceAndIntentBoundaries(t *testing.T) {
	initial, baseline := CompareRouteChecks(RouteCheckFindingBaseline{}, changeCheck("satisfied"), "first", true)
	if initial.Status != "initial" || initial.Summary.Observed != 1 {
		t.Fatalf("initial: %+v", initial)
	}
	check := changeCheck("violated")
	check.Report.Routes[0].Checks[0].Actual = "budget_ms=2000"
	bad, baseline := CompareRouteChecks(baseline, check, "bad", true)
	if bad.Summary.NewlyViolated != 1 || bad.Findings[0].BeforeCheckID != "first" {
		t.Fatalf("regression: %+v", bad)
	}
	unknown := changeCheck("unknown")
	uncertain, retained := CompareRouteChecks(baseline, unknown, "unknown", true)
	if uncertain.Summary.Resolved != 0 || uncertain.Summary.Unknown != 1 || retained.Findings[0].Finding.Status != "violated" {
		t.Fatal("unknown cleared known violation")
	}
	again, retained := CompareRouteChecks(retained, check, "again", true)
	if again.Summary.NewlyViolated != 0 || again.Summary.Resolved != 0 {
		t.Fatalf("unknown recovery repeated violation: %+v", again)
	}
	fixed, _ := CompareRouteChecks(retained, changeCheck("satisfied"), "fixed", true)
	if fixed.Summary.Resolved != 1 || fixed.Findings[0].BeforeCheckID != "again" {
		t.Fatalf("confirmed fix: %+v", fixed)
	}
	for _, scenario := range []string{"removed", "changed revision", "changed hash", "capture unavailable", "not entitled", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			current, eligible := changeCheck("satisfied"), true
			switch scenario {
			case "removed":
				current.Report.Routes = nil
			case "changed revision":
				current.RequirementsRevision++
			case "changed hash":
				current.RequirementsSHA256 = "other"
			case "capture unavailable":
				current.Report.Coverage.Status = "unavailable"
			case "not entitled":
				eligible = false
			case "duplicate":
				current.Report.Routes = append(current.Report.Routes, current.Report.Routes[0])
			}
			delta, _ := CompareRouteChecks(baseline, current, "next", eligible)
			if delta.Summary.Resolved != 0 {
				t.Fatalf("unsafe recovery: %+v", delta)
			}
			if scenario == "removed" && delta.Summary.Removed != 1 {
				t.Fatal("removal was not explicit")
			}
		})
	}
}

func TestRouteCheckChangesDeterminismAndBoundedDetail(t *testing.T) {
	_, baseline := CompareRouteChecks(RouteCheckFindingBaseline{}, changeCheck("satisfied"), "first", true)
	check := changeCheck("satisfied")
	check.Report.Routes[0].Checks[0].RuleIDs = []string{"a", "b"}
	delta, _ := CompareRouteChecks(baseline, check, "same", true)
	if len(delta.Findings) != 0 {
		t.Fatal("rule order generated a change")
	}
	check.Report.Routes = nil
	for i := 0; i < 2000; i++ {
		check.Report.Routes = append(check.Report.Routes, RouteRequirementsResult{Method: "POST", Path: fmt.Sprintf("/route/%04d", i), Checks: []RouteRequirementsFinding{{Requirement: "budget", Expected: "max_ms=500", Status: "violated", Actual: strings.Repeat("x", 2000)}}})
	}
	delta, _ = CompareRouteChecks(baseline, check, "large", true)
	body, _ := json.Marshal(delta)
	if !delta.Truncated || delta.Summary.NewlyViolated != 2000 || len(body) > RouteCheckChangesMaxBytes {
		t.Fatalf("lost exact counts or exceeded detail bound: %d %+v", len(body), delta.Summary)
	}
	for i, j := 0, len(check.Report.Routes)-1; i < j; i, j = i+1, j-1 {
		check.Report.Routes[i], check.Report.Routes[j] = check.Report.Routes[j], check.Report.Routes[i]
	}
	reordered, _ := CompareRouteChecks(baseline, check, "large", true)
	other, _ := json.Marshal(reordered)
	if string(body) != string(other) {
		t.Fatal("route order changed comparison")
	}
}
