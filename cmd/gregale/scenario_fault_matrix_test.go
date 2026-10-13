package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func faultMatrixTestScenario() testScenario {
	zero := 0.0
	probe := func(name string) testHTTPRequest { r := loadTestStep(); r.Name = name; return r }
	rule := testChaosRule{To: "cache", Kind: "tcp_latency", Port: 6379, Percent: 100, Latency: "100ms"}
	return testScenario{Project: "checkout", Services: map[string]testService{"cache": {TCPPorts: []int{6379}}}, Resilience: &testResilienceSpec{Runs: 2, MinPassRate: .5, FaultMatrix: []testFaultMatrixCaseSpec{
		{Name: "slow", Step: "load", Phase: "fault", Rules: []testChaosRule{rule}},
		{Name: "reset", Step: "load", Phase: "fault", Rules: []testChaosRule{{To: "cache", Kind: "tcp_reset", Port: 6379, Percent: 100, ResetAfter: "100ms"}}},
	}}, Steps: []testScenarioStep{
		{Name: "baseline", Requests: []testHTTPRequest{probe("baseline-probe")}},
		{Name: "load", Requests: []testHTTPRequest{probe("load-probe")}, Load: &testLoadSpec{Duration: "3s", ChaosSchedule: []testLoadChaosPhaseSpec{
			{Name: "healthy", Duration: "1s"}, {Name: "fault", Duration: "1s", Rules: []testChaosRule{rule}},
			{Name: "recovery", Duration: "1s", Recovery: &testLoadRecoverySpec{Within: "500ms", Sustain: "200ms", Window: "200ms", Thresholds: testLoadStepThresholdSpec{ErrorRate: &zero, MinSamples: 1}}},
		}}},
		{Name: "recovery-check", Requests: []testHTTPRequest{probe("recovery-probe")}},
	}}
}

func TestFaultMatrixValidationAndIsolation(t *testing.T) {
	scenario := faultMatrixTestScenario()
	if err := validateTestResilience(scenario); err != nil {
		t.Fatal(err)
	}
	missingRecovery := faultMatrixTestScenario()
	missingRecovery.Steps[1].Load.ChaosSchedule[2].Recovery = nil
	if _, err := applyTestFaultMatrixCase(missingRecovery, missingRecovery.Resilience.FaultMatrix[0]); err == nil {
		t.Fatal("target without immediate recovery assertion accepted")
	}
	strict := 1.0
	spec := scenario.Resilience.FaultMatrix[1]
	spec.MinPassRate = &strict
	spec.Recovery = &testLoadRecoverySpec{Within: "600ms", Sustain: "200ms", Window: "200ms", Thresholds: scenario.Steps[1].Load.ChaosSchedule[2].Recovery.Thresholds}
	variant, err := applyTestFaultMatrixCase(scenario, spec)
	if err != nil {
		t.Fatal(err)
	}
	if variant.Resilience.MinPassRate != 1 || len(variant.Resilience.FaultMatrix) != 0 || variant.Steps[1].Load.ChaosSchedule[1].Rules[0].Kind != "tcp_reset" || variant.Steps[1].Load.ChaosSchedule[2].Recovery.Within != "600ms" {
		t.Fatal("case overrides missing")
	}
	variant.Steps[1].Load.ChaosSchedule[1].Rules[0].To = "other"
	variant.Steps[1].Load.ChaosSchedule[2].Recovery.Within = "700ms"
	if scenario.Steps[1].Load.ChaosSchedule[1].Rules[0].To != "cache" || scenario.Steps[1].Load.ChaosSchedule[2].Recovery.Within != "500ms" || scenario.Resilience.MinPassRate != .5 {
		t.Fatal("template mutated")
	}
	if scenario.Resilience.FaultMatrix[1].Rules[0].To != "cache" || spec.Recovery.Within != "600ms" {
		t.Fatal("case spec mutated")
	}
	for _, kind := range []string{"duplicate", "unknown-phase", "healthy-phase", "bad-port", "empty-rules", "nan-rate", "bad-recovery", "one-case"} {
		t.Run(kind, func(t *testing.T) {
			s := faultMatrixTestScenario()
			c := &s.Resilience.FaultMatrix[1]
			switch kind {
			case "duplicate":
				c.Name = "slow"
			case "unknown-phase":
				c.Phase = "missing"
			case "healthy-phase":
				c.Phase = "healthy"
			case "bad-port":
				c.Rules[0].Port = 9999
			case "empty-rules":
				c.Rules = nil
			case "nan-rate":
				v := math.NaN()
				c.MinPassRate = &v
			case "bad-recovery":
				c.Recovery = &testLoadRecoverySpec{Within: "invalid"}
			case "one-case":
				s.Resilience.FaultMatrix = s.Resilience.FaultMatrix[:1]
			}
			if err := validateTestResilience(s); err == nil || !strings.Contains(err.Error(), "fault_matrix") {
				t.Fatalf("invalid case accepted: %v", err)
			}
		})
	}
	scenario.Timeout = "1m"
	if got := estimateTestWorkloadMinutes(scenario, 2); got != 16 {
		t.Fatalf("matrix omitted from resource estimate: %d", got)
	}
}

