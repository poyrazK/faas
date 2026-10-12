package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"html"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestChaosPhaseThresholdConfiguration(t *testing.T) {
	zero, success, nan := 0.0, 0.99, math.NaN()
	spec := testLoadSpec{Duration: "3s", ChaosSchedule: []testLoadChaosPhaseSpec{
		{Name: "baseline", Duration: "1s"},
		{Name: "fault", Duration: "1s", Rules: []testChaosRule{{To: "cache", Kind: "latency", Latency: "100ms", Percent: 100}}},
		{Name: "recovery", Duration: "1s", Thresholds: testLoadStepThresholdSpec{P95: "200ms", P99: "500ms", ErrorRate: &zero, SuccessRate: &success, MinSamples: 20}},
	}}
	cfg, err := resolveTestLoadConfig(testScenario{Requests: []testHTTPRequest{loadTestStep()}, Load: &spec}, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	budget := cfg.ChaosSchedule[2].Thresholds
	if budget.P95 != 200*time.Millisecond || budget.P99 != 500*time.Millisecond || budget.ErrorRate == nil || *budget.ErrorRate != 0 || budget.SuccessRate == nil || *budget.SuccessRate != success || budget.MinSamples != 20 {
		t.Fatalf("phase budget not preserved: %+v", budget)
	}
	if cfg.ChaosSchedule[0].Thresholds.ErrorRate != nil {
		t.Fatal("unspecified phase inherited a default error-rate budget")
	}
	for name, invalid := range map[string]testLoadStepThresholdSpec{
		"latency": {P95: "0s"}, "percentile": {P99: "2m"}, "samples": {MinSamples: -1},
		"error rate": {ErrorRate: &nan}, "success rate": {SuccessRate: &nan},
	} {
		t.Run(name, func(t *testing.T) {
			spec.ChaosSchedule[2].Thresholds = invalid
			if err := validateTestLoadSpec(&spec); err == nil || !strings.Contains(err.Error(), `phase "recovery" thresholds`) {
				t.Fatalf("invalid phase budget: %v", err)
			}
		})
	}
}

func TestChaosPhaseThresholdsCatchMaskedRecoveryAndEmptyPhases(t *testing.T) {
	start := time.Now()
	clock := newTestLoadPhaseClock(3)
	clock.start(start)
	clock.transition(1, start.Add(time.Second))
	clock.transition(2, start.Add(2*time.Second))
	clock.finish(start.Add(3 * time.Second))
	cfg := &testLoadConfig{P95: 50 * time.Millisecond, ErrorRate: 1, ChaosPhaseClock: clock, ChaosSchedule: []testLoadChaosPhase{
		{Name: "baseline", Duration: time.Second},
		{Name: "fault", Duration: time.Second, Thresholds: testLoadStepThresholdConfig{P99: time.Second, MinSamples: 1}},
		{Name: "recovery", Duration: time.Second, Thresholds: testLoadStepThresholdConfig{P95: 50 * time.Millisecond}},
	}}
	newSamples := func() testLoadStepSamples {
		return testLoadStepSamples{evidence: testLoadStepEvidence{Name: "ping", testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}}
	}
	collector := &testLoadCollector{phaseClock: clock, steps: []testLoadStepSamples{newSamples()}}
	for range cfg.ChaosSchedule {
		collector.chaosPhases = append(collector.chaosPhases, testLoadPhaseSamples{steps: []testLoadStepSamples{newSamples()}})
	}
	for range 99 {
		collector.recordAt(0, testHTTPRequestEvidence{Status: 200, Passed: true}, time.Millisecond, start.Add(500*time.Millisecond))
	}
	collector.recordAt(0, testHTTPRequestEvidence{Status: 200, Passed: true}, 200*time.Millisecond, start.Add(2500*time.Millisecond))
	report := collector.report(cfg, 3*time.Second)
	for _, threshold := range report.Thresholds {
		if !threshold.Passed {
			t.Fatalf("aggregate should pass: %+v", report.Thresholds)
		}
	}
	if testLoadThresholdsPassed(report) || report.ChaosPhases[2].Thresholds[0].Passed {
		t.Fatalf("recovery latency was masked: %+v", report.ChaosPhases)
	}
	for _, threshold := range report.ChaosPhases[1].Thresholds {
		if threshold.Passed {
			t.Fatalf("empty phase passed a budget: %+v", threshold)
		}
	}
	failure := testLoadThresholdFailure(report).Error()
	for _, text := range []string{`phase "fault"`, "min_samples", `phase "recovery"`, "p95_ms"} {
		if !strings.Contains(failure, text) {
			t.Fatalf("failure omitted %q: %s", text, failure)
		}
	}
}

func TestChaosPhaseThresholdBreachFailsStagedLoadAndReports(t *testing.T) {
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "phase-budget", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "phase-budget"})
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer control.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer app.Close()
	one := 1.0
	spec := &testLoadSpec{VUs: 1, Duration: "3s", Pacing: "10ms", Thresholds: testLoadThresholds{ErrorRate: &one}, ChaosSchedule: []testLoadChaosPhaseSpec{
		{Name: "baseline", Duration: "1s"},
		{Name: "fault", Duration: "1s", Rules: []testChaosRule{{To: "cache", Kind: "latency", Latency: "100ms", Percent: 100}}},
		{Name: "recovery", Duration: "1s", Thresholds: testLoadStepThresholdSpec{MinSamples: testLoadMaxRequests}, Recovery: &testLoadRecoverySpec{Within: "500ms", Sustain: "200ms", Window: "200ms", Thresholds: testLoadStepThresholdSpec{ErrorRate: &one, MinSamples: testLoadMaxRequests}}},
	}}
	report, _, err := runTestScenarioStepLoad(context.Background(), NewClient(control.URL, "token"), app.URL, "0123456789abcdef0123456789abcdef", nil,
		testScenarioStep{Name: "scheduled", Requests: []testHTTPRequest{loadTestStep()}, Load: spec}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), `phase "recovery": min_samples`) || report == nil || report.Status != "failed" || !report.Thresholds[0].Passed {
		t.Fatalf("phase breach did not fail a passing aggregate: report=%+v err=%v", report, err)
	}
	if report.ChaosPhases[2].Recovery == nil || report.ChaosPhases[2].Recovery.Passed || !strings.Contains(err.Error(), "recovery failed") {
		t.Fatalf("recovery assertion omitted: %+v, %v", report, err)
	}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), `chaos phase "recovery"`) || !strings.Contains(terminal.String(), "min_samples >= 1e+05") || !strings.Contains(terminal.String(), "(failed)") {
		t.Fatalf("terminal omitted phase budget: %s", terminal.String())
	}
	receipt := testRunReceipt{Scenario: "scheduled", Engine: "real-vm", Status: "failed", Error: err.Error(), Load: report,
		Steps: []testScenarioStepEvidence{{Name: "scheduled", Status: "failed", Load: report}}}
	dir := t.TempDir()
	path := filepath.Join(dir, "results.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || suite.Cases[0].Failure == nil || !strings.Contains(suite.Cases[0].Failure.Message, `phase "recovery": min_samples`) {
		t.Fatalf("JUnit omitted phase breach: %+v, %v", suite, err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || len(decoded.Load.ChaosPhases[2].Thresholds) != 1 || decoded.Load.ChaosPhases[2].Thresholds[0].Passed {
		t.Fatalf("JSON phase evidence missing: %+v, %v", decoded, err)
	}
	// Staged receipts need visible phase budgets even without a top-level load.
	receipt.Load = nil
	path = filepath.Join(dir, "results.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	rendered := html.UnescapeString(string(body))
	if err != nil || !strings.Contains(rendered, "Chaos phase recovery:") || !strings.Contains(rendered, "min_samples >= 1e+05:") || !strings.Contains(rendered, "(failed)") || !strings.Contains(rendered, "Recovery: failed") {
		t.Fatalf("HTML omitted phase breach: %v", err)
	}
}
