package main

import (
	"bytes"
	"context"
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

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBulkheadPhaseBudgetValidation(t *testing.T) {
	health := loadTestStep()
	health.Name = "health"
	zero := 0.0
	spec := testLoadSpec{Duration: "3s", ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "baseline", Duration: "1s"}, {Name: "fault", Duration: "1s", Rules: []testChaosRule{{To: "cache", Kind: "http_status", Percent: 100, StatusCode: 503}}, StepThresholds: map[string]testLoadStepThresholdSpec{"health": {P95: "200ms", ErrorRate: &zero, MinSamples: 5}}}, {Name: "recovery", Duration: "1s"}}}
	scenario := testScenario{Requests: []testHTTPRequest{health}, Load: &spec}
	cfg, err := resolveTestLoadConfig(scenario, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	budget := cfg.ChaosSchedule[1].StepThresholds["health"]
	if budget.P95 != 200*time.Millisecond || budget.MinSamples != 5 || budget.ErrorRate == nil || *budget.ErrorRate != 0 || cfg.ChaosSchedule[0].StepThresholds != nil {
		t.Fatal("budget lost or inherited")
	}
	for _, bad := range []testLoadStepThresholdSpec{{P95: "200ms"}, {MinSamples: 5}, {P95: "0s", MinSamples: 5}, {P95: "200ms", MinSamples: testLoadMaxRequests + 1}, {P95: "200ms", ErrorRate: func() *float64 { v := math.NaN(); return &v }(), MinSamples: 5}} {
		scenario.Load.ChaosSchedule[1].StepThresholds = map[string]testLoadStepThresholdSpec{"health": bad}
		if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil {
			t.Fatalf("invalid budget accepted: %+v", bad)
		}
	}
	scenario.Load.ChaosSchedule[1].StepThresholds = map[string]testLoadStepThresholdSpec{"missing": {P95: "200ms", MinSamples: 5}}
	if _, err := resolveTestLoadConfig(scenario, testLoadOverrides{}); err == nil || !strings.Contains(err.Error(), "undeclared HTTP step") {
		t.Fatalf("unknown endpoint accepted: %v", err)
	}
}

func bulkheadTestReport(mode string) *testLoadEvidence {
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	clock := newTestLoadPhaseClock(3)
	clock.start(start)
	clock.transition(1, start.Add(time.Second))
	clock.transition(2, start.Add(2*time.Second))
	clock.finish(start.Add(3 * time.Second))
	zero, one := 0.0, 1.0
	cfg := &testLoadConfig{VUs: 1, Duration: 3 * time.Second, ErrorRate: 1, P95: 200 * time.Millisecond, StepThresholds: map[string]testLoadStepThresholdConfig{"health": {P95: 200 * time.Millisecond, ErrorRate: &one}}, ChaosSchedule: []testLoadChaosPhase{{Name: "baseline", Duration: time.Second}, {Name: "fault", Duration: time.Second, Thresholds: testLoadStepThresholdConfig{P95: 200 * time.Millisecond, ErrorRate: &one, MinSamples: 5}, StepThresholds: map[string]testLoadStepThresholdConfig{"health": {P95: 200 * time.Millisecond, ErrorRate: &zero, MinSamples: 5}}}, {Name: "recovery", Duration: time.Second}}}
	samples := func() []testLoadStepSamples {
		result := make([]testLoadStepSamples, 2)
		for i, name := range []string{"checkout", "health"} {
			result[i].evidence = testLoadStepEvidence{Name: name, testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}
		}
		return result
	}

	collector := &testLoadCollector{started: 1, completed: 1, phaseClock: clock, steps: samples()}
	for range 3 {
		collector.chaosPhases = append(collector.chaosPhases, testLoadPhaseSamples{steps: samples()})
	}
	for range 95 {
		collector.recordAt(1, testHTTPRequestEvidence{Passed: true, Status: 200}, time.Millisecond, start.Add(500*time.Millisecond))
	}
	for range 100 {
		collector.recordAt(0, testHTTPRequestEvidence{Passed: true, Status: 200}, time.Millisecond, start.Add(1500*time.Millisecond))
	}
	count := 5
	duration := time.Millisecond
	passed := true
	status := 200
	switch mode {
	case "slow":
		duration = 500 * time.Millisecond
	case "errors":
		passed = false
		status = 500
	case "empty":
		count = 0
	case "starved":
		count = 2
	}
	for range count {
		collector.recordAt(1, testHTTPRequestEvidence{Passed: passed, Status: status}, duration, start.Add(1500*time.Millisecond))
	}
	for range 20 {
		collector.recordAt(1, testHTTPRequestEvidence{Passed: true, Status: 200}, time.Millisecond, start.Add(2500*time.Millisecond))
	}
	report := collector.report(cfg, 3*time.Second)
	report.Status = "passed"
	if !testLoadThresholdsPassed(report) {
		report.Status = "failed"
	}
	return report
}

