package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func sweepTestScenario(mode string) testScenario {
	s := minimizationTestScenario()
	index := 1
	s.Resilience = &testResilienceSpec{Runs: 2, MinPassRate: 1, FaultSweep: &testFaultSweepSpec{Step: "load", Phase: "fault", RuleIndex: &index, Mode: mode, MaxRuns: 6, MaxWorkloadMinutes: 24}}
	switch mode {
	case "duration":
		s.Resilience.FaultSweep.RuleIndex = nil
		s.Resilience.FaultSweep.Values = []string{"1s", "5s", "15s"}
	case "latency":
		s.Resilience.FaultSweep.Values = []string{"10ms", "100ms", "500ms"}
	case "bandwidth":
		s.Steps[1].Load.ChaosSchedule[1].Rules[1].Kind = "tcp_bandwidth"
		s.Steps[1].Load.ChaosSchedule[1].Rules[1].Latency = ""
		s.Steps[1].Load.ChaosSchedule[1].Rules[1].RateKiBPerSecond = 10
		s.Resilience.FaultSweep.Values = []string{"100", "10", "1"}
	case "percent":
		s.Resilience.FaultSweep.Values = []string{"10", "50", "100"}
	}
	s.Steps[1].Load.ChaosSchedule[1].Rules[1].Seed = 42
	s.Steps[1].Load.ChaosSchedule[2].Recovery.Stability = "100ms"
	return s
}

func TestFaultSweepExpansionIsolationBounds(t *testing.T) {
	for _, mode := range []string{"latency", "bandwidth", "percent"} {
		t.Run(mode, func(t *testing.T) {
			original := sweepTestScenario(mode)
			before, _ := json.Marshal(original)
			expanded, err := expandTestFaultSweep(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateTestResilience(expanded); err != nil {
				t.Fatal(err)
			}
			if testResilienceRuns(original) != 6 || estimateTestWorkloadMinutes(expanded, 2) != 48 {
				t.Fatal("sweep not counted")
			}
			for i, c := range expanded.Resilience.FaultMatrix {
				variant, err := applyTestFaultMatrixCase(expanded, c)
				if err != nil {
					t.Fatal(err)
				}
				if variant.Resilience.FaultSweep != nil || len(variant.Resilience.FaultMatrix) != 0 || variant.Resilience.Runs != 2 {
					t.Fatal("recursive sweep")
				}
				rules := variant.Steps[1].Load.ChaosSchedule[1].Rules
				if len(rules) != 3 || rules[1].Seed != 42 || rules[0] != original.Steps[1].Load.ChaosSchedule[1].Rules[0] || rules[2] != original.Steps[1].Load.ChaosSchedule[1].Rules[2] || variant.Steps[1].Load.ChaosSchedule[2].Recovery.Stability != "100ms" {
					t.Fatal("fixed settings changed")
				}
				switch mode {
				case "latency":
					if rules[1].Latency != original.Resilience.FaultSweep.Values[i] {
						t.Fatal("latency setting missing")
					}
				case "bandwidth":
					if fmt.Sprint(rules[1].RateKiBPerSecond) != c.Sweep.Value {
						t.Fatal("bandwidth setting missing")
					}
				case "percent":
					if fmt.Sprint(rules[1].Percent) != c.Sweep.Value {
						t.Fatal("percent setting missing")
					}
				}
			}
			after, _ := json.Marshal(original)
			if string(before) != string(after) {
				t.Fatal("template mutated")
			}
		})
	}
	for _, invalid := range []string{"matrix", "index", "mode", "duplicates", "one", "eleven", "runs", "minutes", "kind", "latency", "percent", "bandwidth", "phase", "recovery", "timeout"} {
		t.Run(invalid, func(t *testing.T) {
			s := sweepTestScenario("latency")
			spec := s.Resilience.FaultSweep
			switch invalid {
			case "matrix":
				s.Resilience.FaultMatrix = faultMatrixTestScenario().Resilience.FaultMatrix
			case "index":
				spec.RuleIndex = nil
			case "mode":
				spec.Mode = "recovery"
			case "duplicates":
				spec.Values = []string{"1s", "1000ms"}
			case "one":
				spec.Values = spec.Values[:1]
			case "eleven":
				spec.Values = make([]string, 11)
			case "runs":
				spec.MaxRuns = 5
			case "minutes":
				spec.MaxWorkloadMinutes = 23
			case "kind":
				spec.Mode = "bandwidth"
			case "latency":
				spec.Values[0] = "100.1ms"
			case "percent":
				spec.Mode = "percent"
				spec.Values = []string{"0", "100"}
			case "bandwidth":
				s = sweepTestScenario("bandwidth")
				s.Resilience.FaultSweep.Values[0] = "1000001"
			case "phase":
				spec.Phase = "healthy"
			case "recovery":
				s.Steps[1].Load.ChaosSchedule[2].Recovery = nil
			case "timeout":
				s.Timeout = "invalid"
			}
			if err := validateTestResilience(s); err == nil {
				t.Fatal("invalid sweep accepted")
			}
		})
	}
}

func runSweepTest(ctx context.Context, s testScenario, execute func(context.Context, testRunPlan) testRunReceipt) testRunReceipt {
	return runTestFaultMatrix(ctx, testRunPlan{Prepared: &testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: s}, Profile: "cold"}, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		return runTestResilience(ctx, plan, execute)
	})
}
func TestFaultSweepRunsReportsReplay(t *testing.T) {
	s := sweepTestScenario("latency")
	calls := 0
	result := runSweepTest(context.Background(), s, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		value := plan.ReplaySweep.Value
		failed := value == "10ms"
		row := deadlineTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("sweep-run-%d", calls), testMinimizationTarget{Step: "load", Phase: "recovery"}, failed)
		row.Replay = newTestReplayManifest("checkout", "cold", plan.ReplayCase, plan.Prepared.Scenario)
		row.Replay.Sweep = plan.ReplaySweep
		row.Replay.Version = testFaultReplayVersion(row.Replay)
		return row
	})
	if calls != 6 || result.Status != "failed" || result.FaultMatrix.Passed != 2 || result.FaultMatrix.Failed != 1 {
		t.Fatalf("sweep outcome: calls=%d status=%s", calls, result.Status)
	}
	for _, group := range result.FaultMatrix.Cases {
		if group.Sweep == nil || group.Resilience.Runs != 2 {
			t.Fatal("setting evidence missing")
		}
		for _, leaf := range group.Resilience.History {
			selected, err := selectTestReplay([]testRunReceipt{result}, leaf.RunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := prepareTestReplay(selected, map[string]testScenario{"checkout": s}, ".")
			if err != nil {
				t.Fatal(err)
			}
			if replay.Scenario.Resilience != nil || replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Latency != group.Sweep.Value {
				t.Fatal("wrong replay setting")
			}
			for _, bad := range []string{"value", "case", "version", "hash"} {
				altered := *selected
				descriptor := *selected.Replay
				setting := *descriptor.Sweep
				descriptor.Sweep = &setting
				altered.Replay = &descriptor
				switch bad {
				case "value":
					setting.Value = "20ms"
				case "case":
					descriptor.MatrixCase = "absent"
				case "version":
					descriptor.Version = 7
				case "hash":
					descriptor.ConfigurationSHA256 = strings.Repeat("0", 64)
				}
				if _, err := prepareTestReplay(&altered, map[string]testScenario{"checkout": s}, "."); err == nil {
					t.Fatalf("replay tampering accepted: %s", bad)
				}
			}
		}
	}
	// A sweep failure remains usable by existing fault minimization modes.
	original := result.FaultMatrix.Cases[0].Resilience.History[0]
	prepared, err := prepareTestReplay(&original, map[string]testScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	options := minimizationTestOptions()
	options.Mode = "latency"
	options.RuleIndex = 1
	options.Resolution = time.Millisecond
	minimized := runTestFaultMinimization(context.Background(), prepared, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		duration, _ := time.ParseDuration(plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Latency)
		return deadlineTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("min-%d", plan.Attempt), options.Target, duration >= 5*time.Millisecond)
	})
	if minimized.Status != "passed" || minimized.Minimization.Replay.Sweep == nil || minimized.Minimization.Replay.Version != 8 {
		t.Fatal("sweep identity lost in minimization")
	}
	best, err := selectTestReplay([]testRunReceipt{minimized}, minimized.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestReplay(best, map[string]testScenario{"checkout": s}, "."); err != nil {
		t.Fatal("minimized sweep replay failed", err)
	}
	dir := t.TempDir()
	html := filepath.Join(dir, "sweep.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(html)
	if !strings.Contains(string(data), "Resilience sweep") || !strings.Contains(string(data), "latency = 500ms") || !strings.Contains(string(data), "Run 2: failed") {
		t.Fatal("HTML missing settings or history")
	}
	xml := filepath.Join(dir, "sweep.xml")
	if err := writeTestJUnit(xml, []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(xml)
	if !strings.Contains(string(data), "sweep-run-6") || !strings.Contains(string(data), "500ms") {
		t.Fatal("JUnit missing replay evidence")
	}
}

func TestFaultSweepCleanupCancellation(t *testing.T) {
	for _, mode := range []string{"cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runSweepTest(ctx, sweepTestScenario("percent"), func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				row := testRunReceipt{Status: "passed"}
				if mode == "cleanup" {
					row.CleanupError = "cleanup failed"
				} else {
					cancel()
				}
				return row
			})
			if calls != 1 || result.Status != "failed" || result.FaultMatrix.Skipped != 2 {
				t.Fatal("sweep continued after stop")
			}
		})
	}
}

