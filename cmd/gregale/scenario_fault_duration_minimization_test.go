package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func durationMinScenario() testScenario {
	s := minimizationTestScenario()
	s.Steps[1].Load.ChaosSchedule[1].Duration = "9s"
	s.Steps[1].Load.Duration = "11s"
	return s
}
func durationMinReceipt(s testScenario, id string, target testMinimizationTarget, failed bool) testRunReceipt {
	r := deadlineTestReceipt(s, id, target, failed)
	start := r.Steps[0].Load.ChaosPhases[0].StartedAt
	var offset time.Duration
	for i, p := range s.Steps[1].Load.ChaosSchedule {
		d, _ := time.ParseDuration(p.Duration)
		evidence := &r.Steps[0].Load.ChaosPhases[i]
		evidence.PlannedStartMS = offset.Milliseconds()
		evidence.StartedAt = start.Add(offset)
		offset += d
		evidence.PlannedEndMS = offset.Milliseconds()
		evidence.FinishedAt = start.Add(offset)
	}
	return r
}
func durationMinOptions() testFaultMinimizationOptions {
	o := minimizationTestOptions()
	o.Mode = "duration"
	o.DurationResolution = 100 * time.Millisecond
	return o
}

func TestDurationMinimizationThresholdReplay(t *testing.T) {
	for _, endpoint := range []bool{false, true} {
		t.Run(fmt.Sprint(endpoint), func(t *testing.T) {
			base := durationMinScenario()
			options := durationMinOptions()
			if endpoint {
				options.Target.Request = "load-probe"
				base.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": base.Steps[1].Load.ChaosSchedule[2].Recovery}
			}
			before, _ := json.Marshal(base)
			original := durationMinReceipt(base, "original", options.Target, true)
			calls := 0
			result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: base}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				load := plan.Prepared.Scenario.Steps[1].Load
				d, _ := time.ParseDuration(load.ChaosSchedule[1].Duration)
				if load.Duration != (d+2*time.Second).String() || !reflect.DeepEqual(load.ChaosSchedule[1].Rules, base.Steps[1].Load.ChaosSchedule[1].Rules) || !reflect.DeepEqual(load.ChaosSchedule[2], base.Steps[1].Load.ChaosSchedule[2]) {
					t.Fatal("fixed configuration changed")
				}
				return durationMinReceipt(plan.Prepared.Scenario, fmt.Sprintf("duration-min-%d", calls), options.Target, d >= 5*time.Second)
			})
			e := result.Minimization
			if result.Status != "passed" || e.Duration.BestMS != 5000 || *e.Duration.PassedBelowMS != 4937 || e.Replay.Version != 9 || e.RunsUsed%2 != 0 {
				t.Fatalf("evidence=%+v duration=%+v", e, e.Duration)
			}
			best, err := selectTestReplay([]testRunReceipt{result}, e.BestRunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := prepareTestReplay(best, map[string]testScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			if replay.Scenario.Steps[1].Load.Duration != "7s" || replay.Scenario.Steps[1].Load.ChaosSchedule[1].Duration != "5s" {
				t.Fatal("replay duration mismatch")
			}
			after, _ := json.Marshal(base)
			if string(before) != string(after) {
				t.Fatal("source mutated")
			}
			for _, bad := range []string{"duration", "version", "hash"} {
				row := *best
				m := *best.Replay
				m.DurationOverrides = append([]testFaultDurationOverride(nil), m.DurationOverrides...)
				row.Replay = &m
				switch bad {
				case "duration":
					m.DurationOverrides[0].Duration = "4s"
				case "version":
					m.Version = 8
				case "hash":
					m.ReducedConfigurationSHA256 = strings.Repeat("0", 64)
				}
				if _, err := prepareTestReplay(&row, map[string]testScenario{"checkout": base}, "."); err == nil {
					t.Fatalf("tampering accepted: %s", bad)
				}
			}
			passing := e.Trials[1].Runs[0]
			selected, err := selectTestReplay([]testRunReceipt{result}, passing.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := prepareTestReplay(selected, map[string]testScenario{"checkout": base}, "."); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "duration.html")
			if err := writeTestHTMLReport(path, "", []testRunReceipt{result}); err != nil {
				t.Fatal(err)
			}
			body, _ := os.ReadFile(path)
			if !strings.Contains(string(body), "Fault duration load/fault: 9000") {
				t.Fatal("missing duration evidence")
			}
		})
	}
}

