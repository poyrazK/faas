package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func groupingTestReceipt(id string, observed, limit float64, operator string) testRunReceipt {
	return testRunReceipt{
		Scenario: "checkout", Engine: "real-vm", Profile: "warm", RunID: id, Status: "failed",
		Replay: &testReplayManifest{Version: 1, Scenario: "checkout", Profile: "warm"},
		Load: &testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{{
			Name: "outage", Role: "fault",
			Thresholds: []testLoadThresholdEvidence{{Metric: "p95_ms", Operator: operator, Actual: observed, Limit: limit}},
		}}},
	}
}

func TestFailureGroupsCountExecutedRunsAndRetainWorstLimit(t *testing.T) {
	// adr: 984 — frequency and worst measurements preserve independent run evidence.
	a := groupingTestReceipt("a", 200, 100, "<=")
	b := groupingTestReceipt("b", 300, 150, "<=")
	passed, skipped := a, a
	passed.RunID, passed.Status = "passed", "passed"
	skipped.RunID, skipped.Status = "skipped", "skipped"
	r := testRunReceipt{Status: "passed", Resilience: &testResilienceEvidence{History: []testRunReceipt{a, b, passed, skipped}}}
	groups := r.FailureGroups()
	if len(groups) != 1 || groups[0].Failures != 2 || groups[0].ExecutedRuns != 3 ||
		groups[0].WorstObserved == nil || *groups[0].WorstObserved != 300 ||
		groups[0].WorstLimit == nil || *groups[0].WorstLimit != 150 || len(groups[0].Occurrences) != 2 {
		t.Fatalf("wrong grouping: %+v", groups)
	}
	if r.Status != "passed" || r.FailureSummary() != nil || groups[0].Occurrences[0].Replay == "" {
		t.Fatal("tolerated failures changed the verdict or lost replay")
	}
}

func TestFailureGroupsKeepMatrixCasesSeparate(t *testing.T) {
	// adr: 984 — a healthy matrix case cannot dilute another case's frequency.
	a := groupingTestReceipt("a", 200, 100, "<=")
	b := groupingTestReceipt("b", 250, 100, "<=")
	r := testRunReceipt{FaultMatrix: &testFaultMatrixEvidence{Cases: []testRunReceipt{
		{Case: "reset", Resilience: &testResilienceEvidence{History: []testRunReceipt{a}}},
		{Case: "timeout", Resilience: &testResilienceEvidence{History: []testRunReceipt{b}}},
	}}}
	groups := r.FailureGroups()
	if len(groups) != 2 || groups[0].Key.Case == groups[1].Key.Case ||
		groups[0].ExecutedRuns != 1 || groups[1].ExecutedRuns != 1 || a.Case != "" {
		t.Fatalf("case scopes mixed or source mutated: %+v", groups)
	}
}

func TestFailureGroupsRespectLowerBoundsAndMissingMeasurements(t *testing.T) {
	// adr: 984 — opposite directions and missing samples cannot fabricate a worst value.
	a := groupingTestReceipt("a", 0.8, 0.9, ">=")
	b := groupingTestReceipt("b", 0.5, 0.9, ">=")
	r := testRunReceipt{Resilience: &testResilienceEvidence{History: []testRunReceipt{a, b}}}
	groups := r.FailureGroups()
	if len(groups) != 1 || groups[0].WorstObserved == nil || *groups[0].WorstObserved != 0.5 {
		t.Fatal("lower bound selected the wrong worst observation")
	}
	b.Load.ChaosPhases[0].Thresholds[0].Operator = "<="
	groups = r.FailureGroups()
	if groups[0].Operator != "mixed" || groups[0].WorstObserved != nil || groups[0].WorstLimit != nil {
		t.Fatal("opposing comparisons reported a single worst value")
	}
	a.Load.ChaosPhases[0].Thresholds = nil
	a.Error = "unclassified failure"
	r.Resilience.History = []testRunReceipt{a}
	groups = r.FailureGroups()
	if len(groups) != 1 || groups[0].WorstObserved != nil || groups[0].Key.Kind != "unclassified" {
		t.Fatal("unclassified failure became a measured zero")
	}
}

func TestFailureGroupsSerializeAndLinkEscapedEvidence(t *testing.T) {
	// adr: 984 — grouping remains derived, escaped evidence in every report format.
	a := groupingTestReceipt("<script>run</script>", 200, 100, "<=")
	r := testRunReceipt{Scenario: "group", Status: "failed", Resilience: &testResilienceEvidence{History: []testRunReceipt{a}}}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Groups []testFailureGroup `json:"failure_groups"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Groups) != 1 || wire.Groups[0].Version != 1 || wire.Groups[0].Occurrences[0].RunID != a.RunID {
		t.Fatal("machine grouping lost occurrence identity")
	}
	var restored testRunReceipt
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.FailureGroups()) != 1 {
		t.Fatal("grouping could not be regenerated")
	}
	path := filepath.Join(t.TempDir(), "groups.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{r}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	if !strings.Contains(html, "Recurring failures") || !strings.Contains(html, "&lt;script&gt;run&lt;/script&gt;") ||
		!strings.Contains(html, `href="#run-evidence-0"`) || strings.Contains(html, "<script>run</script>") {
		t.Fatal("grouping omitted or failed to escape its linked evidence")
	}
}

func TestFailureGroupsSeparateObservedFaultKinds(t *testing.T) {
	// adr: 984 — different injected fault kinds retain separate groups.
	a := groupingTestReceipt("reset", 200, 100, "<=")
	b := groupingTestReceipt("latency", 200, 100, "<=")
	a.Load.ChaosPhases[0].Chaos = &testChaosEvidence{Matches: []testChaosRuleEvidence{{Rule: chaos.Rule{Kind: "tcp_reset"}, Matches: 1}}}
	b.Load.ChaosPhases[0].Chaos = &testChaosEvidence{Matches: []testChaosRuleEvidence{{Rule: chaos.Rule{Kind: "tcp_latency"}, Matches: 1}}}
	r := testRunReceipt{Resilience: &testResilienceEvidence{History: []testRunReceipt{a, b}}}
	groups := r.FailureGroups()
	if len(groups) != 2 || groups[0].Key.FaultKinds == groups[1].Key.FaultKinds ||
		groups[0].ExecutedRuns != 2 || groups[1].ExecutedRuns != 2 {
		t.Fatal("fault kinds were merged or denominator changed")
	}
}
