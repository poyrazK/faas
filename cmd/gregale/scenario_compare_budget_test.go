package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func comparisonBudgetReceipt(p95, p99 float64, failures, samples int, duration, requestDuration int64, status string) testRunReceipt {
	errorRate := 0.0
	if samples > 0 {
		errorRate = float64(failures) / float64(samples)
	}
	metrics := testLoadMetrics{
		Requests: samples, Failures: failures, ErrorRate: errorRate,
		Statuses: map[string]int{"200": samples - failures},
		Latency: testLoadLatency{
			Samples: samples, MinMS: p95 / 2, MeanMS: p95, P50MS: p95 * .7,
			P95MS: p95, P99MS: p99, MaxMS: p99,
		},
	}
	workload := &testLoadWorkload{Version: 1, SHA256: strings.Repeat("a", 64), Label: "health-v1"}
	load := &testLoadEvidence{
		Status: "passed", Workload: workload, testLoadMetrics: metrics,
		Steps: []testLoadStepEvidence{{Name: "health", Method: "GET", Path: "/health", testLoadMetrics: metrics}},
	}
	return testRunReceipt{
		Scenario: "health", Engine: "local", Profile: "local", Attempt: 1,
		DurationMS: duration, Status: status,
		Requests: []testHTTPRequestEvidence{{Name: "health", Method: "GET", Path: "/health", DurationMS: requestDuration, Passed: status == "passed"}},
		Load:     load,
	}
}

func comparisonBudgetForTest() testComparisonBudget {
	return testComparisonBudget{
		RequirePassing: true, RequireSameRuns: true,
		RunDurationIncreasePercent:  regressionFloat(20),
		HTTPDurationIncreasePercent: regressionFloat(20),
		LoadP95IncreasePercent:      regressionFloat(10),
		LoadP99IncreasePercent:      regressionFloat(20),
		LoadErrorRateIncreasePoints: regressionFloat(1),
		LoadMinSamples:              20,
	}
}

func repeatedComparisonBudgetReceipts(p95 []float64, status string) []testRunReceipt {
	receipts := make([]testRunReceipt, 0, len(p95))
	for index, value := range p95 {
		receipt := comparisonBudgetReceipt(value, value*1.5, 0, 100, int64(value*10), int64(value/2), status)
		receipt.Attempt = index + 1
		receipts = append(receipts, receipt)
	}
	return receipts
}

func repeatedComparisonBudgetForTest() testComparisonBudget {
	return testComparisonBudget{
		RequirePassing: true, RequireSameRuns: true,
		RunDurationIncreasePercent:  regressionFloat(10),
		HTTPDurationIncreasePercent: regressionFloat(10),
		LoadP95IncreasePercent:      regressionFloat(10),
		LoadMinSamples:              20,
		Repeat: &testComparisonRepeatBudget{
			MinRuns: 3, MaxSpreadPercent: 25, MaxErrorRateSpreadPercentagePoints: 1,
		},
	}
}

func findComparisonGateCheck(gate testComparisonGate, metric string) (testComparisonGateCheck, bool) {
	for _, check := range gate.Checks {
		if check.Metric == metric {
			return check, true
		}
	}
	return testComparisonGateCheck{}, false
}

func TestEvaluateTestComparisonBudgetPassesInclusiveLimits(t *testing.T) {
	before := []testRunReceipt{comparisonBudgetReceipt(100, 150, 1, 100, 1000, 50, "passed")}
	after := []testRunReceipt{comparisonBudgetReceipt(110, 180, 2, 100, 1200, 60, "passed")}
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, comparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Status != "passed" || gate.Failed != 0 || gate.Inconclusive != 0 || gate.Passed != 9 {
		t.Fatalf("gate = %+v", gate)
	}
}

func TestEvaluateTestComparisonBudgetFailsRunAndMetricRegressions(t *testing.T) {
	before := []testRunReceipt{comparisonBudgetReceipt(100, 150, 1, 100, 1000, 50, "passed")}
	after := []testRunReceipt{comparisonBudgetReceipt(111, 181, 3, 100, 1300, 70, "failed")}
	after[0].Error = "assertion failed"
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, comparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Status != "failed" || gate.Failed < 7 || gate.Inconclusive != 0 {
		t.Fatalf("gate = %+v", gate)
	}
	for _, metric := range []string{"run.status", "run.duration_ms", "http.health.duration_ms", "load.p95_latency", "load.p99_latency", "load.error_rate"} {
		found := false
		for _, check := range gate.Checks {
			if check.Metric == metric && check.Status == "failed" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no failed gate check for %s: %+v", metric, gate.Checks)
		}
	}
}

