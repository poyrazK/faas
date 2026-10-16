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
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadRecoveryRollingWindows(t *testing.T) {
	zero := 0.0
	cfg := &testLoadRecoveryConfig{Within: 2 * time.Second, Sustain: time.Second, Window: time.Second, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 5}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	phase := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(4 * time.Second)}
	samples := []testLoadRecoverySample{}
	for i := 0; i < 40; i++ {
		samples = append(samples, testLoadRecoverySample{StartedAt: start.Add(time.Duration(i) * 100 * time.Millisecond), Duration: 10 * time.Millisecond, Passed: i >= 5})
	}
	report := evaluateTestLoadRecovery(cfg, phase, samples)
	if !report.Passed || report.RecoveryMS == nil || *report.RecoveryMS != 1500 {
		t.Fatalf("unexpected recovery: %+v", report)
	}
	samples[20].Passed = false
	report = evaluateTestLoadRecovery(cfg, phase, samples)
	if report.Passed {
		t.Fatal("unstable recovery passed")
	}
	if evaluateTestLoadRecovery(cfg, phase, nil).Passed {
		t.Fatal("empty windows passed")
	}
	for i := range samples {
		samples[i].Passed = true
		samples[i].Duration = 10 * time.Second
	}
	if evaluateTestLoadRecovery(cfg, phase, samples).Passed {
		t.Fatal("pending requests passed")
	}
	phase.FinishedAt = start.Add(1200 * time.Millisecond)
	if evaluateTestLoadRecovery(cfg, phase, samples).Passed {
		t.Fatal("truncated phase passed")
	}
}

func TestLoadRecoveryValidationAndFailure(t *testing.T) {
	zero := 0.0
	spec := testLoadChaosPhaseSpec{Name: "recovery", Recovery: &testLoadRecoverySpec{Within: "2s", Sustain: "1s", Window: "1s", Thresholds: testLoadStepThresholdSpec{ErrorRate: &zero, MinSamples: 5}}}
	if err := validateTestLoadRecovery(spec, true, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := validateTestLoadRecovery(spec, false, 3*time.Second); err == nil {
		t.Fatal("baseline recovery accepted")
	}
	if err := validateTestLoadRecovery(spec, true, 2*time.Second); err == nil {
		t.Fatal("insufficient duration accepted")
	}
	cfg := resolveTestLoadRecovery(spec.Recovery)
	if cfg.Within != 2*time.Second || cfg.Thresholds.MinSamples != 5 {
		t.Fatal("configuration lost")
	}
	report := &testLoadEvidence{ChaosPhases: []testLoadChaosPhaseEvidence{{Name: "recovery", Recovery: &testLoadRecoveryEvidence{Error: "no sustained healthy interval"}}}}
	if testLoadThresholdsPassed(report) {
		t.Fatal("recovery failure ignored")
	}
	if err := testLoadThresholdFailure(report); !strings.Contains(err.Error(), `phase "recovery" recovery failed`) {
		t.Fatal(err)
	}
	spec.Recovery.Thresholds.MinSamples = 0
	if err := validateTestLoadRecovery(spec, true, 3*time.Second); err == nil {
		t.Fatal("missing evidence minimum accepted")
	}
}

func TestLoadRecoveryExactDeadlineAndLatency(t *testing.T) {
	start := time.Now()
	cfg := &testLoadRecoveryConfig{Within: 1500 * time.Millisecond, Sustain: 500 * time.Millisecond, Window: time.Second, Thresholds: testLoadStepThresholdConfig{P95: 20 * time.Millisecond, MinSamples: 5}}
	phase := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	var samples []testLoadRecoverySample
	for i := 0; i < 20; i++ {
		duration := 10 * time.Millisecond
		if i < 5 {
			duration = 90 * time.Millisecond
		}
		samples = append(samples, testLoadRecoverySample{StartedAt: start.Add(time.Duration(i) * 100 * time.Millisecond), Duration: duration, Passed: true})
	}
	result := evaluateTestLoadRecovery(cfg, phase, samples)
	if !result.Passed || *result.RecoveryMS != 1500 {
		t.Fatalf("exact deadline rejected: %+v", result)
	}
	cfg.Within = 1400 * time.Millisecond
	if evaluateTestLoadRecovery(cfg, phase, samples).Passed {
		t.Fatal("late recovery passed")
	}
}

func TestLoadRecoverySuccessfulEvidence(t *testing.T) {
	start := time.Now().Add(-3 * time.Second)
	clock := newTestLoadPhaseClock(1)
	clock.start(start)
	clock.finish(start.Add(3 * time.Second))
	zero := 0.0
	cfg := &testLoadConfig{ErrorRate: 1, ChaosPhaseClock: clock, ChaosSchedule: []testLoadChaosPhase{{Name: "recovery", Duration: 3 * time.Second, Recovery: &testLoadRecoveryConfig{Within: 2 * time.Second, Sustain: time.Second, Window: time.Second, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 5}}}}}
	newStep := func() testLoadStepSamples {
		return testLoadStepSamples{evidence: testLoadStepEvidence{Name: "probe", testLoadMetrics: testLoadMetrics{Statuses: map[string]int{}}}}
	}
	collector := &testLoadCollector{phaseClock: clock, steps: []testLoadStepSamples{newStep()}, chaosPhases: []testLoadPhaseSamples{{collectRecovery: true, steps: []testLoadStepSamples{newStep()}}}}
	for i := 0; i < 30; i++ {
		collector.recordAt(0, testHTTPRequestEvidence{Passed: true, Status: 200}, time.Millisecond, start.Add(time.Duration(i)*100*time.Millisecond))
	}
	report := collector.report(cfg, 3*time.Second)
	report.Status = "passed"
	recovery := report.ChaosPhases[0].Recovery
	if recovery == nil || !recovery.Passed || *recovery.RecoveryMS != 1000 {
		t.Fatalf("recovery lost: %+v", recovery)
	}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "observed=1000.000ms") {
		t.Fatal(terminal.String())
	}
	body, err := json.Marshal(report)
	if err != nil || !strings.Contains(string(body), `"recovery_ms":1000`) {
		t.Fatalf("JSON evidence: %s %v", body, err)
	}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{{Scenario: "recovery", Status: "passed", Load: report}}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "observed 1000ms") {
		t.Fatalf("HTML recovery time missing: %v", err)
	}
}

