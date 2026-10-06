package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func loadStage(duration string, target int) testLoadStageSpec {
	return testLoadStageSpec{Duration: duration, Target: &target}
}

func TestLoadStagesAndStepBudgetConfiguration(t *testing.T) {
	zero := 0.0
	scenario := testScenario{Requests: []testHTTPRequest{loadTestStep()}, Load: &testLoadSpec{VUs: 1, Pacing: "50ms", Stages: []testLoadStageSpec{loadStage("1s", 3), loadStage("2s", 0)}, Thresholds: testLoadThresholds{Steps: map[string]testLoadStepThresholdSpec{"ping": {P95: "100ms", ErrorRate: &zero}}}}}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil || cfg.Duration != 3*time.Second || cfg.Iterations != 0 || cfg.maxVUs() != 3 || cfg.Pacing != 50*time.Millisecond || cfg.StepThresholds["ping"].P95 != 100*time.Millisecond {
		t.Fatalf("staged config = %+v, %v", cfg, err)
	}
	receipt := newTestLoadEvidence(cfg)
	if receipt.Mode != "stages" || receipt.MaxVUs != 3 || len(receipt.Stages) != 2 || receipt.Stages[1].StartVUs != 3 || receipt.Stages[1].TargetVUs != 0 || receipt.PacingMS != 50 {
		t.Fatalf("staged metadata = %+v", receipt)
	}
	cfg, err = resolveTestLoadConfig(scenario, testLoadOverrides{Duration: "1s", DurationSet: true, Pacing: "0s", PacingSet: true})
	if err != nil || len(cfg.Stages) != 0 || cfg.Pacing != 0 || cfg.Duration != time.Second {
		t.Fatalf("duration/pacing overrides = %+v, %v", cfg, err)
	}
	cfg, err = resolveTestLoadConfig(scenario, testLoadOverrides{Iterations: 10, IterationsSet: true})
	if err != nil || len(cfg.Stages) != 0 || cfg.Duration != 0 || cfg.Iterations != 10 {
		t.Fatalf("iteration override = %+v, %v", cfg, err)
	}
	scenario.Timeout = "1s"
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil {
		t.Fatal("stages exceeding scenario timeout accepted")
	}
	for name, spec := range map[string]testLoadSpec{
		"too many stages":        {Stages: make([]testLoadStageSpec, 21)},
		"missing target":         {Stages: []testLoadStageSpec{{Duration: "1s"}}},
		"negative target":        {Stages: []testLoadStageSpec{loadStage("1s", -1)}},
		"large target":           {Stages: []testLoadStageSpec{loadStage("1s", 51)}},
		"short stage":            {Stages: []testLoadStageSpec{loadStage("1ms", 2)}},
		"invalid stage duration": {Stages: []testLoadStageSpec{loadStage("later", 2)}},
		"long stage":             {Stages: []testLoadStageSpec{loadStage("6m", 2)}},
		"long schedule":          {Stages: []testLoadStageSpec{loadStage("3m", 2), loadStage("3m", 2)}},
		"duration conflict":      {Duration: "1s", Stages: []testLoadStageSpec{loadStage("1s", 2)}},
		"iteration conflict":     {Iterations: 2, Stages: []testLoadStageSpec{loadStage("1s", 2)}},
		"negative pacing":        {Pacing: "-1ms"}, "long pacing": {Pacing: "2m"}, "bad pacing": {Pacing: "later"},
		"empty budget":    {Thresholds: testLoadThresholds{Steps: map[string]testLoadStepThresholdSpec{"ping": {}}}},
		"bad budget":      {Thresholds: testLoadThresholds{Steps: map[string]testLoadStepThresholdSpec{"ping": {P95: "0s"}}}},
		"bad budget name": {Thresholds: testLoadThresholds{Steps: map[string]testLoadStepThresholdSpec{"Ping!": {P95: "1ms"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateTestLoadSpec(&spec); err == nil {
				t.Fatal("invalid staged load spec accepted")
			}
		})
	}
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{PacingSet: true}); err == nil {
		t.Fatal("empty pacing override accepted")
	}
	scenario.Timeout = ""
	scenario.Load.Thresholds.Steps["missing"] = testLoadStepThresholdSpec{P95: "1ms"}
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil {
		t.Fatal("undeclared step budget accepted")
	}
	manifest := filepath.Join(t.TempDir(), "gregale-test.yaml")
	for _, load := range []string{
		"{thresholds: {steps: {missing: {p95: 1ms}}}}",
		"{thresholds: {steps: {ping: {unknown: 1}}}}",
		"{stages: [{duration: 1s, target: 2, unknown: 1}]}",
	} {
		contents := "version: 1\nscenarios:\n  api-smoke:\n    project: my-api\n    requests: [{name: ping, method: GET, path: /ping, expect: {status: 200}}]\n    load: " + load + "\n"
		if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readTestManifest(manifest); err == nil {
			t.Fatalf("invalid nested manifest accepted: %s", load)
		}
	}
}

func TestLoadStageInterpolationBoundaries(t *testing.T) {
	cfg := &testLoadConfig{VUs: 1, Duration: 3 * time.Second, Stages: []testLoadStage{{Duration: time.Second, Target: 3}, {Duration: time.Second, Target: 3}, {Duration: time.Second, Target: 0}}}
	for _, tt := range []struct {
		elapsed       time.Duration
		target, stage int
	}{
		{-time.Second, 1, 0}, {0, 1, 0}, {250 * time.Millisecond, 2, 0},
		{750 * time.Millisecond, 3, 0}, {time.Second, 3, 1}, {1500 * time.Millisecond, 3, 1},
		{2 * time.Second, 3, 2}, {2500 * time.Millisecond, 2, 2}, {2900 * time.Millisecond, 0, 2},
		{3 * time.Second, 0, 3}, {4 * time.Second, 0, 3},
	} {
		target, stage := cfg.targetAt(tt.elapsed)
		if target != tt.target || stage != tt.stage {
			t.Fatalf("target at %s = %d stage %d, want %d stage %d", tt.elapsed, target, stage, tt.target, tt.stage)
		}
	}
}

func TestLoadStepBudgetCatchesHiddenLatencyAndSkippedChecks(t *testing.T) {
	fast := make([]time.Duration, 100)
	for i := range fast {
		fast[i] = time.Millisecond
	}
	collector := &testLoadCollector{steps: []testLoadStepSamples{
		{evidence: testLoadStepEvidence{Name: "fast", testLoadMetrics: testLoadMetrics{Requests: 100, Statuses: map[string]int{"200": 100}}}, samples: fast},
		{evidence: testLoadStepEvidence{Name: "critical", testLoadMetrics: testLoadMetrics{Requests: 1, Statuses: map[string]int{"200": 1}}}, samples: []time.Duration{100 * time.Millisecond}},
	}}
	cfg := &testLoadConfig{VUs: 1, P95: 50 * time.Millisecond, StepThresholds: map[string]testLoadStepThresholdConfig{"critical": {P95: 50 * time.Millisecond}}}
	report := collector.report(cfg, time.Second)
	if !report.Thresholds[1].Passed || report.Steps[1].Thresholds[0].Passed || testLoadThresholdsPassed(report) {
		t.Fatalf("aggregate latency hid failed critical step: %+v", report)
	}
	collector.steps[1].evidence.Requests, collector.steps[1].samples = 0, nil
	report = collector.report(cfg, time.Second)
	if report.Steps[1].Thresholds[0].Passed || testLoadThresholdsPassed(report) {
		t.Fatal("skipped critical step passed its budget")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	zero := 0.0
	cfg = &testLoadConfig{VUs: 1, Iterations: 3, RequestLimit: 100000, ErrorRate: 1, StepThresholds: map[string]testLoadStepThresholdConfig{"ping": {ErrorRate: &zero}, "check": {P95: time.Second}}}
	check := loadTestStep()
	check.Name = "check"
	report, err := runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep(), check}, nil, cfg)
	if err == nil || report.Status != "failed" || !report.Thresholds[0].Passed || report.Steps[0].Thresholds[0].Passed || report.Steps[1].Thresholds[0].Passed || requests.Load() != 3 {
		t.Fatalf("step error/missing-sample budgets = %+v, %v", report, err)
	}
}