func TestEvaluateTestComparisonBudgetFailsClosedOnWorkloadMismatch(t *testing.T) {
	before := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")}
	after := []testRunReceipt{comparisonBudgetReceipt(90, 120, 0, 100, 900, 45, "passed")}
	after[0].Load.Workload.SHA256 = strings.Repeat("b", 64)
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, comparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Status != "failed" || gate.Inconclusive == 0 {
		t.Fatalf("mismatched workload passed gate: %+v", gate)
	}
	found := false
	for _, check := range gate.Checks {
		if check.Metric == "load.workload" && check.Status == "inconclusive" && strings.Contains(check.Reason, "identity changed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("workload mismatch was not reported: %+v", gate.Checks)
	}
}

func TestEvaluateTestComparisonBudgetRejectsSparseAndChangedHTTPSteps(t *testing.T) {
	before := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")}
	after := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 19, 1000, 50, "passed")}
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, comparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Status != "failed" || gate.Inconclusive == 0 {
		t.Fatalf("sparse load passed gate: %+v", gate)
	}

	after = []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")}
	after[0].Requests[0].Path = "/ready"
	comparison, err = compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err = evaluateTestComparisonBudget(comparison, before, after, comparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Status != "failed" || gate.Inconclusive == 0 {
		t.Fatalf("changed HTTP step passed gate: %+v", gate)
	}
}

func TestEvaluateTestComparisonBudgetRequiresSameRunSetWhenConfigured(t *testing.T) {
	before := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")}
	after := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")}
	after[0].Scenario = "new-health"
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, comparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Status != "failed" {
		t.Fatalf("changed run set passed gate: %+v", gate)
	}
	found := false
	for _, check := range gate.Checks {
		if check.Metric == "run.identity" && check.Status == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("run set difference was not reported: %+v", gate.Checks)
	}
}

func TestEvaluateTestComparisonRepeatBudgetUsesMediansAndReportsSpread(t *testing.T) {
	before := repeatedComparisonBudgetReceipts([]float64{98, 100, 102}, "passed")
	after := repeatedComparisonBudgetReceipts([]float64{106, 108, 110}, "passed")
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, repeatedComparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	if gate.Status != "passed" || gate.Inconclusive != 0 {
		t.Fatalf("gate = %+v", gate)
	}
	check, ok := findComparisonGateCheck(gate, "load.p95_latency")
	if !ok || check.Status != "passed" || check.Before == nil || *check.Before != 100 || check.After == nil || *check.After != 108 {
		t.Fatalf("p95 median check = %+v, found=%v", check, ok)
	}
	if check.BeforeRuns != 3 || check.AfterRuns != 3 || check.BeforeSpread == nil || math.Abs(*check.BeforeSpread-4) > 1e-9 || check.AfterSpread == nil || math.Abs(*check.AfterSpread-(4.0/108*100)) > 1e-9 {
		t.Fatalf("p95 repeat statistics = %+v", check)
	}
	if _, ok := findComparisonGateCheck(gate, "run.duration_ms"); !ok {
		t.Fatalf("run duration median was not checked: %+v", gate.Checks)
	}
	if _, ok := findComparisonGateCheck(gate, "http.health.duration_ms"); !ok {
		t.Fatalf("HTTP duration median was not checked: %+v", gate.Checks)
	}
}

func TestEvaluateTestComparisonRepeatBudgetMarksNoisyGroupsInconclusive(t *testing.T) {
	before := repeatedComparisonBudgetReceipts([]float64{99, 100, 101}, "passed")
	after := repeatedComparisonBudgetReceipts([]float64{100, 100, 160}, "passed")
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, repeatedComparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	check, ok := findComparisonGateCheck(gate, "load.p95_latency")
	if !ok || check.Status != "inconclusive" || check.Before == nil || *check.Before != 100 || check.After == nil || *check.After != 100 || !strings.Contains(check.Reason, "spread exceeds") {
		t.Fatalf("noisy p95 check = %+v, found=%v", check, ok)
	}
	if gate.Status != "failed" || gate.Inconclusive == 0 {
		t.Fatalf("noisy repeated measurements passed the gate: %+v", gate)
	}
}