func TestLoadRecoveryPendingAge(t *testing.T) {
	start := time.Now()
	zero := 0.0
	cfg := &testLoadRecoveryConfig{Within: time.Second, Sustain: time.Second, Window: 500 * time.Millisecond, MaxPendingAge: 200 * time.Millisecond, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 2}}
	phase := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	var healthy []testLoadRecoverySample
	for i := 0; i < 20; i++ {
		healthy = append(healthy, testLoadRecoverySample{StartedAt: start.Add(time.Duration(i) * 100 * time.Millisecond), Duration: time.Millisecond, Passed: true})
	}
	for _, tc := range []struct {
		name     string
		duration time.Duration
		want     bool
	}{
		{"stuck alongside healthy traffic", 3 * time.Second, false},
		{"drains before deadline", 700 * time.Millisecond, true},
		{"drains too late", 1500 * time.Millisecond, false},
		{"exact age allowed", 200 * time.Millisecond, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples := append(append([]testLoadRecoverySample(nil), healthy...), testLoadRecoverySample{StartedAt: start, Duration: tc.duration, Passed: true})
			report := evaluateTestLoadRecovery(cfg, phase, samples)
			if report.Passed != tc.want {
				t.Fatalf("unexpected recovery: %+v", report)
			}
			if len(report.Thresholds) == 0 || report.MaxPendingAgeMS != 200 {
				t.Fatalf("missing measurements: %+v", report)
			}
			if tc.name == "stuck alongside healthy traffic" && (report.OverdueRequests != 1 || !strings.Contains(report.Error, "max_pending_age")) {
				t.Fatalf("missing pending failure: %+v", report)
			}
		})
	}
	cfg.MaxPendingAge = 0
	samples := append(healthy, testLoadRecoverySample{StartedAt: start, Duration: 3 * time.Second, Passed: true})
	if !evaluateTestLoadRecovery(cfg, phase, samples).Passed {
		t.Fatal("optional pending budget changed existing behavior")
	}
}

