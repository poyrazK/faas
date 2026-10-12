package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryRegressionGroup(scenario testScenario, value float64) testRunReceipt {
	history := []testRunReceipt{}
	for i := 1; i <= scenario.Resilience.Runs; i++ {
		v := value
		recovery := &testLoadRecoveryEvidence{Passed: true, RecoveryMS: &v}
		load := &testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{{Name: "recovery", Recovery: recovery}}}
		run := testRunReceipt{Attempt: i, Status: "passed", Steps: []testScenarioStepEvidence{{Name: "load", Load: load}}}
		history = append(history, run)
	}
	return testRunReceipt{Scenario: "matrix", Engine: "real-vm", Profile: "warm", Attempt: 1, Status: "passed", RecoveryWorkloadSHA256: recoveryWorkloadSHA256(scenario), Resilience: &testResilienceEvidence{Runs: scenario.Resilience.Runs, History: history}}
}
func recoveryRegressionPlan() testRunPlan {
	scenario := faultMatrixTestScenario()
	scenario.Resilience.FaultMatrix = nil
	scenario.Resilience.RecoveryRegression = &testRecoveryRegressionSpec{P95IncreasePercent: 20, MinSamples: 2}
	return testRunPlan{Prepared: &testPreparedScenario{Name: "matrix", Engine: "real-vm", Scenario: scenario}, Profile: "warm", Attempt: 1}
}

