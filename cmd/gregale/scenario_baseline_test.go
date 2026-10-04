package main

import (
	"encoding/json"
	"encoding/xml"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func regressionFloat(value float64) *float64 { return &value }
func regressionInt(value int) *int           { return &value }

func baselineTestPrepared(t *testing.T) testPreparedScenario {
	t.Helper()
	scenario := suiteTestScenario("/read?customer=${data.customer}")
	scenario.Checks[0].Headers = map[string]string{"Authorization": "private-token", "X-Customer": "${data.customer}"}
	scenario.Load = &testLoadSpec{Iterations: 20, Workload: "customers-v1"}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	return testPreparedScenario{Name: "api", Engine: "local", Scenario: scenario, Load: cfg,
		Cases: []testDataCase{{Name: "row-1", Values: map[string]any{"customer": "private-customer"}}}}
}

func baselineTestMetrics(samples int, p95 float64) testLoadMetrics {
	return testLoadMetrics{Requests: samples, Statuses: map[string]int{"200": samples}, Latency: testLoadLatency{
		Samples: samples, MinMS: p95, MeanMS: p95, P50MS: p95, P95MS: p95, P99MS: p95, MaxMS: p95,
	}}
}

func baselineTestReceipts(t *testing.T, prepared []testPreparedScenario, repeat int) []testRunReceipt {
	t.Helper()
	plans := prepareTestRunPlans(prepared, repeat)
	if _, err := prepareTestBaselines("", plans); err != nil {
		t.Fatal(err)
	}
	var receipts []testRunReceipt
	for _, plan := range plans {
		cfg := plan.Prepared.Load
		load := newTestLoadEvidence(cfg)
		load.Status, load.Workload = "passed", plan.Workload
		samples := cfg.Iterations
		if samples == 0 {
			samples = 30
		}
		if load.Arrival != nil {
			samples = load.Arrival.Planned
			load.Arrival.Scheduled = samples
		}
		load.IterationsStarted, load.IterationsCompleted = samples, samples
		load.DurationMS = 1000
		steps := append(append([]testHTTPRequest{}, plan.Prepared.Scenario.Requests...), plan.Prepared.Scenario.Checks...)
		for _, step := range steps {
			load.Steps = append(load.Steps, testLoadStepEvidence{Name: step.Name, Method: step.Method, Path: strings.SplitN(step.Path, "?", 2)[0], testLoadMetrics: baselineTestMetrics(samples, 1)})
		}
		load.testLoadMetrics = baselineTestMetrics(samples*len(steps), 1)
		receipts = append(receipts, testRunReceipt{Scenario: plan.Prepared.Name, Engine: "local", Profile: "local", Case: plan.Case.Name, Attempt: plan.Attempt, Status: "passed", Load: load})
	}
	return receipts
}

func TestBaselineBudgetValidationAndInheritance(t *testing.T) {
	defaultCfg := resolveTestRegressionConfig(nil)
	if defaultCfg.P95Percent != 10 || defaultCfg.ErrorRateIncrease != 0 || defaultCfg.MinSamples != 20 {
		t.Fatalf("default budgets = %+v", defaultCfg)
	}
	spec := &testRegressionSpec{testRegressionBudget: testRegressionBudget{P95Percent: regressionFloat(30), MinSamples: regressionInt(50)},
		Steps: map[string]testRegressionBudget{"read": {P95Percent: regressionFloat(0), ErrorRateIncrease: regressionFloat(.01)}}}
	if err := validateTestRegressionSpec(spec); err != nil {
		t.Fatal(err)
	}
	cfg := resolveTestRegressionConfig(spec)
	if got := cfg.stepLimits("read"); got.P95Percent != 0 || got.ErrorRateIncrease != .01 || got.MinSamples != 50 {
		t.Fatalf("step inheritance = %+v", got)
	}
	if cfg.stepLimits("other") != cfg.testRegressionLimits {
		t.Fatal("unconfigured step did not inherit aggregate budgets")
	}
	for name, budget := range map[string]testRegressionBudget{
		"negative percent": {P95Percent: regressionFloat(-1)}, "large percent": {P95Percent: regressionFloat(1001)},
		"NaN percent": {P95Percent: regressionFloat(math.NaN())}, "infinite percent": {P95Percent: regressionFloat(math.Inf(1))},
		"negative rate": {ErrorRateIncrease: regressionFloat(-.01)}, "large rate": {ErrorRateIncrease: regressionFloat(1.01)},
		"NaN rate": {ErrorRateIncrease: regressionFloat(math.NaN())}, "infinite rate": {ErrorRateIncrease: regressionFloat(math.Inf(1))},
		"zero samples": {MinSamples: regressionInt(0)}, "many samples": {MinSamples: regressionInt(100001)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateTestRegressionSpec(&testRegressionSpec{testRegressionBudget: budget}); err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
	p := baselineTestPrepared(t)
	p.Scenario.Load.Regression = &testRegressionSpec{Steps: map[string]testRegressionBudget{"missing": {MinSamples: regressionInt(1)}}}
	if _, err := resolveTestLoadConfig(p.Scenario, testLoadOverrides{}); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("unknown step accepted: %v", err)
	}
	p.Scenario.Load.Regression = &testRegressionSpec{Steps: map[string]testRegressionBudget{"read": {}}}
	if err := validateTestLoadSpec(p.Scenario.Load); err == nil {
		t.Fatal("empty step budget accepted")
	}
}

func TestBaselineBudgetsFromManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gregale-test.yaml")
	writeSuiteTestFile(t, path, `version: 1
scenarios:
  api:
    project: api
    requests:
      - {name: read, method: GET, path: /read, expect: {status: 200}}
    load:
      workload: customers-v1
      iterations: 100
      regression:
        p95_percent: 15
        error_rate_increase: 0.005
        min_samples: 50
        steps:
          read: {p95_percent: 0}
`)
	scenarios, _, err := readTestManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := resolveTestLoadConfig(scenarios["api"], testLoadOverrides{})
	if err != nil || cfg.Regression.P95Percent != 15 || cfg.Regression.MinSamples != 50 || cfg.Regression.ErrorRateIncrease != .005 {
		t.Fatalf("manifest aggregate budgets = %+v, %v", cfg, err)
	}
	if step := cfg.Regression.stepLimits("read"); step.P95Percent != 0 || step.MinSamples != 50 || step.ErrorRateIncrease != .005 {
		t.Fatalf("manifest step budgets = %+v", step)
	}
}

func TestBaselineBudgetsBoundariesSamplesAndZeroLatency(t *testing.T) {
	base := &testLoadEvidence{testLoadMetrics: baselineTestMetrics(20, 100), Steps: []testLoadStepEvidence{{Name: "read", testLoadMetrics: baselineTestMetrics(20, 100)}}}
	cfg := resolveTestRegressionConfig(nil)
	for name, after := range map[string]struct {
		Samples int
		P95     float64
		Rate    float64
		Passed  bool
	}{
		"boundary": {20, 100 * (1 + .1), 0, true}, "just over": {20, math.Nextafter(100*(1+.1), math.Inf(1)), 0, false},
		"improved": {30, 80, 0, true}, "few samples": {19, 100, 0, false}, "new failures": {20, 100, .05, false},
	} {
		t.Run(name, func(t *testing.T) {
			current := &testLoadEvidence{testLoadMetrics: baselineTestMetrics(after.Samples, after.P95), Steps: []testLoadStepEvidence{{Name: "read", testLoadMetrics: baselineTestMetrics(after.Samples, after.P95)}}}
			current.ErrorRate, current.Steps[0].ErrorRate = after.Rate, after.Rate
			result := compareTestBaseline(base, current, cfg, "digest")
			if (result.Status == "passed") != after.Passed || len(result.Checks) != 6 {
				t.Fatalf("comparison = %+v", result)
			}
			if !after.Passed && !strings.Contains(testBaselineFailure(result), "step read") {
				t.Fatal("failure omitted the affected step")
			}
		})
	}
	current := &testLoadEvidence{testLoadMetrics: baselineTestMetrics(20, 100), Steps: []testLoadStepEvidence{{Name: "read", testLoadMetrics: baselineTestMetrics(20, 150)}}}
	if result := compareTestBaseline(base, current, cfg, "digest"); result.Status != "failed" || !result.Checks[1].Passed || result.Checks[4].Passed {
		t.Fatalf("aggregate hid a slower step: %+v", result)
	}
	cfg.Steps = map[string]testRegressionLimits{"read": {P95Percent: 50, MinSamples: 20, ErrorRateIncrease: .05}}
	current.Steps[0].ErrorRate = .05
	if result := compareTestBaseline(base, current, cfg, "digest"); result.Status != "passed" {
		t.Fatalf("step override rejected its boundary: %+v", result)
	}
	base.Latency, base.Steps[0].Latency = baselineTestMetrics(20, 0).Latency, baselineTestMetrics(20, 0).Latency
	current.Latency, current.Steps[0].Latency = baselineTestMetrics(20, 1).Latency, baselineTestMetrics(20, 1).Latency
	result := compareTestBaseline(base, current, cfg, "digest")
	if result.Status != "failed" {
		t.Fatal("positive latency passed a zero baseline")
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatalf("zero baseline generated nonfinite JSON: %v", err)
	}
}

func TestBaselineWorkloadIdentity(t *testing.T) {
	original := baselineTestPrepared(t)
	identity := func(p testPreparedScenario) string {
		t.Helper()
		plans := prepareTestRunPlans([]testPreparedScenario{p}, 1)
		workload, err := testPlanWorkload(plans[0])
		if err != nil {
			t.Fatal(err)
		}
		return workload.SHA256
	}
	want := identity(original)
	for name, mutate := range map[string]struct {
		Change func(*testPreparedScenario)
		Match  bool
	}{
		"iterations":               {func(p *testPreparedScenario) { p.Load.Iterations++ }, false},
		"users":                    {func(p *testPreparedScenario) { p.Load.VUs++ }, false},
		"pacing":                   {func(p *testPreparedScenario) { p.Load.Pacing = time.Millisecond }, false},
		"duration":                 {func(p *testPreparedScenario) { p.Load.Duration = time.Second }, false},
		"arrival rate":             {func(p *testPreparedScenario) { p.Load.Rate = 20 }, false},
		"stage target":             {func(p *testPreparedScenario) { p.Load.Stages = []testLoadStage{{Duration: time.Second, Target: 2}} }, false},
		"query":                    {func(p *testPreparedScenario) { p.Scenario.Checks[0].Path += "&large=true" }, false},
		"body":                     {func(p *testPreparedScenario) { p.Scenario.Checks[0].JSON = map[string]any{"size": 100} }, false},
		"expectation":              {func(p *testPreparedScenario) { p.Scenario.Checks[0].Expect.Status = 201 }, false},
		"data type":                {func(p *testPreparedScenario) { p.Cases[0].Values["customer"] = true }, false},
		"data field":               {func(p *testPreparedScenario) { p.Cases[0].Values["another"] = "value" }, false},
		"label":                    {func(p *testPreparedScenario) { p.Scenario.Load.Workload = "customers-v2" }, false},
		"managed app":              {func(p *testPreparedScenario) { p.Scenario.Local = &testLocalAppSpec{} }, false},
		"timeout":                  {func(p *testPreparedScenario) { p.Scenario.Timeout = "2m" }, false},
		"header dependency":        {func(p *testPreparedScenario) { p.Scenario.Checks[0].Headers["X-Customer"] = "${run.id}" }, false},
		"header token":             {func(p *testPreparedScenario) { p.Scenario.Checks[0].Headers["Authorization"] = "rotated-token" }, true},
		"data value":               {func(p *testPreparedScenario) { p.Cases[0].Values["customer"] = "rotated-customer" }, true},
		"app revision":             {func(p *testPreparedScenario) { p.Scenario.Source = "new-source" }, true},
		"URL port":                 {func(p *testPreparedScenario) { p.BaseURL = "http://localhost:9876" }, true},
		"budget":                   {func(p *testPreparedScenario) { p.Load.P95 = time.Second; p.Load.Regression.P95Percent = 30 }, true},
		"explicit default timeout": {func(p *testPreparedScenario) { p.Scenario.Timeout = "900s" }, true},
	} {
		t.Run(name, func(t *testing.T) {
			p := baselineTestPrepared(t)
			mutate.Change(&p)
			if got := identity(p); (got == want) != mutate.Match {
				t.Fatalf("workload match=%t, want %t", got == want, mutate.Match)
			}
		})
	}
}

func TestBaselineRejectsInvalidRunsBeforeExecution(t *testing.T) {
	for name, mutate := range map[string]func(*testRunReceipt){
		"failed":           func(r *testRunReceipt) { r.Status = "failed" },
		"cleanup failure":  func(r *testRunReceipt) { r.CleanupError = "cleanup failed" },
		"missing metadata": func(r *testRunReceipt) { r.Load.Workload = nil },
		"metadata version": func(r *testRunReceipt) { r.Load.Workload.Version = 2 },
		"wrong workload":   func(r *testRunReceipt) { r.Load.Workload.SHA256 = "other" },
		"wrong schedule":   func(r *testRunReceipt) { r.Load.VUs++ },
		"missing case":     func(r *testRunReceipt) { r.Case = "row-2" },
		"wrong attempt":    func(r *testRunReceipt) { r.Attempt = 2 },
		"unfinished":       func(r *testRunReceipt) { r.Load.IterationsCompleted-- },
		"interrupted":      func(r *testRunReceipt) { r.Load.IterationsInterrupted = 1 },
		"missing step":     func(r *testRunReceipt) { r.Load.Steps = nil },
		"wrong step":       func(r *testRunReceipt) { r.Load.Steps[0].Name = "other" },
		"wrong total":      func(r *testRunReceipt) { r.Load.testLoadMetrics = baselineTestMetrics(21, 1) },
		"few samples": func(r *testRunReceipt) {
			r.Load.IterationLimit = 0
			r.Load.testLoadMetrics, r.Load.Steps[0].testLoadMetrics = baselineTestMetrics(19, 1), baselineTestMetrics(19, 1)
		},
		"inconsistent samples": func(r *testRunReceipt) { r.Load.Latency.Samples-- },
		"inconsistent rate":    func(r *testRunReceipt) { r.Load.ErrorRate = .05 },
		"negative latency":     func(r *testRunReceipt) { r.Load.Latency.MinMS = -1 },
		"reversed percentiles": func(r *testRunReceipt) { r.Load.Latency.P50MS = 2 },
		"huge latency":         func(r *testRunReceipt) { r.Load.Latency.MaxMS = math.MaxFloat64 },
	} {
		t.Run(name, func(t *testing.T) {
			p := baselineTestPrepared(t)
			receipts := baselineTestReceipts(t, []testPreparedScenario{p}, 1)
			mutate(&receipts[0])
			path := filepath.Join(t.TempDir(), "baseline.json")
			if err := writeTestReport(path, receipts); err != nil {
				t.Fatal(err)
			}
			plans := prepareTestRunPlans([]testPreparedScenario{p}, 1)
			if _, err := prepareTestBaselines(path, plans); err == nil {
				t.Fatal("invalid baseline accepted")
			}
		})
	}
	metrics := baselineTestMetrics(20, 1)
	metrics.Latency.P95MS = math.NaN()
	if err := testBenchmarkMetricsValid(metrics); err == nil {
		t.Fatal("nonfinite latency accepted")
	}
	samples := make([]time.Duration, testLoadMaxRequests)
	for i := range samples {
		samples[i] = 333 * time.Nanosecond
	}
	metrics = baselineTestMetrics(len(samples), 0)
	metrics.Latency = testLoadLatencySummary(samples)
	if err := testBenchmarkMetricsValid(metrics); err != nil {
		t.Fatalf("collector rounding invalidated a valid benchmark: %v", err)
	}
}

func TestBaselineReportParsingAndOutputProtection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	p := baselineTestPrepared(t)
	receipts := baselineTestReceipts(t, []testPreparedScenario{p}, 1)
	for name, body := range map[string]string{"empty": "[]", "null": "null", "object": "{}", "trailing JSON": "[] {}", "malformed": "[", "duplicate": ""} {
		t.Run(name, func(t *testing.T) {
			if name == "duplicate" {
				if err := writeTestReport(path, append(receipts, receipts...)); err != nil {
					t.Fatal(err)
				}
			} else {
				writeSuiteTestFile(t, path, body)
			}
			if _, err := prepareTestBaselines(path, prepareTestRunPlans([]testPreparedScenario{p}, 1)); err == nil {
				t.Fatal("invalid report accepted")
			}
		})
	}
	if err := writeTestReport(path, receipts); err != nil {
		t.Fatal(err)
	}
	hardlink, symlink := filepath.Join(dir, "hardlink.json"), filepath.Join(dir, "symlink.json")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{path, hardlink, symlink} {
		if _, err := prepareTestBaselines(path, prepareTestRunPlans([]testPreparedScenario{p}, 1), output); err == nil || !strings.Contains(err.Error(), "overwrite") {
			t.Fatalf("baseline overwrite allowed for %s: %v", output, err)
		}
	}
	if _, digest, err := readTestBaselineReport(path, nil); err != nil || len(digest) != 64 {
		t.Fatalf("report provenance = %s, %v", digest, err)
	}
}