func TestLoadRecoveryPendingAgeValidation(t *testing.T) {
	zero := 0.0
	spec := testLoadChaosPhaseSpec{Name: "recover", Recovery: &testLoadRecoverySpec{Within: "1s", Sustain: "1s", Window: "500ms", Thresholds: testLoadStepThresholdSpec{ErrorRate: &zero, MinSamples: 2}}}
	for _, value := range []string{"0s", "-1s", "1.5ms", "6m", "invalid"} {
		spec.Recovery.MaxPendingAge = value
		if validateTestLoadRecovery(spec, true, 2*time.Second) == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	spec.Recovery.MaxPendingAge = "200ms"
	if err := validateTestLoadRecovery(spec, true, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if resolveTestLoadRecovery(spec.Recovery).MaxPendingAge != 200*time.Millisecond {
		t.Fatal("pending budget lost")
	}
}

func TestLoadRecoveryPendingReportsAndEndpointIsolation(t *testing.T) {
	start := time.Now()
	zero := 0.0
	cfg := &testLoadRecoveryConfig{Within: time.Second, Sustain: time.Second, Window: 500 * time.Millisecond, MaxPendingAge: 100 * time.Millisecond, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 2}}
	phase := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	samples := []testLoadRecoverySample{{Name: "stuck", StartedAt: start, Duration: 3 * time.Second, Passed: false}}
	for i := 0; i < 20; i++ {
		samples = append(samples, testLoadRecoverySample{Name: "healthy", StartedAt: start.Add(time.Duration(i) * 100 * time.Millisecond), Duration: time.Millisecond, Passed: true})
	}
	recovery := evaluateTestLoadRecovery(cfg, phase, samples)
	if !evaluateTestLoadRecovery(cfg, phase, testEndpointRecoverySamples(samples, "healthy")).Passed {
		t.Fatal("another endpoint's stuck request masked health")
	}
	report := &testLoadEvidence{Status: "failed", ChaosPhases: []testLoadChaosPhaseEvidence{{Name: "recover", Recovery: recovery, StepRecovery: map[string]*testLoadRecoveryEvidence{"stuck": recovery}}}}
	failure := testLoadThresholdFailure(report)
	if failure == nil || !strings.Contains(failure.Error(), "max_pending_age") {
		t.Fatal("pending budget did not fail scenario")
	}
	receipt := testRunReceipt{Scenario: "pending", Status: "failed", Error: failure.Error(), Load: report}
	dir := t.TempDir()
	path := filepath.Join(dir, "report.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var suite testJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 {
		t.Fatalf("JUnit missing failure: %v", err)
	}
	var decoded testRunReceipt
	if err := json.Unmarshal([]byte(suite.Cases[0].SystemOut), &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.Load.ChaosPhases[0].Recovery
	if got.OverdueRequests != 1 || got.MaxPendingAgeMS != 100 || len(got.Thresholds) == 0 {
		t.Fatalf("measurements lost: %+v", got)
	}
	path = filepath.Join(dir, "report.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil || strings.Count(string(body), "overdue requests 1 (maximum pending age 100ms)") != 2 {
		t.Fatalf("HTML measurements missing: %v", err)
	}
	cfg.Stability = 500 * time.Millisecond
	cfg.Sustain = 100 * time.Millisecond
	samples[0].StartedAt = start.Add(650 * time.Millisecond)
	recovery = evaluateTestLoadRecovery(cfg, testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2500 * time.Millisecond)}, samples)
	if recovery.Passed || !strings.Contains(recovery.Error, "max_pending_age") {
		t.Fatalf("stability relapse lost pending reason: %+v", recovery)
	}
}