func TestEvaluateTestComparisonRepeatBudgetUsesErrorRateMediansAndPointSpread(t *testing.T) {
	before := repeatedComparisonBudgetReceipts([]float64{99, 100, 101}, "passed")
	after := repeatedComparisonBudgetReceipts([]float64{99, 100, 101}, "passed")
	setRepeatedComparisonFailures(before, []int{1, 2, 1})
	setRepeatedComparisonFailures(after, []int{1, 2, 2})
	budget := repeatedComparisonBudgetForTest()
	budget.RunDurationIncreasePercent = nil
	budget.HTTPDurationIncreasePercent = nil
	budget.LoadP95IncreasePercent = nil
	budget.LoadErrorRateIncreasePoints = regressionFloat(1)
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, budget)
	if err != nil {
		t.Fatal(err)
	}
	check, ok := findComparisonGateCheck(gate, "load.error_rate")
	if !ok || check.Status != "passed" || check.Before == nil || *check.Before != 1 || check.After == nil || *check.After != 2 || check.BeforeSpread == nil || *check.BeforeSpread != 1 || check.AfterSpread == nil || *check.AfterSpread != 1 || check.SpreadUnit != "pp" {
		t.Fatalf("error rate median and spread = %+v, found=%v", check, ok)
	}
}

func setRepeatedComparisonFailures(receipts []testRunReceipt, failures []int) {
	for index, count := range failures {
		receipts[index].Load.Failures = count
		receipts[index].Load.ErrorRate = float64(count) / float64(receipts[index].Load.Requests)
		receipts[index].Load.Statuses = map[string]int{"200": receipts[index].Load.Requests - count}
		receipts[index].Load.Steps[0].Failures = count
		receipts[index].Load.Steps[0].ErrorRate = receipts[index].Load.ErrorRate
		receipts[index].Load.Steps[0].Statuses = map[string]int{"200": receipts[index].Load.Requests - count}
	}
}

func TestEvaluateTestComparisonRepeatBudgetRequiresMinimumAndRejectsMedianRegression(t *testing.T) {
	budget := repeatedComparisonBudgetForTest()
	before := repeatedComparisonBudgetReceipts([]float64{99, 100}, "passed")
	after := repeatedComparisonBudgetReceipts([]float64{100, 101}, "passed")
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, budget)
	if err != nil {
		t.Fatal(err)
	}
	if check, ok := findComparisonGateCheck(gate, "repeat.count"); !ok || check.Status != "inconclusive" || check.BeforeRuns != 2 || check.AfterRuns != 2 {
		t.Fatalf("minimum attempts check = %+v, found=%v", check, ok)
	}
	if _, ok := findComparisonGateCheck(gate, "load.p95_latency"); ok {
		t.Fatalf("sparse repeat group should not emit metric checks: %+v", gate.Checks)
	}

	budget.RunDurationIncreasePercent = nil
	budget.HTTPDurationIncreasePercent = nil
	budget.LoadP95IncreasePercent = regressionFloat(20)
	before = repeatedComparisonBudgetReceipts([]float64{99, 100, 101}, "passed")
	after = repeatedComparisonBudgetReceipts([]float64{120, 125, 130}, "passed")
	comparison, err = compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err = evaluateTestComparisonBudget(comparison, before, after, budget)
	if err != nil {
		t.Fatal(err)
	}
	check, ok := findComparisonGateCheck(gate, "load.p95_latency")
	if !ok || check.Status != "failed" || check.Before == nil || *check.Before != 100 || check.After == nil || *check.After != 125 {
		t.Fatalf("median regression check = %+v, found=%v", check, ok)
	}
}

func TestEvaluateTestComparisonRepeatBudgetRetainsPerAttemptStatusAndWorkloadChecks(t *testing.T) {
	before := repeatedComparisonBudgetReceipts([]float64{99, 100, 101}, "passed")
	after := repeatedComparisonBudgetReceipts([]float64{99, 100, 101}, "passed")
	after[1].Status = "failed"
	after[1].Load.Workload.SHA256 = strings.Repeat("b", 64)
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, repeatedComparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	failedAttempt := false
	for _, check := range gate.Checks {
		if check.Metric == "run.status" && check.Status == "failed" && strings.HasSuffix(check.Run, "#2") {
			failedAttempt = true
			break
		}
	}
	if !failedAttempt {
		t.Fatalf("per-attempt status was not enforced: %+v", gate.Checks)
	}
	if check, ok := findComparisonGateCheck(gate, "load.workload"); !ok || check.Status != "inconclusive" {
		t.Fatalf("within-group workload mismatch was not rejected: %+v, found=%v", check, ok)
	}
}

