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

func bandwidthTestScenario() testScenario {
	scenario := minimizationTestScenario()
	rule := &scenario.Steps[1].Load.ChaosSchedule[1].Rules[1]
	rule.Kind = "tcp_bandwidth"
	rule.Latency = ""
	rule.RateKiBPerSecond = 10
	return scenario
}
func bandwidthTestOptions() testFaultMinimizationOptions {
	options := minimizationTestOptions()
	options.Mode = "bandwidth"
	options.RuleIndex = 1
	options.MaxRateKiBPerSecond = 100
	options.RateResolutionKiBPerSecond = 1
	return options
}
func TestBandwidthMinimizationThresholdAndReplay(t *testing.T) {
	for _, variant := range []string{"plain", "removed-and-latency", "matrix"} {
		t.Run(variant, func(t *testing.T) {
			base := bandwidthTestScenario()
			scenario := base
			original := minimizationTestReceipt(scenario, "original", true)
			selectedIndex := 1
			if variant == "removed-and-latency" {
				edits := testFaultReplayOverrides{Latency: []testFaultLatencyOverride{{Rule: testFaultRuleRef{"load", "fault", 2}, Latency: "50ms"}}}
				removed := []testFaultRuleRef{{"load", "fault", 0}}
				var err error
				scenario, err = applyTestFaultReplayEdits(base, removed, edits.Latency)
				if err != nil {
					t.Fatal(err)
				}
				original = minimizationTestReceipt(scenario, "original", true)
				original.Replay = minimizedTestReplay(newTestReplayManifest("checkout", "cold", "", base), scenario, removed, edits)
				selectedIndex = 0
			} else if variant == "matrix" {
				rules := append([]testChaosRule(nil), base.Steps[1].Load.ChaosSchedule[1].Rules...)
				base.Resilience = &testResilienceSpec{Runs: 2, MinPassRate: 1, FaultMatrix: []testFaultMatrixCaseSpec{{Name: "composite", Step: "load", Phase: "fault", Rules: rules}, {Name: "single", Step: "load", Phase: "fault", Rules: rules[:1]}}}
				var err error
				scenario, err = applyTestFaultMatrixCase(base, base.Resilience.FaultMatrix[0])
				if err != nil {
					t.Fatal(err)
				}
				scenario.Resilience = nil
				original = minimizationTestReceipt(scenario, "original", true)
				original.Replay = newTestReplayManifest("checkout", "cold", "composite", scenario)
			}
			prepared, err := prepareTestReplay(&original, map[string]testScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			result := runTestFaultMinimization(context.Background(), prepared, &original, bandwidthTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				rate := plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[selectedIndex].RateKiBPerSecond
				return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), rate <= 60)
			})
			e := result.Minimization
			if result.Status != "passed" || e.Status != "minimized" || e.Bandwidth.BestKiBPerSecond != 60 || e.Bandwidth.PassedAboveKiBPerSecond == nil || *e.Bandwidth.PassedAboveKiBPerSecond != 61 || e.Replay.Version != 4 {
				t.Fatalf("e=%+v bandwidth=%+v", e, e.Bandwidth)
			}
			best, err := selectTestReplay([]testRunReceipt{result}, e.BestRunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := prepareTestReplay(best, map[string]testScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			if replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[selectedIndex].RateKiBPerSecond != 60 || base.Steps[1].Load.ChaosSchedule[1].Rules[1].RateKiBPerSecond != 10 {
				t.Fatal("replay mismatch or source mutated")
			}
			if variant == "removed-and-latency" && replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Latency != "50ms" {
				t.Fatal("inherited latency lost")
			}
			if variant == "matrix" && (e.Replay.MatrixCase != "composite" || replay.Scenario.Resilience != nil) {
				t.Fatal("matrix replay expanded or case lost")
			}
			for _, mutation := range []string{"version", "hash", "increase"} {
				modified := *best
				manifest := *best.Replay
				manifest.BandwidthOverrides = append([]testFaultBandwidthOverride(nil), manifest.BandwidthOverrides...)
				modified.Replay = &manifest
				switch mutation {
				case "version":
					manifest.Version = 3
				case "hash":
					manifest.ReducedConfigurationSHA256 = "invalid"
				case "increase":
					manifest.BandwidthOverrides[0].RateKiBPerSecond++
				}
				if _, err := prepareTestReplay(&modified, map[string]testScenario{"checkout": base}, "."); err == nil {
					t.Fatalf("accepted %s", mutation)
				}
			}
		})
	}
}
func TestBandwidthMinimizationBoundariesAndAbort(t *testing.T) {
	for _, mode := range []string{"ceiling", "unchanged", "budget", "partial-budget", "flaky", "unmatched", "cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			scenario := bandwidthTestScenario()
			original := minimizationTestReceipt(scenario, "original", true)
			options := bandwidthTestOptions()
			if mode == "budget" {
				options.MaxRuns = 2
			}
			if mode == "partial-budget" {
				options.MaxRuns = 6
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runTestFaultMinimization(ctx, testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				rate := plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].RateKiBPerSecond
				failed := true
				if mode == "unchanged" {
					failed = rate == 10
				}
				if mode == "partial-budget" {
					failed = rate <= 60
				}
				if mode == "flaky" {
					failed = calls != 4
				}
				row := minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), failed)
				if calls == 3 {
					switch mode {
					case "unmatched":
						row.Steps[0].Load.ChaosPhases[1].Chaos.Matches[1].Matches = 0
					case "cleanup":
						row.CleanupError = "cleanup failed"
					case "cancel":
						cancel()
					}
				}
				return row
			})
			e := result.Minimization
			switch mode {
			case "ceiling":
				if e.Status != "minimized" || e.Bandwidth.BestKiBPerSecond != 100 || calls != 4 || e.Bandwidth.PassedAboveKiBPerSecond != nil {
					t.Fatalf("%+v", e)
				}
			case "unchanged":
				if e.Status != "unchanged" || e.Bandwidth.BestKiBPerSecond != 10 || *e.Bandwidth.PassedAboveKiBPerSecond != 11 {
					t.Fatalf("%+v", e)
				}
			default:
				if e.Status != "inconclusive" {
					t.Fatalf("%+v", e)
				}
				if mode == "partial-budget" {
					if e.Bandwidth.BestKiBPerSecond != 55 || e.Replay.Version != 4 {
						t.Fatal("confirmed partial result lost")
					}
				} else if e.Bandwidth.BestKiBPerSecond != 10 {
					t.Fatal("unconfirmed result retained")
				}
			}
		})
	}
}
func TestBandwidthReplayRejectsInvalidEditsAndOptions(t *testing.T) {
	scenario := bandwidthTestScenario()
	ref := testFaultRuleRef{"load", "fault", 1}
	for _, rate := range []int64{0, -1, 9, 1000001} {
		if _, err := applyTestFaultReplayEdits(scenario, nil, nil, []testFaultBandwidthOverride{{ref, rate}}); err == nil {
			t.Fatalf("accepted rate %d", rate)
		}
	}
	for _, overrides := range [][]testFaultBandwidthOverride{{{ref, 20}, {ref, 30}}, {{testFaultRuleRef{"load", "fault", 0}, 20}}, {{testFaultRuleRef{"load", "fault", -1}, 20}}, {{testFaultRuleRef{"load", "fault", 30}, 20}}} {
		if _, err := applyTestFaultReplayEdits(scenario, nil, nil, overrides); err == nil {
			t.Fatal("accepted invalid override")
		}
	}
	if _, err := applyTestFaultReplayEdits(scenario, []testFaultRuleRef{ref}, nil, []testFaultBandwidthOverride{{ref, 20}}); err == nil {
		t.Fatal("accepted removed override")
	}
	original := minimizationTestReceipt(scenario, "original", true)
	prepared := testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}
	for _, field := range []string{"kind", "index", "maximum", "resolution"} {
		options := bandwidthTestOptions()
		switch field {
		case "kind":
			options.RuleIndex = 0
		case "index":
			options.RuleIndex = 30
		case "maximum":
			options.MaxRateKiBPerSecond = 10
		case "resolution":
			options.RateResolutionKiBPerSecond = 0
		}
		if _, err := validateTestMinimization(prepared, &original, options); err == nil {
			t.Fatalf("accepted %s", field)
		}
	}
	for _, args := range [][]string{{"--mode", "bandwidth", "--validate"}, {"--mode", "bandwidth", "--rule-index", "1", "--validate"}, {"--mode", "bandwidth", "--rule-index", "1", "--max-rate-kib-per-second", "100", "--resolution", "1ms", "--validate"}, {"--mode", "latency", "--rule-index", "1", "--max-rate-kib-per-second", "100", "--validate"}} {
		if cmdTestMinimize(args) == 0 {
			t.Fatal("accepted invalid mode flags")
		}
	}
}
func TestBandwidthMinimizationCLIAndReports(t *testing.T) {
	dir := t.TempDir()
	scenario := bandwidthTestScenario()
	for name, body := range map[string]string{"package.json": `{"name":"bandwidth-test","version":"1.0.0","scripts":{"start":"node server.js"}}`, "server.js": `console.log("ready")`} {
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
	original := minimizationTestReceipt(scenario, "original", true)
	from := filepath.Join(dir, "failed.json")
	if err := writeTestReport(from, []testRunReceipt{original}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if cmdTestMinimize([]string{"--from", from, "--run-id", "original", "--manifest", manifest, "--assertion", "load/recovery", "--mode", "bandwidth", "--rule-index", "1", "--max-rate-kib-per-second", "100", "--max-workload-minutes", "40", "--validate"}) != 0 {
		t.Fatal("offline validation failed")
	}
	calls := 0
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, bandwidthTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("report-%d", calls), true)
	})
	output := filepath.Join(dir, "bandwidth.json")
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
	if _, err := prepareTestReplay(best, scenarios, dir); err != nil {
		t.Fatal(err)
	}
	html := filepath.Join(dir, "bandwidth.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(html)
	if err != nil || !strings.Contains(string(rendered), "Confirmed bandwidth") || !strings.Contains(string(rendered), "100") {
		t.Fatal("HTML missing bandwidth result")
	}
	if err := writeTestJUnit(filepath.Join(dir, "bandwidth.xml"), []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	if cmdTestReplay([]string{"--from", output, "--run-id", best.RunID, "--manifest", manifest, "--validate"}) != 0 {
		t.Fatal("offline bandwidth replay failed")
	}
	// Continuing latency minimization must retain the earlier bandwidth override.
	replay, err := prepareTestReplay(best, scenarios, dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := minimizationTestOptions()
	opts.Mode = "latency"
	opts.RuleIndex = 2
	opts.Resolution = time.Millisecond
	continuation := runTestFaultMinimization(context.Background(), replay, best, opts, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("continued-%d", calls), true)
	})
	if continuation.Minimization.Replay.Version != 4 || len(continuation.Minimization.Replay.BandwidthOverrides) != 1 || continuation.Minimization.Replay.BandwidthOverrides[0].RateKiBPerSecond != 100 {
		t.Fatal("continuation lost bandwidth")
	}
	selected, err := selectTestReplay([]testRunReceipt{continuation}, continuation.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTestReplay(selected, scenarios, dir); err != nil {
		t.Fatal(err)
	}
}

func TestBandwidthMinimizationReplayCapacityAndSingleRule(t *testing.T) {
	scenario := bandwidthTestScenario()
	scenario.Steps[1].Load.ChaosSchedule[1].Rules = scenario.Steps[1].Load.ChaosSchedule[1].Rules[1:2]
	original := minimizationTestReceipt(scenario, "original", true)
	options := bandwidthTestOptions()
	options.RuleIndex = 0
	prepared := testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}
	calls := 0
	result := runTestFaultMinimization(context.Background(), prepared, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("single-%d", calls), true)
	})
	if result.Status != "passed" || result.Minimization.RemainingRules != 1 || result.Minimization.Bandwidth.BestKiBPerSecond != 100 {
		t.Fatal("single-rule phase rejected")
	}
	manifest := newTestReplayManifest("checkout", "cold", "", scenario)
	for i := 0; i < 20; i++ {
		manifest.BandwidthOverrides = append(manifest.BandwidthOverrides, testFaultBandwidthOverride{Rule: testFaultRuleRef{"other", "fault", i}, RateKiBPerSecond: 20})
	}
	if _, _, err := testBandwidthSelection(scenario, manifest, options); err == nil {
		t.Fatal("accepted unreplayable new override")
	}
	manifest.BandwidthOverrides[0].Rule = testFaultRuleRef{"load", "fault", 0}
	if _, _, err := testBandwidthSelection(scenario, manifest, options); err != nil {
		t.Fatal(err)
	}
	manifest.BandwidthOverrides = append(manifest.BandwidthOverrides, testFaultBandwidthOverride{Rule: testFaultRuleRef{"extra", "fault", 0}, RateKiBPerSecond: 20})
	if _, err := applyTestFaultReplayEdits(scenario, nil, nil, manifest.BandwidthOverrides); err == nil {
		t.Fatal("accepted more than 20 overrides")
	}
}
