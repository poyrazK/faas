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

func TestRecoveryStabilityObservation(t *testing.T) {
	zero := 0.0
	cfg := &testLoadRecoveryConfig{Within: time.Second, Sustain: 200 * time.Millisecond, Window: 200 * time.Millisecond, Stability: 550 * time.Millisecond, Thresholds: testLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 2}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	phase := testLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	samples := make([]testLoadRecoverySample, 40)
	for i := range samples {
		samples[i] = testLoadRecoverySample{StartedAt: start.Add(time.Duration(i) * 50 * time.Millisecond), Duration: time.Millisecond, Passed: true}
	}
	healthy := evaluateTestLoadRecovery(cfg, phase, samples)
	if !healthy.Passed || healthy.Stability == nil || !healthy.Stability.Passed || healthy.Stability.ObservedMS != 550 || *healthy.RecoveryMS != 200 || *healthy.Stability.StartedMS != 400 {
		t.Fatalf("healthy observation: %+v / %+v", healthy, healthy.Stability)
	}
	for _, mode := range []string{"errors", "latency", "empty", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			local := *cfg
			data := append([]testLoadRecoverySample(nil), samples...)
			window := phase
			switch mode {
			case "errors":
				data[12].Passed = false
			case "latency":
				local.Thresholds.P95 = 20 * time.Millisecond
				data[12].Duration = 100 * time.Millisecond
			case "empty":
				data = data[:9]
			case "truncated":
				window.FinishedAt = start.Add(800 * time.Millisecond)
			}
			r := evaluateTestLoadRecovery(&local, window, data)
			if r.Passed || r.RecoveryMS == nil || r.Stability.Passed {
				t.Fatalf("missed instability: %+v", r)
			}
			if mode == "truncated" {
				if !strings.Contains(r.Error, "did not complete") || r.Stability.RelapseMS != nil {
					t.Fatalf("truncation: %+v", r)
				}
			} else if r.Stability.RelapseMS == nil || !strings.Contains(r.Error, "relapsed") {
				t.Fatalf("missing relapse: %+v", r)
			}
		})
	}
	samples[12].Passed = false
	legacy := *cfg
	legacy.Stability = 0
	if !evaluateTestLoadRecovery(&legacy, phase, samples).Passed {
		t.Fatal("legacy recovery must finish before late failure")
	}
	phase.FinishedAt = start.Add(300 * time.Millisecond)
	r := evaluateTestLoadRecovery(cfg, phase, samples)
	if r.Passed || r.RecoveryMS != nil || r.Stability.StartedMS != nil {
		t.Fatal("initial recovery incomplete")
	}
}

func TestRecoveryStabilityValidation(t *testing.T) {
	zero := 0.0
	spec := testLoadChaosPhaseSpec{Name: "recovery", Recovery: &testLoadRecoverySpec{Within: "1s", Sustain: "200ms", Window: "200ms", Stability: "550ms", Thresholds: testLoadStepThresholdSpec{ErrorRate: &zero, MinSamples: 2}}}
	if err := validateTestLoadRecovery(spec, true, 1750*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if resolveTestLoadRecovery(spec.Recovery).Stability != 550*time.Millisecond {
		t.Fatal("stability lost")
	}
	if err := validateTestLoadRecovery(spec, true, 1749*time.Millisecond); err == nil {
		t.Fatal("observation outside phase accepted")
	}
	for _, invalid := range []string{"0s", "99ms", "100.1ms", "6m", "invalid"} {
		spec.Recovery.Stability = invalid
		if validateTestLoadRecovery(spec, true, 5*time.Minute) == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	scenario := minimizationTestScenario()
	scenario.Steps[1].Load.ChaosSchedule[2].Recovery.Stability = "100ms"
	target := testMinimizationTarget{Step: "load", Phase: "recovery"}
	if _, err := applyTestRecoveryDeadlineOverrides(scenario, []testRecoveryDeadlineOverride{{Target: target, Within: "800ms"}}); err == nil {
		t.Fatal("deadline override ignored stability duration")
	}
	if _, err := applyTestRecoveryDeadlineOverrides(scenario, []testRecoveryDeadlineOverride{{Target: target, Within: "700ms"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryStabilityReportsAndReplay(t *testing.T) {
	report := endpointRecoveryReport("stability")
	r := report.ChaosPhases[2].StepRecovery["checkout"]
	if r.Passed || r.RecoveryMS == nil || r.Stability == nil || r.Stability.RelapseMS == nil {
		t.Fatalf("endpoint relapse not detected: %+v", r)
	}
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "stability: observed=") || !strings.Contains(terminal.String(), "relapse at") {
		t.Fatal(terminal.String())
	}
	receipt := testRunReceipt{Scenario: "stability", Engine: "real-vm", Status: "failed", RunID: "stability-failed", Load: report, Steps: []testScenarioStepEvidence{{Name: "load", Load: report}}}
	bundle := prepareTestRecoveryFailureDiagnostics(&receipt)
	expected := report.ChaosPhases[2].StartedAt.Add(time.Duration(*r.Stability.RelapseMS * float64(time.Millisecond))).Add(-5 * time.Second)
	if bundle == nil || !bundle.StartedAt.Equal(expected) {
		t.Fatalf("diagnostics not centered on relapse: %+v", bundle)
	}
	path := filepath.Join(t.TempDir(), "stability.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Stability:") || !strings.Contains(string(data), "relapse at") {
		t.Fatalf("HTML evidence missing: %v", err)
	}
	spec := endpointRecoverySpec()
	old, _ := json.Marshal(spec)
	if strings.Contains(string(old), "stability") {
		t.Fatal("unused stability changes old replay fingerprint")
	}
	scenario := minimizationTestScenario()
	before := newTestReplayManifest("checkout", "cold", "", scenario)
	scenario.Steps[1].Load.ChaosSchedule[2].Recovery.Stability = "100ms"
	after := newTestReplayManifest("checkout", "cold", "", scenario)
	if before.ConfigurationSHA256 == after.ConfigurationSHA256 {
		t.Fatal("replay fingerprint ignores stability")
	}
	path = filepath.Join(t.TempDir(), "stability.xml")
	if err := writeTestJUnit(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "relapse_ms") {
		t.Fatalf("JUnit evidence missing: %v", err)
	}
}