func TestDurationMinimizationStopsAndRetainsBest(t *testing.T) {
	for _, mode := range []string{"floor", "budget", "flaky", "unmatched", "cleanup", "cancel", "wrong-duration", "wrong-phase"} {
		t.Run(mode, func(t *testing.T) {
			s := durationMinScenario()
			opts := durationMinOptions()
			original := durationMinReceipt(s, "original", opts.Target, true)
			if mode == "budget" {
				opts.MaxRuns = 7
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runTestFaultMinimization(ctx, testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: s}, &original, opts, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				row := durationMinReceipt(plan.Prepared.Scenario, fmt.Sprintf("stop-%d", calls), opts.Target, (mode != "flaky" || calls != 4) && (mode != "budget" || plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Duration != "1s"))
				if calls == 3 {
					switch mode {
					case "unmatched":
						for i := range row.Steps[0].Load.ChaosPhases[1].Chaos.Matches {
							row.Steps[0].Load.ChaosPhases[1].Chaos.Matches[i].Matches = 0
						}
					case "cleanup":
						row.CleanupError = "cleanup failed"
					case "cancel":
						cancel()
					case "wrong-phase":
						row.Steps[0].Load.ChaosPhases[1].Name = "other"
					case "wrong-duration":
						row.Steps[0].Load.ChaosPhases[1].PlannedEndMS++
					}
				}
				return row
			})
			e := result.Minimization
			if mode == "budget" {
				if e.Status != "inconclusive" || e.Duration.BestMS != 5000 || e.BestRunID != "stop-6" || calls != 6 {
					t.Fatalf("%+v", e)
				}
			} else if mode == "floor" {
				if result.Status != "passed" || e.Duration.BestMS != 1000 || calls != 4 {
					t.Fatalf("%+v", e)
				}
			} else if e.Status != "inconclusive" || e.BestRunID != "stop-2" || calls > 4 {
				t.Fatalf("%+v", e)
			}
		})
	}
}

func TestDurationMinimizationInvalidSelectionAndOverrides(t *testing.T) {
	for _, kind := range []string{"stages", "iterations", "resolution", "original-duration", "cleanup", "unmatched", "wrong-evidence", "missing-recovery", "limit"} {
		t.Run(kind, func(t *testing.T) {
			s := durationMinScenario()
			o := durationMinOptions()
			original := durationMinReceipt(s, "original", o.Target, true)
			switch kind {
			case "stages":
				s.Steps[1].Load.Stages = []testLoadStageSpec{{}}
			case "iterations":
				s.Steps[1].Load.Duration = ""
			case "resolution":
				o.DurationResolution = time.Microsecond
			case "original-duration":
				s.Steps[1].Load.ChaosSchedule[1].Duration = "0s"
			case "cleanup":
				original.CleanupError = "cleanup failed"
			case "unmatched":
				for i := range original.Steps[0].Load.ChaosPhases[1].Chaos.Matches {
					original.Steps[0].Load.ChaosPhases[1].Chaos.Matches[i].Matches = 0
				}
			case "wrong-evidence":
				original.Steps[0].Load.ChaosPhases[1].PlannedEndMS++
			case "missing-recovery":
				s.Steps[1].Load.ChaosSchedule[2].Recovery = nil
			case "limit":
				for i := 0; i < 20; i++ {
					original.Replay.DurationOverrides = append(original.Replay.DurationOverrides, testFaultDurationOverride{Step: fmt.Sprintf("other-%d", i), Phase: "fault", Duration: "1s"})
				}
			}
			if _, err := validateTestMinimization(testPreparedScenario{Engine: "real-vm", Scenario: s}, &original, o); err == nil {
				t.Fatal("invalid duration selection accepted")
			}
		})
	}
	base := durationMinScenario()
	for _, overrides := range [][]testFaultDurationOverride{
		{{Step: "load", Phase: "fault", Duration: "10s"}},
		{{Step: "load", Phase: "fault", Duration: "999ms"}},
		{{Step: "load", Phase: "fault", Duration: "1.0001s"}},
		{{Step: "load", Phase: "healthy", Duration: "1s"}},
		{{Step: "absent", Phase: "fault", Duration: "1s"}},
		{{Step: "load", Phase: "fault", Duration: "1s"}, {Step: "load", Phase: "fault", Duration: "2s"}},
	} {
		if _, err := applyTestFaultDurationOverrides(base, overrides); err == nil {
			t.Fatal("invalid replay override accepted")
		}
	}
	for _, args := range [][]string{
		{"--mode", "duration", "--rule-index", "0"},
		{"--mode", "duration", "--resolution", "1ms"},
		{"--mode", "latency", "--duration-resolution", "100ms"},
		{"--mode", "duration", "--duration-resolution", "1us"},
	} {
		if cmdTestMinimize(args) == 0 {
			t.Fatal("invalid CLI flags accepted")
		}
	}
}

