package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"github.com/onebox-faas/faas/pkg/api"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoveryRelativeValidation(t *testing.T) {
	twenty := 20.0
	phase := testLoadChaosPhaseSpec{Name: "recovery", Recovery: &testLoadRecoverySpec{Within: "2s", Sustain: "1s", Window: "1s", Thresholds: testLoadStepThresholdSpec{MinSamples: 5}, Relative: &testLoadRecoveryRelativeSpec{P95IncreasePercent: &twenty, BaselineMinSamples: 20}}}
	if err := validateTestLoadRecovery(phase, true, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	cfg := resolveTestLoadRecovery(phase.Recovery)
	if cfg.Relative == nil || *cfg.Relative.P95IncreasePercent != 20 {
		t.Fatal("relative config lost")
	}
	for _, bad := range []testLoadRecoveryRelativeSpec{{BaselineMinSamples: 0, P95IncreasePercent: &twenty}, {BaselineMinSamples: 2}, {BaselineMinSamples: testLoadMaxRequests + 1, P95IncreasePercent: &twenty}} {
		if err := validateTestLoadRecoveryRelative(&bad); err == nil {
			t.Fatalf("invalid relative spec accepted: %+v", bad)
		}
	}
	for _, bad := range []float64{math.NaN(), math.Inf(1), -1, 1001} {
		spec := testLoadRecoveryRelativeSpec{BaselineMinSamples: 2, P95IncreasePercent: &bad}
		if err := validateTestLoadRecoveryRelative(&spec); err == nil {
			t.Fatal("invalid percent accepted")
		}
	}
	zero := 0.0
	spec := testLoadRecoveryRelativeSpec{BaselineMinSamples: 2, P95IncreasePercent: &zero}
	if err := validateTestLoadRecoveryRelative(&spec); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryRelativeBaselineAndRollingBudgets(t *testing.T) {
	start := time.Now()
	baselineWindow := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(time.Second)}
	samples := []testLoadRecoverySample{}
	for i := 0; i < 10; i++ {
		samples = append(samples, testLoadRecoverySample{StartedAt: start.Add(time.Duration(i) * 50 * time.Millisecond), Duration: 100 * time.Millisecond, Passed: true})
	}
	// This request finishes after the fault starts and must not inflate p95.
	samples = append(samples, testLoadRecoverySample{StartedAt: start.Add(900 * time.Millisecond), Duration: time.Second, Passed: true})
	baseline := testLoadRecoveryBaselineFor("healthy", baselineWindow, samples, 10)
	if baseline.Requests != 10 || baseline.Latency.P95MS != 100 {
		t.Fatalf("fault contaminated baseline: %+v", baseline)
	}
	twenty := 20.0
	cfg := &testLoadRecoveryConfig{Within: 2 * time.Second, Sustain: time.Second, Window: time.Second, Thresholds: testLoadStepThresholdConfig{MinSamples: 5}, Relative: &testLoadRecoveryRelativeSpec{P95IncreasePercent: &twenty, BaselineMinSamples: 10}}
	recoveryStart := start.Add(2 * time.Second)
	recoveryWindow := testLoadPhaseWindow{StartedAt: recoveryStart, FinishedAt: recoveryStart.Add(3 * time.Second)}
	var recovery []testLoadRecoverySample
	for i := 0; i < 30; i++ {
		recovery = append(recovery, testLoadRecoverySample{StartedAt: recoveryStart.Add(time.Duration(i) * 100 * time.Millisecond), Duration: 120 * time.Millisecond, Passed: true})
	}
	report := evaluateTestLoadRecovery(cfg, recoveryWindow, recovery, baseline)
	if !report.Passed || len(report.Relative) != 1 || report.Relative[0].Limit != 120 || report.Relative[0].Actual != 120 {
		t.Fatalf("relative limit rejected: %+v", report)
	}
	cfg.Thresholds.P95 = 110 * time.Millisecond
	if evaluateTestLoadRecovery(cfg, recoveryWindow, recovery, baseline).Passed {
		t.Fatal("relative budget bypassed absolute budget")
	}
	cfg.Thresholds.P95 = 0
	for i := range recovery {
		recovery[i].Duration = 121 * time.Millisecond
	}
	report = evaluateTestLoadRecovery(cfg, recoveryWindow, recovery, baseline)
	if report.Passed || !strings.Contains(report.Error, "relative p95_ms") {
		t.Fatalf("relative breach lost: %+v", report)
	}
	cfg.Relative.BaselineMinSamples = 11
	if evaluateTestLoadRecovery(cfg, recoveryWindow, recovery, baseline).Passed {
		t.Fatal("weak baseline passed")
	}
	if evaluateTestLoadRecovery(cfg, recoveryWindow, recovery).Passed {
		t.Fatal("missing baseline passed")
	}
	cfg.Relative.BaselineMinSamples = 10
	baseline.Latency.P95MS = 0
	if evaluateTestLoadRecovery(cfg, recoveryWindow, recovery, baseline).Passed {
		t.Fatal("zero latency baseline passed")
	}
}

func TestRecoveryRelativeRateLimits(t *testing.T) {
	rate := .1
	zero := 0.0
	baseline := testLoadMetrics{Requests: 10, ErrorRate: .2, SuccessRate: .8, Latency: testLoadLatency{P95MS: 10, P99MS: 20}}
	actual := testLoadMetrics{Requests: 10, ErrorRate: .25, SuccessRate: .75, Latency: testLoadLatency{P95MS: 10, P99MS: 20}}
	spec := &testLoadRecoveryRelativeSpec{P95IncreasePercent: &zero, P99IncreasePercent: &zero, ErrorRateIncrease: &rate, SuccessRateDrop: &rate, BaselineMinSamples: 10}
	checks := testLoadRecoveryRelativeChecks(spec, baseline, actual)
	if len(checks) != 4 {
		t.Fatal(checks)
	}
	for _, check := range checks {
		if !check.Passed {
			t.Fatal(check)
		}
	}
	actual.ErrorRate = .3
	actual.SuccessRate = .7
	checks = testLoadRecoveryRelativeChecks(spec, baseline, actual)
	if !checks[2].Passed || !checks[3].Passed {
		t.Fatal("exact rate boundary rejected", checks)
	}
	actual.ErrorRate = .4
	actual.SuccessRate = .6
	checks = testLoadRecoveryRelativeChecks(spec, baseline, actual)
	if checks[2].Passed || checks[3].Passed {
		t.Fatal("rate degradation passed")
	}
	spec.ErrorRateIncrease = &zero
	spec.SuccessRateDrop = &zero
	if testLoadRecoveryRelativeChecks(spec, baseline, actual)[2].Passed {
		t.Fatal("explicit zero ignored")
	}
}

func TestRecoveryRelativeCollectorAndReports(t *testing.T) {
	start := time.Now().Add(-5 * time.Second)
	clock := newTestLoadPhaseClock(3)
	clock.start(start)
	clock.transition(1, start.Add(time.Second))
	clock.transition(2, start.Add(2*time.Second))
	clock.finish(start.Add(5 * time.Second))
	twenty := 20.0
	cfg := &testLoadConfig{ErrorRate: 1, ChaosPhaseClock: clock, ChaosSchedule: []testLoadChaosPhase{{Name: "healthy", Duration: time.Second}, {Name: "fault", Duration: time.Second}, {Name: "recovery", Duration: 3 * time.Second, Recovery: &testLoadRecoveryConfig{Within: 2 * time.Second, Sustain: time.Second, Window: time.Second, Thresholds: testLoadStepThresholdConfig{MinSamples: 5}, Relative: &testLoadRecoveryRelativeSpec{P95IncreasePercent: &twenty, BaselineMinSamples: 10}}}}}
	step := func() testLoadStepSamples {
		return testLoadStepSamples{evidence: testLoadStepEvidence{Name: "probe", testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}}
	}
	collector := &testLoadCollector{phaseClock: clock, steps: []testLoadStepSamples{step()}}
	for range 3 {
		collector.chaosPhases = append(collector.chaosPhases, testLoadPhaseSamples{collectRecovery: true, steps: []testLoadStepSamples{step()}})
	}
	for i := 0; i < 10; i++ {
		collector.recordAt(0, testHTTPRequestEvidence{Passed: true, Status: 200}, 100*time.Millisecond, start.Add(time.Duration(i)*50*time.Millisecond))
	}
	for i := 0; i < 30; i++ {
		collector.recordAt(0, testHTTPRequestEvidence{Passed: true, Status: 200}, 130*time.Millisecond, start.Add(2*time.Second+time.Duration(i)*100*time.Millisecond))
	}
	report := collector.report(cfg, 5*time.Second)
	report.Status = "failed"
	if report.ChaosPhases[2].Recovery.Passed || testLoadThresholdsPassed(report) {
		t.Fatal("relative failure ignored")
	}
	failure := testLoadThresholdFailure(report)
	if !strings.Contains(failure.Error(), "relative p95_ms") {
		t.Fatal(failure)
	}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), `baseline "healthy": 10 completed samples`) || !strings.Contains(terminal.String(), "limit <= 120, observed 130") {
		t.Fatal(terminal.String())
	}
	receipt := testRunReceipt{Scenario: "relative", Status: "failed", Error: failure.Error(), Load: report}
	dir := t.TempDir()
	path := filepath.Join(dir, "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].Failure.Message, "relative p95_ms") {
		t.Fatalf("JUnit lost failure: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || decoded.Load.ChaosPhases[2].Recovery.Baseline.Requests != 10 {
		t.Fatalf("JSON lost baseline: %v", err)
	}
	path = filepath.Join(dir, "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	if !strings.Contains(string(body), "Baseline healthy: 10 completed samples") || !strings.Contains(string(body), "Relative p95_ms: baseline 100") {
		t.Fatal("HTML lost relative budget")
	}
}