func TestLoadRampDownDrainsWholeJourneys(t *testing.T) {
	var arrivals atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			if arrivals.Add(1) == 3 {
				close(started)
			}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		select {
		case <-started:
			_ = waitTestLoad(ctx, 170*time.Millisecond)
		case <-ctx.Done():
		}
		close(release)
	}()
	steps := []testHTTPRequest{loadTestStep(), loadTestStep()}
	steps[0].Name, steps[0].Path, steps[1].Name = "hold", "/hold", "check"
	cfg := &testLoadConfig{VUs: 3, Duration: 150 * time.Millisecond, RequestLimit: 100000, Stages: []testLoadStage{{Duration: 50 * time.Millisecond, Target: 3}, {Duration: 50 * time.Millisecond, Target: 0}, {Duration: 50 * time.Millisecond, Target: 0}}}
	report, err := runTestLoad(ctx, server.URL, "root", nil, steps, nil, cfg)
	if err != nil || report.StopReason != "stages" || report.IterationsCompleted != 3 || report.IterationsStarted != 3 || report.Requests != 6 || report.PeakVUs != 3 || report.IterationsInterrupted != 0 || report.DurationMS < 150 || report.Stages[0].IterationsStarted != 3 || report.Stages[2].IterationsStarted != 0 {
		t.Fatalf("ramp down interrupted or started extra journeys: %+v, %v", report, err)
	}
}