func TestFaultMatrixCaseBudgetsAndReports(t *testing.T) {
	scenario := faultMatrixTestScenario()
	strict := 1.0
	scenario.Resilience.FaultMatrix[1].MinPassRate = &strict
	plan := testRunPlan{Prepared: &testPreparedScenario{Name: "matrix", Scenario: scenario, Engine: "real-vm"}, Profile: "warm"}
	calls := 0
	receipt := runTestFaultMatrix(context.Background(), plan, func(ctx context.Context, p testRunPlan) testRunReceipt {
		return runTestResilience(ctx, p, func(_ context.Context, r testRunPlan) testRunReceipt {
			calls++
			kind := r.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[0].Kind
			value := 100.0
			if kind == "tcp_reset" {
				value = 200
			}
			result := testRunReceipt{Scenario: "matrix", Engine: "real-vm", Profile: "warm", Status: "passed", Steps: []testScenarioStepEvidence{{Name: "load", Load: &testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{{Name: "recovery", Recovery: &testLoadRecoveryEvidence{Passed: true, RecoveryMS: &value}}}}}}}
			if kind == "tcp_reset" && r.Attempt == 2 {
				result.Status = "failed"
				result.Error = "recovery failed"
			}
			return result
		})
	})
	if receipt.Status != "failed" || calls != 4 || receipt.FaultMatrix.Passed != 1 || receipt.FaultMatrix.Failed != 1 || !strings.Contains(receipt.Error, `case "reset"`) {
		t.Fatalf("case failure masked: %+v", receipt)
	}
	if receipt.FaultMatrix.Cases[0].Resilience.Recovery[0].P95MS != 100 || receipt.FaultMatrix.Cases[1].Resilience.Recovery[0].P95MS != 200 {
		t.Fatal("case metrics mixed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].Failure.Message, `case "reset"`) {
		t.Fatalf("JUnit matrix failure missing: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || len(decoded.FaultMatrix.Cases[1].Resilience.History) != 2 {
		t.Fatalf("individual run evidence lost: %v", err)
	}
	path = filepath.Join(dir, "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	for _, text := range []string{"Dependency fault matrix", "Case reset: failed", "Recovery p95", "200.000 ms", "Run 2: failed"} {
		if !strings.Contains(string(body), text) {
			t.Fatalf("HTML missing %q", text)
		}
	}
}

func TestFaultMatrixStopsAfterCleanupOrCancellation(t *testing.T) {
	for _, kind := range []string{"cleanup", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			plan := testRunPlan{Prepared: &testPreparedScenario{Name: "matrix", Scenario: faultMatrixTestScenario(), Engine: "real-vm"}, Profile: "warm"}
			calls := 0
			receipt := runTestFaultMatrix(ctx, plan, func(context.Context, testRunPlan) testRunReceipt {
				calls++
				result := testRunReceipt{Status: "passed"}
				if kind == "cleanup" {
					result.CleanupError = "namespace deletion failed"
				} else {
					cancel()
				}
				return result
			})
			if calls != 1 || receipt.Status != "failed" || receipt.FaultMatrix.Skipped != 1 {
				t.Fatalf("matrix continued after stop: %+v", receipt)
			}
		})
	}
}