func TestDurationMinimizationInheritedEditsAndSweep(t *testing.T) {
	for _, sweep := range []bool{false, true} {
		t.Run(fmt.Sprint(sweep), func(t *testing.T) {
			base := durationMinScenario()
			caseName := ""
			var setting *testFaultSweepSetting
			if sweep {
				base = sweepTestScenario("duration")
				expanded, err := expandTestFaultSweep(base)
				if err != nil {
					t.Fatal(err)
				}
				c := expanded.Resilience.FaultMatrix[2]
				variant, err := applyTestFaultMatrixCase(expanded, c)
				if err != nil {
					t.Fatal(err)
				}
				caseName = c.Name
				setting = c.Sweep
				baseVariant := variant
				baseVariant.Resilience = nil
				// The replay source retains its generated sweep case.
				originalManifest := newTestReplayManifest("checkout", "cold", caseName, baseVariant)
				originalManifest.Sweep = setting
				originalManifest.Version = 8
				testDurationInheritedCase(t, base, baseVariant, originalManifest)
			} else {
				testDurationInheritedCase(t, base, base, newTestReplayManifest("checkout", "cold", caseName, base))
			}
		})
	}
}
func testDurationInheritedCase(t *testing.T, base, variant testScenario, manifest *testReplayManifest) {
	t.Helper()
	edits := testFaultReplayOverrides{Latency: []testFaultLatencyOverride{{Rule: testFaultRuleRef{"load", "fault", 1}, Latency: "60ms"}}, Duration: []testFaultDurationOverride{{Step: "load", Phase: "fault", Duration: "7s"}}, Recovery: []testRecoveryDeadlineOverride{{Target: testMinimizationTarget{Step: "load", Phase: "recovery"}, Within: "400ms"}}}
	removed := []testFaultRuleRef{{"load", "fault", 0}}
	effective, err := applyTestFaultReplayOverrides(variant, removed, edits)
	if err != nil {
		t.Fatal(err)
	}
	options := durationMinOptions()
	original := durationMinReceipt(effective, "inherited-original", options.Target, true)
	original.Replay = minimizedTestReplay(manifest, effective, removed, edits)
	prepared, err := prepareTestReplay(&original, map[string]testScenario{"checkout": base}, ".")
	if err != nil {
		t.Fatal(err)
	}
	result := runTestFaultMinimization(context.Background(), prepared, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		return durationMinReceipt(plan.Prepared.Scenario, fmt.Sprintf("inherited-%d", plan.Attempt), options.Target, true)
	})
	e := result.Minimization
	if result.Status != "passed" || e.Duration.BestMS != 1000 || e.Replay.Version != 9 || len(e.Replay.RemovedRules) != 1 || len(e.Replay.LatencyOverrides) != 1 || len(e.Replay.RecoveryOverrides) != 1 || !reflect.DeepEqual(e.Replay.Sweep, manifest.Sweep) {
		t.Fatalf("inherited edits lost: %+v", e)
	}
	best, err := selectTestReplay([]testRunReceipt{result}, e.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := prepareTestReplay(best, map[string]testScenario{"checkout": base}, ".")
	if err != nil {
		t.Fatal(err)
	}
	load := replay.Scenario.Steps[1].Load
	if load.Duration != "3s" || len(load.ChaosSchedule[1].Rules) != 2 || load.ChaosSchedule[1].Rules[0].Latency != "60ms" || load.ChaosSchedule[2].Recovery.Within != "400ms" {
		t.Fatal("inherited replay configuration changed")
	}
	// Continue a real latency search from the duration-minimized replay.
	nextOptions := minimizationTestOptions()
	nextOptions.Mode = "latency"
	nextOptions.RuleIndex = 1
	nextOptions.Resolution = time.Millisecond
	continued := runTestFaultMinimization(context.Background(), replay, best, nextOptions, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		return durationMinReceipt(plan.Prepared.Scenario, fmt.Sprintf("continued-%d", plan.Attempt), options.Target, true)
	})
	if continued.Status != "passed" || continued.Minimization.Latency.BestMS != 1 || !reflect.DeepEqual(continued.Minimization.Replay.DurationOverrides, e.Replay.DurationOverrides) {
		t.Fatalf("continuing search dropped duration: %+v", continued.Minimization)
	}
	continuedBest, err := selectTestReplay([]testRunReceipt{continued}, continued.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestReplay(continuedBest, map[string]testScenario{"checkout": base}, "."); err != nil {
		t.Fatal(err)
	}
}

func TestDurationMinimizationOfflineCLI(t *testing.T) {
	dir := t.TempDir()
	s := durationMinScenario()
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
	normalized, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	original := durationMinReceipt(normalized["checkout"], "duration-cli-original", durationMinOptions().Target, true)
	report := filepath.Join(dir, "original.json")
	if err := writeTestReport(report, []testRunReceipt{original}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if cmdTestMinimize([]string{"--from", report, "--run-id", original.RunID, "--manifest", manifest, "--assertion", "load/recovery", "--mode", "duration", "--max-runs", "20", "--max-workload-minutes", "180", "--validate"}) != 0 {
		t.Fatal("offline duration minimization validation failed")
	}
}