func TestLoadRecoveryPendingCarryover(t *testing.T) {
	start := time.Now()
	zero := 0.0
	budget := &testLoadRecoveryConfig{Within: time.Second, Sustain: time.Second, Window: 500 * time.Millisecond, MaxPendingAge: 200 * time.Millisecond, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 2}}
	phases := []testLoadChaosPhase{{Name: "fault"}, {Name: "recover", Recovery: budget}}
	if !testLoadNeedsPendingRecovery(phases) {
		t.Fatal("carryover collection disabled")
	}
	if !testLoadNeedsPendingRecovery([]testLoadChaosPhase{{StepRecovery: map[string]*testLoadRecoveryConfig{"checkout": budget}}}) {
		t.Fatal("endpoint carryover collection disabled")
	}
	collector := &testLoadCollector{chaosPhases: []testLoadPhaseSamples{
		{recoverySamples: []testLoadRecoverySample{{Name: "checkout", StartedAt: start.Add(-time.Second), Duration: 4 * time.Second, Passed: false}}},
		{},
	}}
	for i := 0; i < 20; i++ {
		collector.chaosPhases[1].recoverySamples = append(collector.chaosPhases[1].recoverySamples, testLoadRecoverySample{Name: "checkout", StartedAt: start.Add(time.Duration(i) * 100 * time.Millisecond), Duration: time.Millisecond, Passed: true})
	}
	phase := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	recovery := evaluateTestLoadRecovery(budget, phase, collector.recoverySamplesFor(1, budget))
	if recovery.Passed || recovery.OverdueRequests != 1 {
		t.Fatalf("fault request hidden: %+v", recovery)
	}
	// A failed fault-phase request which finishes during recovery does not
	// contaminate the recovery-phase error budget after its pending state drains.
	collector.chaosPhases[0].recoverySamples[0].Duration = 1200 * time.Millisecond
	recovery = evaluateTestLoadRecovery(budget, phase, collector.recoverySamplesFor(1, budget))
	if !recovery.Passed {
		t.Fatalf("fault completion entered recovery metrics: %+v", recovery)
	}
	budget.MaxPendingAge = 0
	if len(collector.recoverySamplesFor(1, budget)) != 20 || testLoadNeedsPendingRecovery(phases) {
		t.Fatal("optional carryover collection changed legacy behavior")
	}
}

func TestLoadRecoveryPendingScheduledRuntime(t *testing.T) {
	var first atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			timer := time.NewTimer(3500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	step := loadTestStep()
	cfg, err := resolveTestLoadConfig(testScenario{Requests: []testHTTPRequest{step}, Load: &testLoadSpec{VUs: 2, Duration: "3s", Pacing: "10ms"}}, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ErrorRate = 1
	zero := 0.0
	budget := &testLoadRecoveryConfig{Within: 500 * time.Millisecond, Sustain: 200 * time.Millisecond, Window: 200 * time.Millisecond, MaxPendingAge: 100 * time.Millisecond, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 3}}
	clock := newTestLoadPhaseClock(3)
	cfg.ChaosPhaseClock = clock
	cfg.OnStart = func(start time.Time) {
		clock.start(start)
		clock.transition(1, start.Add(time.Second))
		clock.transition(2, start.Add(2*time.Second))
		clock.finish(start.Add(3 * time.Second))
	}
	cfg.ChaosSchedule = []testLoadChaosPhase{{Name: "baseline", Duration: time.Second}, {Name: "fault", Duration: time.Second}, {Name: "recover", Duration: time.Second, Recovery: budget, StepRecovery: map[string]*testLoadRecoveryConfig{step.Name: budget}}}
	report, err := runTestLoad(context.Background(), server.URL, "pending-runtime", nil, []testHTTPRequest{step}, nil, cfg)
	if err == nil || report == nil {
		t.Fatalf("runtime hid pending request: %v", err)
	}
	recovery := report.ChaosPhases[2].Recovery
	if recovery.Passed || recovery.OverdueRequests != 1 || report.ChaosPhases[2].StepRecovery[step.Name].OverdueRequests != 1 {
		t.Fatalf("carryover missing from runtime: %+v", recovery)
	}
	for _, threshold := range recovery.Thresholds {
		if !threshold.Passed {
			t.Fatalf("failure should come from pending request, got %+v", threshold)
		}
	}
}
