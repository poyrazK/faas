package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
