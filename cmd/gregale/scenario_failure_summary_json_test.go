package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

func machineFailureReceipt() testRunReceipt {
	return testRunReceipt{Status: "failed", Scenario: "checkout", Error: "load failed",
		Steps: []testScenarioStepEvidence{{Name: "campaign", Status: "failed", Load: &testLoadEvidence{
			ChaosPhases: []testLoadChaosPhaseEvidence{{
				Name: "outage", Role: "fault",
				Thresholds: []testLoadThresholdEvidence{{Metric: "p95_ms", Operator: "<=", Limit: 100, Actual: 200}},
			}},
		}}},
	}
}

func decodeMachineFailure(t *testing.T, body []byte) *testFailureSummary {
	t.Helper()
	var wire struct {
		Summary *testFailureSummary `json:"failure_summary"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Summary
}

func TestMachineFailureSummaryPreservesNumbersAndRecomputesAfterDecode(t *testing.T) {
	// adr: 984 — derived report data cannot override the original assertion.
	r := machineFailureReceipt()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	summary := decodeMachineFailure(t, body)
	if summary == nil || summary.Version != 1 || summary.Step != "campaign" || summary.Phase != "outage" ||
		summary.Assertion == nil || summary.Assertion.Name != "p95_ms" || summary.Assertion.Expected == nil ||
		*summary.Assertion.Expected != 100 || summary.Assertion.Observed == nil || *summary.Assertion.Observed != 200 {
		t.Fatalf("incomplete machine summary: %+v", summary)
	}
	if summary.CleanupStatus != "not_verified" || summary.ReplayAvailable || len(summary.FaultEvidence) != 0 {
		t.Fatal("missing evidence became a confirmed result")
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	wire["failure_summary"] = json.RawMessage(`{"version":1,"assertion":{"observed":0}}`)
	edited, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var restored testRunReceipt
	if err := json.Unmarshal(edited, &restored); err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if observed := decodeMachineFailure(t, body).Assertion.Observed; observed == nil || *observed != 200 {
		t.Fatal("saved derived data overrode the raw threshold")
	}
}

func TestMachineFailureSummaryOmitsPassingGroupsAndKeepsNestedFailures(t *testing.T) {
	// adr: 984 — passing pass-rate groups retain their tolerated failed runs.
	r := testRunReceipt{Status: "passed", Resilience: &testResilienceEvidence{History: []testRunReceipt{machineFailureReceipt()}}}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if decodeMachineFailure(t, body) != nil {
		t.Fatal("passing group acquired a failure summary")
	}
	var wire struct {
		Resilience struct {
			History []json.RawMessage `json:"history"`
		} `json:"resilience"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Resilience.History) != 1 || decodeMachineFailure(t, wire.Resilience.History[0]) == nil {
		t.Fatal("nested raw receipt lost its machine failure")
	}
}

func TestMachineFailureSummaryJUnitRetainsSameReceiptAndVerdict(t *testing.T) {
	// adr: 984 — JUnit embeds the same structured receipt without changing status.
	path := filepath.Join(t.TempDir(), "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{machineFailureReceipt()}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Failures != 1 || len(suite.Cases) != 1 || suite.Cases[0].Failure == nil {
		t.Fatal("JUnit verdict changed")
	}
	summary := decodeMachineFailure(t, []byte(suite.Cases[0].SystemOut))
	if summary == nil || summary.Assertion == nil || summary.Assertion.Observed == nil || *summary.Assertion.Observed != 200 {
		t.Fatal("JUnit omitted structured assertion evidence")
	}
}

func TestMachineFailureSummaryRetainsZeroMatchesAndCleanupErrors(t *testing.T) {
	// adr: 984 — zero matches and cleanup errors remain independent evidence.
	r := machineFailureReceipt()
	r.CleanupError = "namespace remains"
	r.Steps[0].Load.ChaosPhases[0].Chaos = &testChaosEvidence{
		Matches: []testChaosRuleEvidence{{Matches: 0, MinMatches: 1}},
	}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	summary := decodeMachineFailure(t, body)
	if summary.CleanupStatus != "error_recorded" || len(summary.Cleanup) != 1 ||
		summary.FaultPhase != "outage" || len(summary.FaultEvidence) != 1 ||
		summary.FaultEvidence[0].Matches != 0 || summary.FaultEvidence[0].Minimum != 1 {
		t.Fatalf("evidence changed during serialization: %+v", summary)
	}
}