func TestFaultSweepOfflineManifestReplay(t *testing.T) {
	dir := t.TempDir()
	s := sweepTestScenario("percent")
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": s}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	normalized := scenarios["checkout"]
	if len(normalized.Resilience.FaultMatrix) != 3 {
		t.Fatal("manifest not expanded")
	}
	t.Setenv("FAAS_TOKEN", "")
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 {
		t.Fatal("offline sweep validation failed")
	}
	variant, err := applyTestFaultMatrixCase(normalized, normalized.Resilience.FaultMatrix[0])
	if err != nil {
		t.Fatal(err)
	}
	variant.Resilience = nil
	leaf := deadlineTestReceipt(variant, "passing-sweep", testMinimizationTarget{Step: "load", Phase: "recovery"}, false)
	leaf.Replay = newTestReplayManifest("checkout", "cold", normalized.Resilience.FaultMatrix[0].Name, variant)
	leaf.Replay.Sweep = normalized.Resilience.FaultMatrix[0].Sweep
	leaf.Replay.Version = 8
	report := filepath.Join(dir, "sweep.json")
	if err := writeTestReport(report, []testRunReceipt{leaf}); err != nil {
		t.Fatal(err)
	}
	if cmdTestReplay([]string{"--from", report, "--run-id", "passing-sweep", "--manifest", manifest, "--validate"}) != 0 {
		t.Fatal("passing sweep replay failed")
	}
	// The unchanged case can replay when other settings change, but its own value is pinned.
	raw := sweepTestScenario("percent")
	raw.Resilience.FaultSweep.Values[0] = "11"
	if _, err := prepareTestReplay(&leaf, map[string]testScenario{"checkout": raw}, dir); err == nil {
		t.Fatal("changed sweep setting accepted")
	}
}