func TestBulkheadFailuresCannotHideBehindAggregate(t *testing.T) {
	for _, mode := range []string{"healthy", "slow", "errors", "empty", "starved"} {
		t.Run(mode, func(t *testing.T) {
			report := bulkheadTestReport(mode)
			for _, threshold := range report.Thresholds {
				if !threshold.Passed {
					t.Fatal("overall budget unexpectedly failed")
				}
			}
			for _, threshold := range report.ChaosPhases[1].Thresholds {
				if !threshold.Passed {
					t.Fatal("aggregate phase budget unexpectedly failed")
				}
			}
			for _, step := range report.Steps {
				for _, threshold := range step.Thresholds {
					if !threshold.Passed {
						t.Fatal("whole-run endpoint budget unexpectedly failed")
					}
				}
			}
			if testLoadThresholdsPassed(report) != (mode == "healthy") {
				t.Fatalf("fault endpoint result masked: %+v", report.ChaosPhases[1].Steps)
			}
			if mode != "healthy" {
				failure := testLoadThresholdFailure(report).Error()
				if !strings.Contains(failure, `phase "fault" HTTP step "health"`) {
					t.Fatal(failure)
				}
			}
			if len(report.ChaosPhases[0].Steps[1].Thresholds) != 0 || len(report.ChaosPhases[2].Steps[1].Thresholds) != 0 {
				t.Fatal("budget leaked into other phases")
			}
		})
	}
}

func TestBulkheadBudgetsReports(t *testing.T) {
	report := bulkheadTestReport("slow")
	failure := testLoadThresholdFailure(report)
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), `HTTP step "health": 5 samples`) || !strings.Contains(terminal.String(), "p95_ms <= 200: 500 (failed)") {
		t.Fatal(terminal.String())
	}
	receipt := testRunReceipt{Scenario: "bulkhead", Status: "failed", Error: failure.Error(), Load: report, Steps: []testScenarioStepEvidence{{Name: "load", Status: "failed", Load: report}}}
	path := filepath.Join(t.TempDir(), "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].Failure.Message, `HTTP step "health"`) {
		t.Fatalf("JUnit failure missing: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil || len(decoded.Load.ChaosPhases[1].Steps[1].Thresholds) != 3 {
		t.Fatalf("JSON budgets missing: %v", err)
	}
	path = filepath.Join(t.TempDir(), "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "HTTP step health: 5 samples") || !strings.Contains(string(body), "health.p95_ms") {
		t.Fatal("HTML endpoint budgets missing")
	}
}

func TestBulkheadBudgetFailsScheduledLoad(t *testing.T) {
	var fault atomic.Bool
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			fault.Store(true)
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "bulkhead", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
		case http.MethodDelete:
			fault.Store(false)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "bulkhead"})
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer control.Close()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" && fault.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer app.Close()
	checkout, health := loadTestStep(), loadTestStep()
	checkout.Name = "checkout"
	health.Name = "health"
	health.Path = "/health"
	one, rate := 1.0, 0.01
	spec := &testLoadSpec{Duration: "3s", Rate: 20, VUs: 2, Thresholds: testLoadThresholds{ErrorRate: &one}, ChaosSchedule: []testLoadChaosPhaseSpec{{Name: "baseline", Duration: "1s"}, {Name: "fault", Duration: "1s", Rules: []testChaosRule{{From: "app", To: "cache", Kind: "http_status", Percent: 100, StatusCode: 503}}, StepThresholds: map[string]testLoadStepThresholdSpec{"health": {P95: "200ms", ErrorRate: &rate, MinSamples: 5}}}, {Name: "recovery", Duration: "1s"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report, _, err := runTestScenarioStepLoad(ctx, NewClient(control.URL, "token"), app.URL, "run", nil, testScenarioStep{Name: "isolation", Requests: []testHTTPRequest{checkout, health}, Load: spec}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), `phase "fault" HTTP step "health": error_rate`) || report == nil || report.Status != "failed" {
		t.Fatalf("isolation failure did not fail run: %+v %v", report, err)
	}
	for _, threshold := range report.Thresholds {
		if !threshold.Passed {
			t.Fatal("aggregate unexpectedly failed")
		}
	}
	if len(report.ChaosPhases[0].Steps[1].Thresholds) != 0 || len(report.ChaosPhases[2].Steps[1].Thresholds) != 0 {
		t.Fatal("budget leaked outside fault phase")
	}
	if fault.Load() {
		t.Fatal("fault was not cleared")
	}
}
