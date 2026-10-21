package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecoveryStabilityObservation(t *testing.T) {
	zero := 0.0
	cfg := &main.TestLoadRecoveryConfig{Within: time.Second, Sustain: 200 * time.Millisecond, Window: 200 * time.Millisecond, Stability: 550 * time.Millisecond, Thresholds: main.TestLoadStepThresholdConfig{ErrorRate: &zero, MinSamples: 2}}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	phase := main.TestLoadPhaseWindow{StartedAt: start, FinishedAt: start.Add(2 * time.Second)}
	samples := make([]main.TestLoadRecoverySample, 40)
	for i := range samples {
		samples[i] = main.TestLoadRecoverySample{StartedAt: start.Add(time.Duration(i) * 50 * time.Millisecond), Duration: time.Millisecond, Passed: true}
	}
	healthy := main.EvaluateTestLoadRecovery(cfg, phase, samples)
	if !healthy.Passed || healthy.Stability == nil || !healthy.Stability.Passed || healthy.Stability.ObservedMS != 550 || *healthy.RecoveryMS != 200 || *healthy.Stability.StartedMS != 400 {
		t.Fatalf("healthy observation: %+v / %+v", healthy, healthy.Stability)
	}
	for _, mode := range []string{"errors", "latency", "empty", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			local := *cfg
			data := append([]main.TestLoadRecoverySample(nil), samples...)
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
			r := main.EvaluateTestLoadRecovery(&local, window, data)
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
	if !main.EvaluateTestLoadRecovery(&legacy, phase, samples).Passed {
		t.Fatal("legacy recovery must finish before late failure")
	}
	phase.FinishedAt = start.Add(300 * time.Millisecond)
	r := main.EvaluateTestLoadRecovery(cfg, phase, samples)
	if r.Passed || r.RecoveryMS != nil || r.Stability.StartedMS != nil {
		t.Fatal("initial recovery incomplete")
	}
}

func TestRecoveryStabilityValidation(t *testing.T) {
	zero := 0.0
	spec := main.TestLoadChaosPhaseSpec{Name: "recovery", Recovery: &main.TestLoadRecoverySpec{Within: "1s", Sustain: "200ms", Window: "200ms", Stability: "550ms", Thresholds: main.TestLoadStepThresholdSpec{ErrorRate: &zero, MinSamples: 2}}}
	if err := main.ValidateTestLoadRecovery(spec, true, 1750*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if main.ResolveTestLoadRecovery(spec.Recovery).Stability != 550*time.Millisecond {
		t.Fatal("stability lost")
	}
	if err := main.ValidateTestLoadRecovery(spec, true, 1749*time.Millisecond); err == nil {
		t.Fatal("observation outside phase accepted")
	}
	for _, invalid := range []string{"0s", "99ms", "100.1ms", "6m", "invalid"} {
		spec.Recovery.Stability = invalid
		if main.ValidateTestLoadRecovery(spec, true, 5*time.Minute) == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	scenario := main.MinimizationTestScenario()
	scenario.Steps[1].Load.ChaosSchedule[2].Recovery.Stability = "100ms"
	target := main.TestMinimizationTarget{Step: "load", Phase: "recovery"}
	if _, err := main.ApplyTestRecoveryDeadlineOverrides(scenario, []main.TestRecoveryDeadlineOverride{{Target: target, Within: "800ms"}}); err == nil {
		t.Fatal("deadline override ignored stability duration")
	}
	if _, err := main.ApplyTestRecoveryDeadlineOverrides(scenario, []main.TestRecoveryDeadlineOverride{{Target: target, Within: "700ms"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryStabilityReportsAndReplay(t *testing.T) {
	report := main.EndpointRecoveryReport("stability")
	r := report.ChaosPhases[2].StepRecovery["checkout"]
	if r.Passed || r.RecoveryMS == nil || r.Stability == nil || r.Stability.RelapseMS == nil {
		t.Fatalf("endpoint relapse not detected: %+v", r)
	}
	var terminal bytes.Buffer
	main.PrintTestLoadSummary(&terminal, report)
	if !strings.Contains(terminal.String(), "stability: observed=") || !strings.Contains(terminal.String(), "relapse at") {
		t.Fatal(terminal.String())
	}
	receipt := main.TestRunReceipt{Scenario: "stability", Engine: "real-vm", Status: "failed", RunID: "stability-failed", Load: report, Steps: []main.TestScenarioStepEvidence{{Name: "load", Load: report}}}
	bundle := main.PrepareTestRecoveryFailureDiagnostics(&receipt)
	expected := report.ChaosPhases[2].StartedAt.Add(time.Duration(*r.Stability.RelapseMS * float64(time.Millisecond))).Add(-5 * time.Second)
	if bundle == nil || !bundle.StartedAt.Equal(expected) {
		t.Fatalf("diagnostics not centered on relapse: %+v", bundle)
	}
	path := filepath.Join(t.TempDir(), "stability.html")
	if err := main.WriteTestHTMLReport(path, "", []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "Stability:") || !strings.Contains(string(data), "relapse at") {
		t.Fatalf("HTML evidence missing: %v", err)
	}
	spec := main.EndpointRecoverySpec()
	old, _ := json.Marshal(spec)
	if strings.Contains(string(old), "stability") {
		t.Fatal("unused stability changes old replay fingerprint")
	}
	scenario := main.MinimizationTestScenario()
	before := main.NewTestReplayManifest("checkout", "cold", "", scenario)
	scenario.Steps[1].Load.ChaosSchedule[2].Recovery.Stability = "100ms"
	after := main.NewTestReplayManifest("checkout", "cold", "", scenario)
	if before.ConfigurationSHA256 == after.ConfigurationSHA256 {
		t.Fatal("replay fingerprint ignores stability")
	}
	path = filepath.Join(t.TempDir(), "stability.xml")
	if err := main.WriteTestJUnit(path, []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "relapse_ms") {
		t.Fatalf("JUnit evidence missing: %v", err)
	}
}
