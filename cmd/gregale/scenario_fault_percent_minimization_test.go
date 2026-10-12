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

func percentTestOptions() testFaultMinimizationOptions {
	options := minimizationTestOptions()
	options.Mode = "percent"
	options.RuleIndex = 1
	options.PercentResolution = 1
	return options
}
func percentTestScenario(kind string) testScenario {
	scenario := minimizationTestScenario()
	rule := &scenario.Steps[1].Load.ChaosSchedule[1].Rules[1]
	rule.Kind = kind
	rule.Seed = 42
	rule.Latency = ""
	rule.Port = 0
	switch kind {
	case "latency", "dns_latency":
		rule.Latency = "100ms"
	case "http_status":
		rule.StatusCode = 503
	case "tcp_latency":
		rule.Latency = "100ms"
		rule.Port = 5432
	case "tcp_bandwidth":
		rule.RateKiBPerSecond = 10
		rule.Port = 5432
	case "tcp_timeout", "tcp_reset", "tcp_connect_timeout", "tcp_connect_refused":
		rule.Port = 5432
	}
	return scenario
}
func TestPercentMinimizationThresholdAcrossFaultKinds(t *testing.T) {
	for _, kind := range []string{"latency", "http_status", "tcp_latency", "tcp_bandwidth", "tcp_timeout", "tcp_reset", "tcp_connect_timeout", "tcp_connect_refused", "dns_latency", "dns_timeout", "dns_servfail", "dns_nxdomain"} {
		t.Run(kind, func(t *testing.T) {
			scenario := percentTestScenario(kind)
			original := minimizationTestReceipt(scenario, "original", true)
			calls := 0
			result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, percentTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				rule := plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[1]
				if rule.Seed != 42 {
					t.Fatal("selection seed changed")
				}
				return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), rule.Percent >= 38)
			})
			e := result.Minimization
			if result.Status != "passed" || e.Status != "minimized" || e.Percent.BestPercent != 38 || e.Percent.PassedBelowPercent == nil || *e.Percent.PassedBelowPercent != 37 || e.Replay.Version != 5 {
				t.Fatalf("e=%+v percent=%+v", e, e.Percent)
			}
			best, err := selectTestReplay([]testRunReceipt{result}, e.BestRunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := prepareTestReplay(best, map[string]testScenario{"checkout": scenario}, ".")
			if err != nil {
				t.Fatal(err)
			}
			if replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Percent != 38 || scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Percent != 100 {
				t.Fatal("replay mismatch or source mutated")
			}
		})
	}
}
func TestPercentMinimizationInheritedEditsAndMatrix(t *testing.T) {
	for _, variant := range []string{"combined", "matrix"} {
		t.Run(variant, func(t *testing.T) {
			base := percentTestScenario("tcp_bandwidth")
			scenario := base
			original := minimizationTestReceipt(scenario, "original", true)
			index := 1
			if variant == "combined" {
				removed := []testFaultRuleRef{{"load", "fault", 0}}
				edits := testFaultReplayOverrides{Latency: []testFaultLatencyOverride{{Rule: testFaultRuleRef{"load", "fault", 2}, Latency: "50ms"}}, Bandwidth: []testFaultBandwidthOverride{{Rule: testFaultRuleRef{"load", "fault", 1}, RateKiBPerSecond: 20}}}
				var err error
				scenario, err = applyTestFaultReplayOverrides(base, removed, edits)
				if err != nil {
					t.Fatal(err)
				}
				original = minimizationTestReceipt(scenario, "original", true)
				original.Replay = minimizedTestReplay(newTestReplayManifest("checkout", "cold", "", base), scenario, removed, edits)
				index = 0
			} else {
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
			result := runTestFaultMinimization(context.Background(), prepared, &original, percentTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("percent-%d", calls), plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[index].Percent >= 38)
			})
			best, err := selectTestReplay([]testRunReceipt{result}, result.Minimization.BestRunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := prepareTestReplay(best, map[string]testScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			if result.Minimization.Percent.BestPercent != 38 || replay.Scenario.Resilience != nil {
				t.Fatal("reduction or matrix isolation lost")
			}
			if variant == "matrix" {
				if best.Replay.MatrixCase != "composite" {
					t.Fatal("matrix case lost")
				}
				return
			}
			rules := replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules
			if len(rules) != 2 || rules[0].RateKiBPerSecond != 20 || rules[1].Latency != "50ms" {
				t.Fatal("inherited edits lost")
			}
			// Later bandwidth and latency searches retain the percentage override.
			for _, mode := range []string{"bandwidth", "latency", "percent"} {
				options := percentTestOptions()
				options.Mode = mode
				if mode == "bandwidth" {
					options.MaxRateKiBPerSecond = 100
					options.RateResolutionKiBPerSecond = 1
				}
				if mode == "latency" {
					options.RuleIndex = 2
					options.Resolution = time.Millisecond
				}
				continued := runTestFaultMinimization(context.Background(), replay, best, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
					calls++
					return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("continued-%d", calls), true)
				})
				if continued.Status != "passed" || continued.Minimization.Replay.Version != 5 || len(continued.Minimization.Replay.PercentOverrides) != 1 {
					t.Fatalf("continuation=%+v", continued.Minimization)
				}
				selected, err := selectTestReplay([]testRunReceipt{continued}, continued.Minimization.BestRunID)
				if err != nil {
					t.Fatal(err)
				}
				next, err := prepareTestReplay(selected, map[string]testScenario{"checkout": base}, ".")
				if err != nil {
					t.Fatal(err)
				}
				expected := 38
				if mode == "percent" {
					expected = 1
				}
				if next.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[0].Percent != expected {
					t.Fatal("percentage lost or not replaced")
				}
			}
			for _, mutation := range []string{"old-version", "bad-hash", "changed-percent", "unsupported-version"} {
				changed := *best
				manifest := *best.Replay
				manifest.PercentOverrides = append([]testFaultPercentOverride(nil), manifest.PercentOverrides...)
				changed.Replay = &manifest
				switch mutation {
				case "old-version":
					manifest.Version = 4
				case "bad-hash":
					manifest.ReducedConfigurationSHA256 = "invalid"
				case "changed-percent":
					manifest.PercentOverrides[0].Percent++
				case "unsupported-version":
					manifest.Version = 8
				}
				if _, err := prepareTestReplay(&changed, map[string]testScenario{"checkout": base}, "."); err == nil {
					t.Fatalf("accepted %s", mutation)
				}
			}
		})
	}
}
func TestPercentMinimizationBoundariesAndAbort(t *testing.T) {
	for _, mode := range []string{"floor", "already-floor", "unchanged", "coarse", "budget", "workload-budget", "partial-budget", "flaky", "unmatched", "cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			scenario := percentTestScenario("http_status")
			if mode == "already-floor" {
				scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Percent = 1
			}
			original := minimizationTestReceipt(scenario, "original", true)
			options := percentTestOptions()
			if mode == "coarse" {
				options.PercentResolution = 10
			}
			if mode == "budget" {
				options.MaxRuns = 2
			}
			if mode == "partial-budget" {
				options.MaxRuns = 6
			}
			if mode == "workload-budget" {
				options.MaxWorkloadMinutes = 2 * estimateTestWorkloadMinutes(scenario, 1)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runTestFaultMinimization(ctx, testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				percent := plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Percent
				failed := true
				switch mode {
				case "unchanged":
					failed = percent == 100
				case "coarse", "partial-budget":
					failed = percent >= 38
				case "flaky":
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
			case "floor":
				if e.Status != "minimized" || e.Percent.BestPercent != 1 || calls != 4 {
					t.Fatalf("%+v", e)
				}
			case "already-floor":
				if e.Status != "unchanged" || e.Percent.BestPercent != 1 || calls != 2 {
					t.Fatalf("%+v", e)
				}
			case "unchanged":
				if e.Status != "unchanged" || e.Percent.BestPercent != 100 || *e.Percent.PassedBelowPercent != 99 {
					t.Fatalf("%+v", e)
				}
			case "coarse":
				if e.Status != "minimized" || e.Percent.BestPercent != 43 || *e.Percent.PassedBelowPercent != 37 {
					t.Fatalf("%+v", e)
				}
			default:
				if e.Status != "inconclusive" {
					t.Fatalf("%+v", e)
				}
				expected := 100
				if mode == "partial-budget" {
					expected = 50
				}
				if e.Percent.BestPercent != expected {
					t.Fatal("unconfirmed or partial result mishandled")
				}
			}
		})
	}
}
func TestPercentReplayValidationAndCapacity(t *testing.T) {
	scenario := percentTestScenario("http_status")
	scenario.Steps[1].Load.ChaosSchedule[1].Rules[1].Percent = 50
	ref := testFaultRuleRef{"load", "fault", 1}
	for _, percent := range []int{-1, 0, 51, 101} {
		if _, err := applyTestFaultReplayOverrides(scenario, nil, testFaultReplayOverrides{Percent: []testFaultPercentOverride{{ref, percent}}}); err == nil {
			t.Fatalf("accepted %d", percent)
		}
	}
	for _, edits := range [][]testFaultPercentOverride{{{ref, 20}, {ref, 10}}, {{testFaultRuleRef{"load", "fault", -1}, 10}}, {{testFaultRuleRef{"load", "fault", 30}, 10}}, {{testFaultRuleRef{"missing", "fault", 0}, 10}}} {
		if _, err := applyTestFaultReplayOverrides(scenario, nil, testFaultReplayOverrides{Percent: edits}); err == nil {
			t.Fatal("accepted invalid refs")
		}
	}
	if _, err := applyTestFaultReplayOverrides(scenario, []testFaultRuleRef{ref}, testFaultReplayOverrides{Percent: []testFaultPercentOverride{{ref, 10}}}); err == nil {
		t.Fatal("accepted removed override")
	}
	manifest := newTestReplayManifest("checkout", "cold", "", scenario)
	options := percentTestOptions()
	for i := 0; i < 20; i++ {
		manifest.PercentOverrides = append(manifest.PercentOverrides, testFaultPercentOverride{Rule: testFaultRuleRef{"other", "fault", i}, Percent: 10})
	}
	if _, _, err := testPercentSelection(scenario, manifest, options); err == nil {
		t.Fatal("accepted unreplayable additional override")
	}
	manifest.PercentOverrides[0].Rule = ref
	if _, _, err := testPercentSelection(scenario, manifest, options); err != nil {
		t.Fatal(err)
	}
	manifest.PercentOverrides = append(manifest.PercentOverrides, testFaultPercentOverride{Rule: testFaultRuleRef{"extra", "fault", 0}, Percent: 10})
	if _, err := applyTestFaultReplayOverrides(scenario, nil, testFaultReplayOverrides{Percent: manifest.PercentOverrides}); err == nil {
		t.Fatal("accepted more than 20 overrides")
	}
	for _, args := range [][]string{{"--mode", "percent", "--validate"}, {"--mode", "percent", "--rule-index", "1", "--percent-resolution", "0", "--validate"}, {"--mode", "percent", "--rule-index", "1", "--resolution", "1ms", "--validate"}, {"--mode", "bandwidth", "--percent-resolution", "1", "--validate"}, {"--mode", "percent", "--rule-index", "1", "--max-rate-kib-per-second", "100", "--validate"}} {
		if cmdTestMinimize(args) == 0 {
			t.Fatal("accepted invalid flags")
		}
	}
}
func TestPercentMinimizationCLIAndReports(t *testing.T) {
	dir := t.TempDir()
	scenario := percentTestScenario("http_status")
	for name, body := range map[string]string{"package.json": `{"name":"percent-test","version":"1.0.0","scripts":{"start":"node server.js"}}`, "server.js": `console.log("ready")`} {
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
	if cmdTestMinimize([]string{"--from", from, "--run-id", "original", "--manifest", manifest, "--assertion", "load/recovery", "--mode", "percent", "--rule-index", "1", "--max-workload-minutes", "40", "--validate"}) != 0 {
		t.Fatal("offline percentage validation failed")
	}
	calls := 0
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, percentTestOptions(), func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("report-%d", calls), true)
	})
	output := filepath.Join(dir, "percent.json")
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
	html := filepath.Join(dir, "percent.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	rendered, err := os.ReadFile(html)
	if err != nil || !strings.Contains(string(rendered), "Confirmed selection percentage: 100 → 1%") {
		t.Fatal("HTML missing percentage comparison")
	}
	junit := filepath.Join(dir, "percent.xml")
	if err := writeTestJUnit(junit, []testRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	xmlBody, err := os.ReadFile(junit)
	if err != nil || !strings.Contains(string(xmlBody), "best_percent") {
		t.Fatal("JUnit missing evidence")
	}
	if cmdTestReplay([]string{"--from", output, "--run-id", best.RunID, "--manifest", manifest, "--validate"}) != 0 {
		t.Fatal("offline percentage replay failed")
	}
	// Single-rule fault phases still use positive selection percentages.
	single := scenario
	single.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	load := *scenario.Steps[1].Load
	load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), load.ChaosSchedule...)
	load.ChaosSchedule[1].Rules = scenario.Steps[1].Load.ChaosSchedule[1].Rules[1:2]
	single.Steps[1].Load = &load
	options := percentTestOptions()
	options.RuleIndex = 0
	row := minimizationTestReceipt(single, "single", true)
	if _, err := validateTestMinimization(testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: single}, &row, options); err != nil {
		t.Fatal(err)
	}
}

func TestPercentReplayRejectsRemovedSeverityEdits(t *testing.T) {
	scenario := percentTestScenario("tcp_bandwidth")
	for _, mode := range []string{"latency", "bandwidth"} {
		edits := testFaultReplayOverrides{Percent: []testFaultPercentOverride{{Rule: testFaultRuleRef{"load", "fault", 0}, Percent: 50}}}
		var removed testFaultRuleRef
		if mode == "latency" {
			removed = testFaultRuleRef{"load", "fault", 2}
			edits.Latency = []testFaultLatencyOverride{{Rule: removed, Latency: "1ms"}}
		} else {
			removed = testFaultRuleRef{"load", "fault", 1}
			edits.Bandwidth = []testFaultBandwidthOverride{{Rule: removed, RateKiBPerSecond: 20}}
		}
		if _, err := applyTestFaultReplayOverrides(scenario, []testFaultRuleRef{removed}, edits); err == nil {
			t.Fatalf("accepted removed %s edit", mode)
		}
	}
}