func TestReadTestComparisonBudgetValidatesSchemaAndLimits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "budget.yaml")
	valid := `version: 1
runs:
  require_passing: true
  require_same_runs: true
  max_duration_increase_percent: 20
http:
  max_duration_increase_percent: 15
load:
  max_p95_increase_percent: 10
  max_p99_increase_percent: 20
  max_error_rate_increase_percentage_points: 0.5
  min_samples: 50
repeat:
  min_runs: 3
  max_spread_percent: 20
  max_error_rate_spread_percentage_points: 0.5
`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	budget, err := readTestComparisonBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	if !budget.RequirePassing || !budget.RequireSameRuns || budget.LoadMinSamples != 50 || *budget.LoadErrorRateIncreasePoints != .5 || budget.Repeat == nil || budget.Repeat.MinRuns != 3 || budget.Repeat.MaxSpreadPercent != 20 || budget.Repeat.MaxErrorRateSpreadPercentagePoints != .5 {
		t.Fatalf("budget = %+v", budget)
	}
	for name, body := range map[string]string{
		"unknown field": `version: 1
runs: {max_duraton_increase_percent: 10}
`,
		"unsupported version": `version: 2
`,
		"negative limit": `version: 1
load: {max_p95_increase_percent: -1}
`,
		"too large limit": `version: 1
http: {max_duration_increase_percent: 1001}
`,
		"repeat minimum missing": `version: 1
repeat: {max_spread_percent: 20}
`,
		"repeat minimum too high": `version: 1
repeat: {min_runs: 21}
`,
		"negative repeat spread": `version: 1
repeat: {min_runs: 3, max_spread_percent: -1}
`,
		"repeat error spread too large": `version: 1
repeat: {min_runs: 3, max_error_rate_spread_percentage_points: 101}
`,
		"second document": `version: 1
---
version: 1
`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readTestComparisonBudget(path); err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}

func TestCmdTestCompareBudgetWritesHTMLAndReturnsFailure(t *testing.T) {
	dir := t.TempDir()
	beforePath := filepath.Join(dir, "before.json")
	afterPath := filepath.Join(dir, "after.json")
	budgetPath := filepath.Join(dir, "budget.yaml")
	htmlPath := filepath.Join(dir, "comparison.html")
	before := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "passed")}
	after := []testRunReceipt{comparisonBudgetReceipt(100, 150, 0, 100, 1000, 50, "failed")}
	for path, receipts := range map[string][]testRunReceipt{beforePath: before, afterPath: after} {
		body, err := json.Marshal(receipts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(budgetPath, []byte("version: 1\nruns:\n  require_passing: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var stdout, stderr strings.Builder
	osStdout, osStderr, jsonOutput = &stdout, &stderr, false
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	code := cmdTestCompare([]string{beforePath, afterPath, "--budget", budgetPath, "--html", htmlPath})
	if code == 0 {
		t.Fatal("failed run status returned success")
	}
	if !strings.Contains(stdout.String(), "Regression budget gate: FAILED") {
		t.Fatalf("comparison output omitted gate result: %s", stdout.String())
	}
	body, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("comparison HTML not written on gate failure: %v", err)
	}
	if !strings.Contains(string(body), "Regression budget gate: <strong>failed</strong>") || !strings.Contains(string(body), "run.status") {
		t.Fatalf("comparison HTML omitted gate details: %s", body)
	}
}

func TestRepeatedComparisonHTMLShowsMedianSpreadAndRunCount(t *testing.T) {
	before := repeatedComparisonBudgetReceipts([]float64{98, 100, 102}, "passed")
	after := repeatedComparisonBudgetReceipts([]float64{106, 108, 110}, "passed")
	comparison, err := compareTestReports("before.json", "after.json", before, after)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := evaluateTestComparisonBudget(comparison, before, after, repeatedComparisonBudgetForTest())
	if err != nil {
		t.Fatal(err)
	}
	comparison.Gate = &gate
	path := filepath.Join(t.TempDir(), "comparison.html")
	if err := writeTestComparisonHTML(path, comparison); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Earlier spread") || !strings.Contains(string(body), "4.000 % (3 runs)") {
		t.Fatalf("comparison HTML omitted repeated-run spread details: %s", body)
	}
}