func TestLoadPacingAndCanceledIdleWorkers(t *testing.T) {
	var mu sync.Mutex
	var arrivals []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
	}))
	defer server.Close()
	cfg := &testLoadConfig{VUs: 1, Iterations: 3, RequestLimit: 100000, Pacing: 50 * time.Millisecond}
	report, err := runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err != nil || report.Requests != 3 || report.DurationMS < 100 || report.DurationMS > 1000 {
		t.Fatalf("paced run = %+v, %v", report, err)
	}
	mu.Lock()
	for i := 1; i < len(arrivals); i++ {
		if arrivals[i].Sub(arrivals[i-1]) < 45*time.Millisecond {
			t.Error("pacing interval was not respected")
		}
	}
	mu.Unlock()
	cfg.Iterations, cfg.Pacing = 1, time.Minute
	report, err = runTestLoad(context.Background(), server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err != nil || report.DurationMS > 500 {
		t.Fatalf("pacing delayed final completion: %+v, %v", report, err)
	}
	cfg.Iterations = 3
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	report, err = runTestLoad(ctx, server.URL, "root", nil, []testHTTPRequest{loadTestStep()}, nil, cfg)
	if err == nil || report.StopReason != "scenario_timeout" || report.Requests != 1 || report.DurationMS > 1000 {
		t.Fatalf("idle paced worker ignored cancellation: %+v, %v", report, err)
	}
}

func TestLoadStagesCLIProgressBudgetsAndJUnit(t *testing.T) {
	dir := t.TempDir()
	// PeakVUs counts overlapping journeys. Fast requests can fall between
	// another user's pacing intervals, so hold the first request until the
	// ramp starts a second journey rather than relying on timer alignment.
	var arrivals atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if arrivals.Add(1) == 2 {
			close(started)
		}
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-started:
		case <-r.Context().Done():
			return
		case <-timer.C:
			http.Error(w, "load ramp did not start a second journey", http.StatusServiceUnavailable)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}))
	defer server.Close()
	manifest := filepath.Join(dir, "gregale-test.yaml")
	contents := `version: 1
scenarios:
  api-staged:
    project: my-api
    load:
      vus: 1
      pacing: 20ms
      stages: [{duration: 1s, target: 3}, {duration: 1s, target: 0}]
      thresholds:
        steps:
          ping: {p95: 1ns, error_rate: 0}
    setup:
      - [sh, -c, 'test "$GREGALE_TEST_LOAD_MODE" = stages && test "$GREGALE_TEST_LOAD_MAX_VUS" = 3 && test "$GREGALE_TEST_LOAD_PACING" = 20ms']
    cleanup:
      - [sh, -c, 'test -n "$GREGALE_TEST_LOAD_JSON" && printf cleaned > cleaned']
    requests:
      - {name: ping, method: GET, path: /ping, expect: {status: 200}}
`
	if err := os.WriteFile(manifest, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	oldJSON, oldOut, oldErr := jsonOutput, osStdout, osStderr
	var stdout, stderr bytes.Buffer
	jsonOutput, osStdout, osStderr = true, &stdout, &stderr
	t.Cleanup(func() { jsonOutput, osStdout, osStderr = oldJSON, oldOut, oldErr })
	args := []string{"--scenario", "api-staged", "--engine", "local", "--load", "--manifest", manifest}
	if code := cmdTest(append(append([]string{}, args...), "--validate")); code != 0 {
		t.Fatalf("staged validation failed: %d %s", code, stderr.String())
	}
	stdout.Reset()
	junit := filepath.Join(dir, "results.xml")
	args = append(args, "--base-url", server.URL, "--progress", "--junit", junit)
	if code := cmdTest(args); code != 1 {
		t.Fatalf("step latency budget did not fail CLI: %d %s", code, stderr.String())
	}
	var receipts []testRunReceipt
	if err := json.Unmarshal(stdout.Bytes(), &receipts); err != nil || len(receipts) != 1 {
		t.Fatalf("progress contaminated JSON: %s, %v", stdout.String(), err)
	}
	load := receipts[0].Load
	if load.Mode != "stages" || load.PeakVUs < 2 || load.MaxVUs != 3 || load.IterationsStarted != load.IterationsCompleted || len(load.Stages) != 2 || load.Stages[0].IterationsStarted+load.Stages[1].IterationsStarted != load.IterationsStarted || !load.Thresholds[0].Passed || load.Steps[0].Thresholds[1].Passed {
		t.Fatalf("staged receipt = %+v", load)
	}
	if !strings.Contains(stderr.String(), "load 1.") || !strings.Contains(stderr.String(), "target") || strings.Contains(stdout.String(), "journeys completed") {
		t.Fatalf("progress output misplaced: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	var human bytes.Buffer
	printTestLoadSummary(&human, load)
	if !strings.Contains(human.String(), "ping.p95_ms") || !strings.Contains(human.String(), "(failed)") {
		t.Fatalf("failed budget missing from human summary: %s", human.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "cleaned")); err != nil {
		t.Fatal("staged cleanup missing", err)
	}
	body, err := os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].SystemOut, "thresholds") {
		t.Fatalf("step-budget JUnit failure missing: %+v, %v", suite, err)
	}
}
