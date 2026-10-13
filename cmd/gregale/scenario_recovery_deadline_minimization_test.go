package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func deadlineTestOptions() testFaultMinimizationOptions {
	options := minimizationTestOptions()
	options.Mode = "recovery"
	options.MaxRecoveryWithin = 700 * time.Millisecond
	options.RecoveryResolution = time.Millisecond
	return options
}
func deadlineTestReceipt(scenario testScenario, id string, target testMinimizationTarget, failed bool) testRunReceipt {
	row := minimizationTestReceipt(scenario, id, failed)
	phase := &row.Steps[0].Load.ChaosPhases[2]
	spec, _, _ := testRecoveryDeadlineSpec(scenario, target)
	cfg := resolveTestLoadRecovery(spec)
	selected := *phase.Recovery
	selected.WithinMS = cfg.Within.Milliseconds()
	selected.WindowMS = cfg.Window.Milliseconds()
	selected.SustainMS = cfg.Sustain.Milliseconds()
	if target.Request != "" {
		phase.Recovery.Passed = true
		phase.Recovery.Error = ""
		phase.StepRecovery = map[string]*testLoadRecoveryEvidence{target.Request: &selected}
	} else {
		phase.Recovery = &selected
	}
	return row
}
func TestRecoveryDeadlineThresholdAndReplay(t *testing.T) {
	for _, variant := range []string{"aggregate", "endpoint", "combined", "matrix"} {
		t.Run(variant, func(t *testing.T) {
			base := minimizationTestScenario()
			scenario := base
			options := deadlineTestOptions()
			original := deadlineTestReceipt(scenario, "original", options.Target, true)
			if variant == "endpoint" {
				spec := *base.Steps[1].Load.ChaosSchedule[2].Recovery
				base.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": &spec}
				scenario = base
				options.Target.Request = "load-probe"
				original = deadlineTestReceipt(scenario, "original", options.Target, true)
			}
			if variant == "combined" {
				base = bandwidthTestScenario()
				spec := *base.Steps[1].Load.ChaosSchedule[2].Recovery
				base.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": &spec}
				edits := testFaultReplayOverrides{Latency: []testFaultLatencyOverride{{Rule: testFaultRuleRef{"load", "fault", 2}, Latency: "50ms"}}, Bandwidth: []testFaultBandwidthOverride{{Rule: testFaultRuleRef{"load", "fault", 1}, RateKiBPerSecond: 20}}, Percent: []testFaultPercentOverride{{Rule: testFaultRuleRef{"load", "fault", 1}, Percent: 38}}, Recovery: []testRecoveryDeadlineOverride{{Target: testMinimizationTarget{"load", "recovery", "load-probe"}, Within: "600ms"}}}
				removed := []testFaultRuleRef{{"load", "fault", 0}}
				var err error
				scenario, err = applyTestFaultReplayOverrides(base, removed, edits)
				if err != nil {
					t.Fatal(err)
				}
				original = deadlineTestReceipt(scenario, "original", options.Target, true)
				original.Replay = minimizedTestReplay(newTestReplayManifest("checkout", "cold", "", base), scenario, removed, edits)
			}
			if variant == "matrix" {
				rules := append([]testChaosRule(nil), base.Steps[1].Load.ChaosSchedule[1].Rules...)
				base.Resilience = &testResilienceSpec{Runs: 2, MinPassRate: 1, FaultMatrix: []testFaultMatrixCaseSpec{{Name: "composite", Step: "load", Phase: "fault", Rules: rules}, {Name: "single", Step: "load", Phase: "fault", Rules: rules[:1]}}}
				var err error
				scenario, err = applyTestFaultMatrixCase(base, base.Resilience.FaultMatrix[0])
				if err != nil {
					t.Fatal(err)
				}
				scenario.Resilience = nil
				original = deadlineTestReceipt(scenario, "original", options.Target, true)
				original.Replay = newTestReplayManifest("checkout", "cold", "composite", scenario)
			}
			prepared, err := prepareTestReplay(&original, map[string]testScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			faultFingerprint := newTestReplayManifest("checkout", "cold", "", scenario).ConfigurationSHA256
			result := runTestFaultMinimization(context.Background(), prepared, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				spec, _, err := testRecoveryDeadlineSpec(plan.Prepared.Scenario, options.Target)
				if err != nil {
					t.Fatal(err)
				}
				within, _ := time.ParseDuration(spec.Within)
				return deadlineTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), options.Target, within < 600*time.Millisecond)
			})
			e := result.Minimization
			if result.Status != "passed" || e.Recovery.BestWithinMS == nil || *e.Recovery.BestWithinMS != 600 || e.Recovery.FailedWithinMS != 599 || e.Replay.Version != 6 {
				t.Fatalf("e=%+v recovery=%+v", e, e.Recovery)
			}
			best, err := selectTestReplay([]testRunReceipt{result}, e.BestRunID)
			if err != nil {
				t.Fatal(err)
			}
			if best.Status != "passed" {
				t.Fatal("best deadline is a failed run")
			}
			replay, err := prepareTestReplay(best, map[string]testScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			spec, _, _ := testRecoveryDeadlineSpec(replay.Scenario, options.Target)
			if spec.Within != "600ms" || spec.Sustain != "200ms" || spec.Window != "200ms" {
				t.Fatal("deadline changed other recovery settings")
			}
			if newTestReplayManifest("checkout", "cold", "", scenario).ConfigurationSHA256 != faultFingerprint {
				t.Fatal("source mutated")
			}
			if variant == "endpoint" && base.Steps[1].Load.ChaosSchedule[2].Recovery.Within != "500ms" {
				t.Fatal("aggregate budget changed")
			}
			if variant == "combined" {
				rules := replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules
				if len(rules) != 2 || rules[0].RateKiBPerSecond != 20 || rules[0].Percent != 38 || rules[1].Latency != "50ms" || len(best.Replay.RecoveryOverrides) != 2 {
					t.Fatal("inherited reductions lost")
				}
				failed, err := selectTestReplay([]testRunReceipt{result}, e.Recovery.FailedRunID)
				if err != nil {
					t.Fatal(err)
				}
				failPrepared, err := prepareTestReplay(failed, map[string]testScenario{"checkout": base}, ".")
				if err != nil {
					t.Fatal(err)
				}
				percentOptions := percentTestOptions()
				next := runTestFaultMinimization(context.Background(), failPrepared, failed, percentOptions, func(ctx context.Context, plan testRunPlan) testRunReceipt {
					calls++
					return deadlineTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("next-%d", calls), options.Target, true)
				})
				if next.Status != "passed" || len(next.Minimization.Replay.RecoveryOverrides) != 2 || next.Minimization.Replay.Version != 6 {
					t.Fatal("later severity reduction lost recovery overrides")
				}
				selected, err := selectTestReplay([]testRunReceipt{next}, next.Minimization.BestRunID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := prepareTestReplay(selected, map[string]testScenario{"checkout": base}, "."); err != nil {
					t.Fatal(err)
				}
			}
			if variant == "matrix" && (best.Replay.MatrixCase != "composite" || replay.Scenario.Resilience != nil) {
				t.Fatal("matrix replay expanded or case lost")
			}
			for _, kind := range []string{"old-version", "bad-hash", "unsupported-version", "changed-deadline"} {
				changed := *best
				manifest := *best.Replay
				manifest.RecoveryOverrides = append([]testRecoveryDeadlineOverride(nil), manifest.RecoveryOverrides...)
				changed.Replay = &manifest
				switch kind {
				case "old-version":
					manifest.Version = 5
				case "bad-hash":
					manifest.ReducedConfigurationSHA256 = "invalid"
				case "unsupported-version":
					manifest.Version = 8
				case "changed-deadline":
					manifest.RecoveryOverrides[len(manifest.RecoveryOverrides)-1].Within = "601ms"
				}
				if _, err := prepareTestReplay(&changed, map[string]testScenario{"checkout": base}, "."); err == nil {
					t.Fatalf("accepted %s", kind)
				}
			}
		})
	}
}
func TestRecoveryDeadlineAbortAndPartialResults(t *testing.T) {
	for _, mode := range []string{"ceiling-fails", "budget", "partial-budget", "flaky", "cleanup", "cancel", "unmatched", "mismatched-evidence", "unrelated", "coarse"} {
		t.Run(mode, func(t *testing.T) {
			scenario := minimizationTestScenario()
			options := deadlineTestOptions()
			if mode == "budget" {
				options.MaxRuns = 2
			}
			if mode == "partial-budget" {
				options.MaxRuns = 6
			}
			if mode == "coarse" {
				options.RecoveryResolution = 25 * time.Millisecond
			}
			original := deadlineTestReceipt(scenario, "original", options.Target, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runTestFaultMinimization(ctx, testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				spec, _, _ := testRecoveryDeadlineSpec(plan.Prepared.Scenario, options.Target)
				within, _ := time.ParseDuration(spec.Within)
				failed := within < 600*time.Millisecond
				if mode == "ceiling-fails" {
					failed = true
				}
				if mode == "flaky" && calls == 4 {
					failed = true
				}
				row := deadlineTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), options.Target, failed)
				if calls == 3 {
					switch mode {
					case "cleanup":
						row.CleanupError = "cleanup failed"
					case "cancel":
						cancel()
					case "unmatched":
						for i := range row.Steps[0].Load.ChaosPhases[1].Chaos.Matches {
							row.Steps[0].Load.ChaosPhases[1].Chaos.Matches[i].Matches = 0
						}
					case "mismatched-evidence":
						row.Steps[0].Load.ChaosPhases[2].Recovery.WithinMS++
					case "unrelated":
						row.Status = "failed"
						row.Error = "another step failed"
					}
				}
				return row
			})
			e := result.Minimization
			if mode == "coarse" {
				if result.Status != "passed" || *e.Recovery.BestWithinMS != 600 || e.Recovery.FailedWithinMS != 575 {
					t.Fatalf("%+v", e)
				}
				return
			}
			if e.Status != "inconclusive" {
				t.Fatalf("%+v", e)
			}
			if mode == "partial-budget" {
				if e.Recovery.BestWithinMS == nil || *e.Recovery.BestWithinMS != 600 || e.BestRunID == "" || e.Replay.Version != 6 {
					t.Fatal("confirmed passing result lost")
				}
			} else if e.Recovery.BestWithinMS != nil || e.BestRunID != "" || e.Replay != nil {
				t.Fatal("unconfirmed passing result retained")
			}
		})
	}
}
func TestRecoveryDeadlineReplayValidationAndCLI(t *testing.T) {
	scenario := minimizationTestScenario()
	options := deadlineTestOptions()
	target := options.Target
	for _, within := range []string{"0ms", "99ms", "100us", "199ms", "801ms", "6m", "invalid"} {
		if _, err := applyTestFaultReplayOverrides(scenario, nil, testFaultReplayOverrides{Recovery: []testRecoveryDeadlineOverride{{Target: target, Within: within}}}); err == nil {
			t.Fatalf("accepted %s", within)
		}
	}
	for _, overrides := range [][]testRecoveryDeadlineOverride{{{Target: target, Within: "600ms"}, {Target: target, Within: "700ms"}}, {{Target: testMinimizationTarget{"missing", "recovery", ""}, Within: "600ms"}}, {{Target: testMinimizationTarget{"load", "recovery", "missing"}, Within: "600ms"}}} {
		if _, err := applyTestRecoveryDeadlineOverrides(scenario, overrides); err == nil {
			t.Fatal("accepted invalid target")
		}
	}
	original := deadlineTestReceipt(scenario, "original", target, true)
	prepared := testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}
	for _, field := range []string{"maximum", "phase-fit", "resolution"} {
		changed := options
		switch field {
		case "maximum":
			changed.MaxRecoveryWithin = 500 * time.Millisecond
		case "phase-fit":
			changed.MaxRecoveryWithin = 801 * time.Millisecond
		case "resolution":
			changed.RecoveryResolution = 0
		}
		if _, err := validateTestMinimization(prepared, &original, changed); err == nil {
			t.Fatalf("accepted %s", field)
		}
	}
	manifest := *original.Replay
	for i := 0; i < 20; i++ {
		manifest.RecoveryOverrides = append(manifest.RecoveryOverrides, testRecoveryDeadlineOverride{Target: testMinimizationTarget{"other", "recovery", fmt.Sprintf("probe-%d", i)}, Within: "600ms"})
	}
	if _, _, err := testRecoveryDeadlineSelection(scenario, &manifest, options); err == nil {
		t.Fatal("accepted unreplayable added override")
	}
	manifest.RecoveryOverrides[0].Target = target
	if _, _, err := testRecoveryDeadlineSelection(scenario, &manifest, options); err != nil {
		t.Fatal(err)
	}
	manifest.RecoveryOverrides = append(manifest.RecoveryOverrides, testRecoveryDeadlineOverride{Target: testMinimizationTarget{"extra", "recovery", ""}, Within: "600ms"})
	if _, err := applyTestRecoveryDeadlineOverrides(scenario, manifest.RecoveryOverrides); err == nil {
		t.Fatal("accepted more than 20 overrides")
	}
	for _, args := range [][]string{{"--mode", "recovery", "--validate"}, {"--mode", "recovery", "--max-recovery-within", "700ms", "--rule-index", "1", "--validate"}, {"--mode", "percent", "--rule-index", "1", "--max-recovery-within", "700ms", "--validate"}, {"--mode", "recovery", "--max-recovery-within", "700ms", "--recovery-resolution", "0ms", "--validate"}} {
		if cmdTestMinimize(args) == 0 {
			t.Fatal("accepted invalid recovery flags")
		}
	}
	// A normal passing run remains outside failure replay selection.
	passed := deadlineTestReceipt(scenario, "passed", target, false)
	if _, err := selectTestReplay([]testRunReceipt{passed}, "passed"); err == nil {
		t.Fatal("ordinary success became replayable")
	}
}
func TestRecoveryDeadlineReportsAndOfflineReplay(t *testing.T) {
	dir := t.TempDir()
	scenario := minimizationTestScenario()
	for name, body := range map[string]string{"package.json": `{"name":"deadline-test","version":"1.0.0","scripts":{"start":"node server.js"}}`, "server.js": `console.log("ready")`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": scenario}})
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
	scenario = scenarios["checkout"]
	options := deadlineTestOptions()
	original := deadlineTestReceipt(scenario, "original", options.Target, true)
	from := filepath.Join(dir, "failed.json")
	if err := writeTestReport(from, []testRunReceipt{original}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if cmdTestMinimize([]string{"--from", from, "--run-id", "original", "--manifest", manifest, "--assertion", "load/recovery", "--mode", "recovery", "--max-recovery-within", "700ms", "--max-workload-minutes", "40", "--validate"}) != 0 {
		t.Fatal("offline deadline validation failed")
	}
	calls := 0
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		spec, _, _ := testRecoveryDeadlineSpec(plan.Prepared.Scenario, options.Target)
		within, _ := time.ParseDuration(spec.Within)
		return deadlineTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("report-%d", calls), options.Target, within < 600*time.Millisecond)
	})
	output := filepath.Join(dir, "deadline.json")
	if err := writeTestReport(output, []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := readTestBaselineReport(output, nil)
	if err != nil {
		t.Fatal(err)
	}
	best, err := selectTestReplay(rows, result.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	if best.Status != "passed" {
		t.Fatal("best deadline was not passing")
	}
	if _, err := prepareTestReplay(best, scenarios, dir); err != nil {
		t.Fatal(err)
	}
	if cmdTestReplay([]string{"--from", output, "--run-id", best.RunID, "--manifest", manifest, "--validate"}) != 0 {
		t.Fatal("offline passing replay failed")
	}
	if _, err := selectTestReplay(append(rows, *best), best.RunID); err == nil {
		t.Fatal("duplicate passing replay IDs accepted")
	}
	html := filepath.Join(dir, "deadline.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(html)
	if err != nil || !strings.Contains(string(rendered), "Shortest confirmed passing deadline: 600 ms") {
		t.Fatal("HTML missing passing boundary")
	}
	junit := filepath.Join(dir, "deadline.xml")
	if err := writeTestJUnit(junit, []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	rendered, err = os.ReadFile(junit)
	if err != nil || !strings.Contains(string(rendered), "best_within_ms") {
		t.Fatal("JUnit missing deadline evidence")
	}
}