func TestRecoveryRelativeScheduledRuntime(t *testing.T) {
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "relative", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "relative"})
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer control.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer app.Close()
	zero := 0.0
	spec := &testLoadSpec{VUs: 1, Duration: "3s", Pacing: "10ms", ChaosSchedule: []testLoadChaosPhaseSpec{
		{Name: "healthy", Duration: "1s"},
		{Name: "fault", Duration: "1s", Rules: []testChaosRule{{To: "cache", Kind: "latency", Latency: "100ms", Percent: 100}}},
		{Name: "recovery", Duration: "1s", Recovery: &testLoadRecoverySpec{Within: "500ms", Sustain: "200ms", Window: "200ms", Thresholds: testLoadStepThresholdSpec{MinSamples: 3}, Relative: &testLoadRecoveryRelativeSpec{ErrorRateIncrease: &zero, BaselineMinSamples: 20}}},
	}}
	report, _, err := runTestScenarioStepLoad(context.Background(), NewClient(control.URL, "token"), app.URL, "0123456789abcdef0123456789abcdef", nil, testScenarioStep{Name: "load", Requests: []testHTTPRequest{loadTestStep()}, Load: spec}, nil, nil)
	if err != nil || report == nil {
		t.Fatalf("scheduled relative recovery: %v", err)
	}
	evidence := report.ChaosPhases[2].Recovery
	if !evidence.Passed || evidence.Baseline == nil || evidence.Baseline.Phase != "healthy" || evidence.Baseline.Requests < 20 || len(evidence.Relative) != 1 || evidence.Relative[0].Limit != 0 {
		t.Fatalf("runtime baseline not collected: %+v", evidence)
	}
}