func TestRecoveryRegressionPreparationAndComparison(t *testing.T) {
	plan := recoveryRegressionPlan()
	baseline := recoveryRegressionGroup(plan.Prepared.Scenario, 100)
	baseline.Resilience.Recovery = []testResilienceRecoveryEvidence{{Step: "load", Phase: "recovery", Samples: 999, P95MS: 9999}}
	dir := t.TempDir()
	path := filepath.Join(dir, "previous.json")
	if err := writeTestReport(path, []testRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	plans := []testRunPlan{plan}
	if err := prepareTestRecoveryBaselines(path, plans); err != nil {
		t.Fatal(err)
	}
	if plans[0].RecoveryBaseline == nil || plans[0].RecoveryBaselineSHA256 == "" {
		t.Fatal("baseline not attached")
	}
	current := recoveryRegressionGroup(plan.Prepared.Scenario, 120)
	applyTestRecoveryBaseline(plans[0], &current)
	if current.Status != "passed" || current.RecoveryRegression.Status != "passed" || current.RecoveryRegression.Checks[0].LimitMS != 120 {
		t.Fatalf("exact boundary rejected: %+v", current)
	}
	current = recoveryRegressionGroup(plan.Prepared.Scenario, 121)
	applyTestRecoveryBaseline(plans[0], &current)
	if current.Status != "failed" || !strings.Contains(current.Error, "load/recovery") || current.RecoveryRegression.Checks[0].BaselineSamples != 2 {
		t.Fatalf("regression not enforced: %+v", current)
	}
	reportPath := filepath.Join(dir, "report.xml")
	if err := writeTestJUnit(reportPath, []testRunReceipt{current}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(reportPath)
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].Failure.Message, "load/recovery") {
		t.Fatalf("JUnit regression missing: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || decoded.RecoveryRegression.Checks[0].CurrentP95MS != 121 {
		t.Fatalf("JSON regression lost: %v", err)
	}
	reportPath = filepath.Join(dir, "report.html")
	if err := writeTestHTMLReport(reportPath, "", []testRunReceipt{current}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(reportPath)
	for _, text := range []string{"Recovery regression: failed", "Previous p95", "121.000 ms", "100.000 ms"} {
		if !strings.Contains(string(body), text) {
			t.Fatalf("HTML missing %q", text)
		}
	}
	if err := prepareTestRecoveryBaselines(path, []testRunPlan{plan}, path); err == nil {
		t.Fatal("baseline overwrite allowed")
	}
}

func TestRecoveryRegressionRejectsWeakOrMismatchedBaselines(t *testing.T) {
	for _, kind := range []string{"weak", "missing-metadata", "changed-fault", "failed", "duplicate-attempt", "wrong-profile", "duplicate-root"} {
		t.Run(kind, func(t *testing.T) {
			plan := recoveryRegressionPlan()
			receipt := recoveryRegressionGroup(plan.Prepared.Scenario, 100)
			switch kind {
			case "weak":
				receipt.Resilience.History[1].Steps[0].Load.ChaosPhases[0].Recovery.Passed = false
			case "missing-metadata":
				receipt.RecoveryWorkloadSHA256 = ""
			case "changed-fault":
				plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[0].Latency = "200ms"
			case "failed":
				receipt.Status = "failed"
			case "duplicate-attempt":
				receipt.Resilience.History[1].Attempt = 1
			case "wrong-profile":
				receipt.Profile = "cold"
			}
			receipts := []testRunReceipt{receipt}
			if kind == "duplicate-root" {
				receipts = append(receipts, receipt)
			}
			path := filepath.Join(t.TempDir(), "previous.json")
			if err := writeTestReport(path, receipts); err != nil {
				t.Fatal(err)
			}
			if err := prepareTestRecoveryBaselines(path, []testRunPlan{plan}); err == nil {
				t.Fatal("invalid baseline accepted")
			}
		})
	}
}

func TestRecoveryRegressionCurrentSamplesAndFailures(t *testing.T) {
	plan := recoveryRegressionPlan()
	baseline := recoveryRegressionGroup(plan.Prepared.Scenario, 100)
	plan.RecoveryBaseline = &baseline
	current := recoveryRegressionGroup(plan.Prepared.Scenario, 100)
	current.Resilience.History[1].Steps[0].Load.ChaosPhases[0].Recovery.Passed = false
	applyTestRecoveryBaseline(plan, &current)
	if current.Status != "failed" || !strings.Contains(current.Error, "confirmed samples") {
		t.Fatal("weak current samples passed")
	}
	current = recoveryRegressionGroup(plan.Prepared.Scenario, 100)
	current.Status = "failed"
	current.Error = "original failure"
	applyTestRecoveryBaseline(plan, &current)
	if current.RecoveryRegression.Status != "not_compared" || current.Error != "original failure" {
		t.Fatal("execution failure overwritten")
	}
	original := recoveryWorkloadSHA256(plan.Prepared.Scenario)
	plan.Prepared.Scenario.Source = "different-code-version"
	if recoveryWorkloadSHA256(plan.Prepared.Scenario) != original {
		t.Fatal("application code changes invalidated experiment")
	}
}

func TestRecoveryRegressionMatrixCases(t *testing.T) {
	plan := recoveryRegressionPlan()
	plan.Prepared.Scenario.Resilience.FaultMatrix = faultMatrixTestScenario().Resilience.FaultMatrix
	baseline := testRunReceipt{Scenario: "matrix", Engine: "real-vm", Profile: "warm", Attempt: 1, Status: "passed", FaultMatrix: &testFaultMatrixEvidence{}}
	current := baseline
	current.FaultMatrix = &testFaultMatrixEvidence{}
	for _, spec := range plan.Prepared.Scenario.Resilience.FaultMatrix {
		variant, err := applyTestFaultMatrixCase(plan.Prepared.Scenario, spec)
		if err != nil {
			t.Fatal(err)
		}
		before := recoveryRegressionGroup(variant, 100)
		before.Case = spec.Name
		baseline.FaultMatrix.Cases = append(baseline.FaultMatrix.Cases, before)
		value := 110.0
		if spec.Name == "reset" {
			value = 130
		}
		now := recoveryRegressionGroup(variant, value)
		now.Case = spec.Name
		current.FaultMatrix.Cases = append(current.FaultMatrix.Cases, now)
	}
	path := filepath.Join(t.TempDir(), "previous.json")
	if err := writeTestReport(path, []testRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	plans := []testRunPlan{plan}
	if err := prepareTestRecoveryBaselines(path, plans); err != nil {
		t.Fatal(err)
	}
	applyTestRecoveryBaseline(plans[0], &current)
	if current.Status != "failed" || len(current.RecoveryRegression.Checks) != 2 || !current.RecoveryRegression.Checks[0].Passed || current.RecoveryRegression.Checks[1].Passed || !strings.Contains(current.Error, `case "reset"`) {
		t.Fatalf("case regression masked: %+v", current.RecoveryRegression)
	}
}