func TestBaselineSampleGateAndFailedCurrentRun(t *testing.T) {
	p := baselineTestPrepared(t)
	p.Scenario.Load.Iterations, p.Scenario.Load.Duration = 0, "1s"
	var err error
	p.Load, err = resolveTestLoadConfig(p.Scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	baseline := baselineTestReceipts(t, []testPreparedScenario{p}, 1)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := writeTestReport(path, baseline); err != nil {
		t.Fatal(err)
	}
	plans := prepareTestRunPlans([]testPreparedScenario{p}, 1)
	digest, err := prepareTestBaselines(path, plans)
	if err != nil {
		t.Fatal(err)
	}
	current := baselineTestReceipts(t, []testPreparedScenario{p}, 1)[0]
	current.Load.IterationsStarted, current.Load.IterationsCompleted = 10, 10
	current.Load.testLoadMetrics, current.Load.Steps[0].testLoadMetrics = baselineTestMetrics(10, 1), baselineTestMetrics(10, 1)
	applyTestBaseline(plans[0], &current, digest)
	if current.Status != "failed" || current.Baseline.Status != "failed" || current.Baseline.Checks[0].Passed || !strings.Contains(current.Error, "need at least 20") {
		t.Fatalf("sparse run passed: %+v", current)
	}
	current.Status, current.Error, current.CleanupError = "failed", "assertion failed", "cleanup failed"
	applyTestBaseline(plans[0], &current, digest)
	if current.Baseline.Status != "not_compared" || current.Error != "assertion failed" || current.CleanupError != "cleanup failed" {
		t.Fatalf("failed execution lost its error: %+v", current)
	}
	baseline[0].Load.IterationsStarted, baseline[0].Load.IterationsCompleted = 19, 19
	baseline[0].Load.testLoadMetrics, baseline[0].Load.Steps[0].testLoadMetrics = baselineTestMetrics(19, 1), baselineTestMetrics(19, 1)
	if err := writeTestReport(path, baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestBaselines(path, prepareTestRunPlans([]testPreparedScenario{p}, 1)); err == nil || !strings.Contains(err.Error(), "needs at least 20") {
		t.Fatalf("sparse baseline accepted: %v", err)
	}
}

func TestBaselineArrivalScheduleMustBeDelivered(t *testing.T) {
	p := baselineTestPrepared(t)
	p.Scenario.Load.Iterations, p.Scenario.Load.Duration, p.Scenario.Load.Rate = 0, "1s", 20
	var err error
	p.Load, err = resolveTestLoadConfig(p.Scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	baseline := baselineTestReceipts(t, []testPreparedScenario{p}, 1)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := writeTestReport(path, baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestBaselines(path, prepareTestRunPlans([]testPreparedScenario{p}, 1)); err != nil {
		t.Fatalf("complete arrival baseline rejected: %v", err)
	}
	baseline[0].Load.Arrival.Dropped = 1
	if err := writeTestReport(path, baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestBaselines(path, prepareTestRunPlans([]testPreparedScenario{p}, 1)); err == nil || !strings.Contains(err.Error(), "dropped arrivals") {
		t.Fatalf("dropped baseline accepted: %v", err)
	}
}

func TestBaselineSuiteFailsRegressionAfterCleanupAndSkipsRemainingRuns(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/first" {
			t.Error("fail-fast allowed a later member to run")
		}
		time.Sleep(15 * time.Millisecond)
	}))
	defer server.Close()
	first, second := suiteTestScenario("/first"), suiteTestScenario("/second")
	for _, s := range []*testScenario{&first, &second} {
		s.Load = &testLoadSpec{Iterations: 20}
		s.Cleanup = [][]string{{"sh", "-c", "echo cleaned >> cleanup.log"}}
	}
	manifest := writeSuiteTestManifest(t, dir, testManifest{Version: 1, Scenarios: map[string]testScenario{"first": first, "second": second},
		Suites: map[string]testSuite{"benchmark": {Scenarios: []testSuiteMember{{Scenario: "first"}, {Scenario: "second"}}}}})
	prepared, err := prepareTestSuite(manifest, "benchmark", testSuiteOptions{Engine: "local", EngineSet: true, Load: true, Repeat: 2, BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	baseline, report, junit := filepath.Join(dir, "baseline.json"), filepath.Join(dir, "current.json"), filepath.Join(dir, "current.xml")
	if err := writeTestReport(baseline, baselineTestReceipts(t, prepared, 2)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	if code := cmdTest([]string{"--suite", "benchmark", "--engine", "local", "--base-url", server.URL, "--manifest", manifest,
		"--load", "--repeat", "2", "--baseline", baseline, "--report", report, "--junit", junit, "--fail-fast"}); code != 1 {
		t.Fatalf("regression exit %d: %s", code, stderr)
	}
	var results []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil || len(results) != 4 {
		t.Fatalf("report = %s, %v", stdout, err)
	}
	if results[0].Status != "failed" || results[0].Load.Status != "passed" || results[0].Baseline.Status != "failed" || !strings.Contains(results[0].Error, "step read p95_ms") {
		t.Fatalf("regression evidence = %+v", results[0])
	}
	for _, skipped := range results[1:] {
		if skipped.Status != "skipped" || skipped.Baseline.Status != "not_compared" || skipped.Load.Workload == nil || skipped.RunID != "" {
			t.Fatalf("remaining run = %+v", skipped)
		}
	}
	if calls.Load() != 20 {
		t.Fatalf("unexpected requests: %d", calls.Load())
	}
	if body, err := os.ReadFile(filepath.Join(dir, "cleanup.log")); err != nil || string(body) != "cleaned\n" {
		t.Fatalf("cleanup = %s, %v", body, err)
	}
	after, err := os.ReadFile(baseline)
	if err != nil || string(before) != string(after) {
		t.Fatal("baseline report changed")
	}
	body, err := os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	var xmlReport testJUnitSuite
	if err := xml.Unmarshal(body, &xmlReport); err != nil || xmlReport.Tests != 4 || xmlReport.Failures != 1 || xmlReport.Skipped != 3 || !strings.Contains(xmlReport.Cases[0].SystemOut, "report_sha256") {
		t.Fatalf("JUnit = %+v, %v", xmlReport, err)
	}
}

func TestBaselineRecordAndReuseFromCLI(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr := captureSuiteTestOutput(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(10 * time.Millisecond) }))
	defer server.Close()
	scenario := suiteTestScenario("/read")
	scenario.Checks[0].Headers = map[string]string{"Authorization": "private-token"}
	scenario.Load = &testLoadSpec{Iterations: 20, Regression: &testRegressionSpec{testRegressionBudget: testRegressionBudget{P95Percent: regressionFloat(1000)}}}
	manifest := writeSuiteTestManifest(t, dir, testManifest{Version: 1, Scenarios: map[string]testScenario{"api": scenario}})
	baseline := filepath.Join(dir, "baseline.json")
	args := []string{"--scenario", "api", "--engine", "local", "--base-url", server.URL, "--manifest", manifest, "--load"}
	if code := cmdTest(append(append([]string{}, args...), "--report", baseline)); code != 0 {
		t.Fatalf("record exit %d: %s", code, stderr)
	}
	if strings.Contains(stdout.String(), "private-token") {
		t.Fatal("workload metadata exposed credentials")
	}
	stdout.Reset()
	if code := cmdTest(append(append([]string{}, args...), "--baseline", baseline)); code != 0 {
		t.Fatalf("reuse exit %d: %s / %s", code, stdout, stderr)
	}
	var results []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil || len(results) != 1 || results[0].Baseline.Status != "passed" || len(results[0].Baseline.Checks) != 6 {
		t.Fatalf("comparison = %s, %v", stdout, err)
	}
}

func TestBaselineCLIRejectsMismatchBeforeSetup(t *testing.T) {
	dir := t.TempDir()
	stdout, _ := captureSuiteTestOutput(t)
	p := baselineTestPrepared(t)
	baseline := filepath.Join(dir, "baseline.json")
	if err := writeTestReport(baseline, baselineTestReceipts(t, []testPreparedScenario{p}, 1)); err != nil {
		t.Fatal(err)
	}
	scenario := suiteTestScenario("/different")
	scenario.Load = &testLoadSpec{Iterations: 20}
	scenario.Setup = [][]string{{"sh", "-c", "touch executed"}}
	manifest := writeSuiteTestManifest(t, dir, testManifest{Version: 1, Scenarios: map[string]testScenario{"api": scenario}})
	args := []string{"--scenario", "api", "--engine", "local", "--base-url", "http://localhost:3000", "--manifest", manifest, "--load", "--baseline", baseline}
	for _, extra := range [][]string{nil, {"--validate"}, {"--load=false"}} {
		code, problem := runWithStderr(t, func() int { return cmdTest(append(append([]string{}, args...), extra...)) })
		if code != 1 || strings.Contains(problem, "transport_error") || stdout.Len() != 0 {
			t.Fatalf("invalid baseline options exit %d: %s / %s", code, stdout, problem)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
		t.Fatalf("mismatched workload ran setup: %v", err)
	}
}
