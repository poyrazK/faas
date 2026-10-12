package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sweepRegPlan() testRunPlan {
	s := sweepTestScenario("latency")
	s.Resilience.Runs = 4
	s.Resilience.MinPassRate = .75
	s.Resilience.FaultSweep.MaxRuns = 12
	s.Resilience.FaultSweep.MaxWorkloadMinutes = 48
	s.Resilience.SweepRegression = &testSweepRegressionSpec{MaxPassRateDrop: .25}
	return testRunPlan{Prepared: &testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: s, Profiles: []string{"cold"}}, Profile: "cold", Attempt: 1}
}
func sweepRegReceipt(plan testRunPlan, counts []int, value float64, prefix string) testRunReceipt {
	index := map[string]int{}
	for i, value := range plan.Prepared.Scenario.Resilience.FaultSweep.Values {
		index[value] = i
	}
	row := runSweepTest(context.Background(), plan.Prepared.Scenario, func(ctx context.Context, leaf testRunPlan) testRunReceipt {
		failed := leaf.Attempt > counts[index[leaf.ReplaySweep.Value]]
		r := deadlineTestReceipt(leaf.Prepared.Scenario, fmt.Sprintf("%s-%s-%d", prefix, leaf.ReplaySweep.Value, leaf.Attempt), testMinimizationTarget{Step: "load", Phase: "recovery"}, failed)
		r.Steps[0].Load.ChaosPhases[2].Recovery.RecoveryMS = &value
		return r
	})
	row.Attempt = plan.Attempt
	for i := range row.FaultMatrix.Cases {
		row.FaultMatrix.Cases[i].Attempt = plan.Attempt
	}
	return row
}
func sweepRegAttach(t *testing.T, plan testRunPlan, baseline testRunReceipt) testRunPlan {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := writeTestReport(path, []testRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	plans := []testRunPlan{plan}
	if err := prepareTestSweepBaselines(path, plans); err != nil {
		t.Fatal(err)
	}
	return plans[0]
}
func sweepRegClone(t *testing.T, row testRunReceipt) testRunReceipt {
	t.Helper()
	body, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var copy testRunReceipt
	if err := json.Unmarshal(body, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func TestSweepRegressionPassRatesAndFailingBaseline(t *testing.T) {
	plan := sweepRegPlan()
	baseline := sweepRegReceipt(plan, []int{4, 3, 1}, 100, "before")
	if baseline.Status != "failed" {
		t.Fatal("fixture must contain a failing setting")
	}
	plan = sweepRegAttach(t, plan, baseline)
	for _, mode := range []string{"equal", "allowed", "drop", "lost", "improved"} {
		t.Run(mode, func(t *testing.T) {
			counts := []int{4, 3, 1}
			switch mode {
			case "allowed":
				counts[0] = 3
			case "drop":
				counts[0] = 2
			case "lost":
				counts[1] = 2
			case "improved":
				counts = []int{4, 4, 4}
			}
			current := sweepRegReceipt(plan, counts, 100, "now")
			applyTestSweepBaseline(plan, &current)
			e := current.SweepRegression
			expected := "passed"
			if mode == "drop" || mode == "lost" {
				expected = "failed"
			}
			if e == nil || e.Status != expected || len(e.Checks) != 3 || e.ReportSHA256 == "" {
				t.Fatalf("comparison: %+v", e)
			}
			if mode == "allowed" && (!e.Checks[0].Passed || e.Checks[0].Drop != .25) {
				t.Fatal("exact allowance rejected")
			}
			if mode == "lost" && (!e.Checks[1].LostPassingSetting || !strings.Contains(e.Error, "previously passing")) {
				t.Fatal("lost setting accepted despite allowed drop")
			}
			if mode == "improved" && current.Status != "passed" {
				t.Fatal("improvement failed")
			}
			if mode == "equal" && current.Status != "failed" {
				t.Fatal("comparison hid preexisting sweep failure")
			}
		})
	}
}

func TestSweepRegressionRecoveryReports(t *testing.T) {
	plan := sweepRegPlan()
	plan.Prepared.Scenario.Resilience.SweepRegression.Recovery = &testRecoveryRegressionSpec{P95IncreasePercent: 20, MinSamples: 2}
	baseline := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "before")
	plan = sweepRegAttach(t, plan, baseline)
	exact := sweepRegReceipt(plan, []int{4, 4, 4}, 120, "exact")
	applyTestSweepBaseline(plan, &exact)
	if exact.SweepRegression.Status != "passed" || exact.Status != "passed" || len(exact.SweepRegression.Recovery) != 3 {
		t.Fatalf("boundary: %+v", exact.SweepRegression)
	}
	current := sweepRegReceipt(plan, []int{4, 4, 4}, 121, "slow")
	applyTestSweepBaseline(plan, &current)
	if current.Status != "failed" || current.SweepRegression.Status != "failed" || current.SweepRegression.Recovery[0].BaselineSamples != 4 || !strings.Contains(current.Error, "p95") {
		t.Fatal("slower recovery accepted")
	}
	dir := t.TempDir()
	html := filepath.Join(dir, "report.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{current}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(html)
	if err != nil || !strings.Contains(string(body), "Sweep regression: failed") || !strings.Contains(string(body), "121.000") {
		t.Fatal("HTML evidence missing", err)
	}
	path := filepath.Join(dir, "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{current}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	var suite testJUnitSuite
	if err != nil || xml.Unmarshal(body, &suite) != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].SystemOut, "max_drop") || !strings.Contains(suite.Cases[0].Failure.Message, "p95") {
		t.Fatal("JUnit regression missing")
	}
	incomplete := sweepRegReceipt(plan, []int{1, 4, 4}, 100, "missing")
	applyTestSweepBaseline(plan, &incomplete)
	if incomplete.SweepRegression.Status != "failed" || !strings.Contains(incomplete.SweepRegression.Error, "confirmed samples") {
		t.Fatal("sample shortage not enforced")
	}
}

func TestSweepRegressionRejectsInvalidBaselineAndCurrent(t *testing.T) {
	for _, mode := range []string{"setting", "workload", "runs", "policy", "summary", "duplicate", "id", "cleanup", "skipped", "profile", "attempt", "cases", "prior", "root"} {
		t.Run(mode, func(t *testing.T) {
			plan := sweepRegPlan()
			valid := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "before")
			bad := sweepRegClone(t, valid)
			group := &bad.FaultMatrix.Cases[0]
			leaf := &group.Resilience.History[0]
			switch mode {
			case "setting":
				group.Sweep.Value = "11ms"
			case "workload":
				group.RecoveryWorkloadSHA256 = "wrong"
			case "runs":
				group.Resilience.Runs = 3
			case "policy":
				group.Resilience.MinPassRate = .5
			case "summary":
				group.Resilience.Passed = 999
			case "duplicate":
				group.Resilience.History[1].Attempt = 1
			case "id":
				leaf.RunID = ""
			case "cleanup":
				leaf.CleanupError = "cleanup failed"
			case "skipped":
				leaf.Status = "skipped"
			case "profile":
				bad.Profile = "warm"
			case "attempt":
				bad.Attempt = 2
			case "cases":
				bad.FaultMatrix.Cases = bad.FaultMatrix.Cases[:2]
			case "prior":
				bad.SweepRegression = &testSweepRegressionEvidence{Status: "failed"}
				bad.Status = "failed"
			case "root":
				bad.FaultMatrix.Passed = 999
			}
			path := filepath.Join(t.TempDir(), "bad.json")
			if err := writeTestReport(path, []testRunReceipt{bad}); err != nil {
				t.Fatal(err)
			}
			if err := prepareTestSweepBaselines(path, []testRunPlan{plan}); err == nil {
				t.Fatal("invalid baseline accepted")
			}
			plan = sweepRegAttach(t, plan, valid)
			applyTestSweepBaseline(plan, &bad)
			if bad.Status != "failed" || bad.SweepRegression.Status != "failed" {
				t.Fatal("invalid current report accepted")
			}
		})
	}
}

func TestSweepRegressionValidationAndSelection(t *testing.T) {
	plan := sweepRegPlan()
	for _, value := range []float64{-1, 1.01, math.NaN(), math.Inf(1)} {
		plan.Prepared.Scenario.Resilience.SweepRegression.MaxPassRateDrop = value
		if validateTestResilience(plan.Prepared.Scenario) == nil {
			t.Fatal("bad allowance accepted")
		}
	}
	plan = sweepRegPlan()
	plan.Prepared.Scenario.Resilience.SweepRegression.Recovery = &testRecoveryRegressionSpec{MinSamples: 1}
	if validateTestResilience(plan.Prepared.Scenario) == nil {
		t.Fatal("bad sample floor accepted")
	}
	plan = sweepRegPlan()
	plan.Prepared.Scenario.Resilience.FaultSweep = nil
	if validateTestResilience(plan.Prepared.Scenario) == nil {
		t.Fatal("regression without sweep accepted")
	}
	for _, args := range [][]string{{"--sweep-baseline", "base.json", "--validate"}, {"--sweep-baseline", "base.json", "--preflight", "--scenario", "checkout"}, {"--sweep-baseline", "base.json", "--recovery-baseline", "other.json", "--scenario", "checkout"}, {"--sweep-baseline", "base.json", "--baseline", "other.json", "--scenario", "checkout"}} {
		if cmdTest(args) == 0 {
			t.Fatal("invalid flags accepted")
		}
	}
	plan = sweepRegPlan()
	baseline := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "before")
	path := filepath.Join(t.TempDir(), "base.json")
	if err := writeTestReport(path, []testRunReceipt{baseline, baseline}); err != nil {
		t.Fatal(err)
	}
	if prepareTestSweepBaselines(path, []testRunPlan{plan}) == nil {
		t.Fatal("duplicate root identity accepted")
	}
	if err := writeTestReport(path, []testRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	if prepareTestSweepBaselines(path, []testRunPlan{plan}, path) == nil {
		t.Fatal("output overwrites baseline")
	}
	plan.Prepared.Scenario.Resilience.SweepRegression = nil
	if prepareTestSweepBaselines(path, []testRunPlan{plan}) == nil {
		t.Fatal("missing comparison policy accepted")
	}
}

func TestSweepRegressionEndpointRecoveryAndSampleFloor(t *testing.T) {
	plan := sweepRegPlan()
	spec := *plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[2].Recovery
	plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": &spec}
	plan.Prepared.Scenario.Resilience.SweepRegression.Recovery = &testRecoveryRegressionSpec{P95IncreasePercent: 20, MinSamples: 2}
	addEndpoint := func(row *testRunReceipt, value float64) {
		for i := range row.FaultMatrix.Cases {
			for j := range row.FaultMatrix.Cases[i].Resilience.History {
				phase := &row.FaultMatrix.Cases[i].Resilience.History[j].Steps[0].Load.ChaosPhases[2]
				r := *phase.Recovery
				r.RecoveryMS = &value
				phase.StepRecovery = map[string]*testLoadRecoveryEvidence{"load-probe": &r}
			}
		}
	}
	baseline := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "before")
	addEndpoint(&baseline, 80)
	plan = sweepRegAttach(t, plan, baseline)
	current := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "now")
	addEndpoint(&current, 100)
	applyTestSweepBaseline(plan, &current)
	if current.SweepRegression.Status != "failed" || len(current.SweepRegression.Recovery) != 6 || !strings.Contains(current.Error, "load-probe") {
		t.Fatal("endpoint recovery masked by aggregate")
	}
	for _, check := range current.SweepRegression.Recovery {
		if check.Request == "" && !check.Passed {
			t.Fatal("aggregate baseline mixed with endpoint")
		}
	}
	sparse := sweepRegReceipt(plan, []int{1, 4, 4}, 100, "sparse")
	addEndpoint(&sparse, 80)
	path := filepath.Join(t.TempDir(), "sparse.json")
	if err := writeTestReport(path, []testRunReceipt{sparse}); err != nil {
		t.Fatal(err)
	}
	if prepareTestSweepBaselines(path, []testRunPlan{plan}) == nil {
		t.Fatal("baseline recovery sample shortage accepted")
	}
}

func TestSweepRegressionProfileRepeatSelection(t *testing.T) {
	plan := sweepRegPlan()
	before := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "before")
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := writeTestReport(path, []testRunReceipt{before}); err != nil {
		t.Fatal(err)
	}
	missing := plan
	missing.Attempt = 2
	if prepareTestSweepBaselines(path, []testRunPlan{plan, missing}) == nil {
		t.Fatal("missing outer attempt accepted")
	}
	missing = plan
	missing.Profile = "warm"
	if prepareTestSweepBaselines(path, []testRunPlan{missing}) == nil {
		t.Fatal("missing profile accepted")
	}
	duplicate := sweepRegClone(t, before)
	duplicate.FaultMatrix.Cases[1].Resilience.History[0].RunID = duplicate.FaultMatrix.Cases[0].Resilience.History[0].RunID
	if _, err := sweepRegressionGroups(&duplicate, plan); err == nil {
		t.Fatal("duplicate run ID across settings accepted")
	}
}
