package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestLatencyMinimizationThresholdAndReplay(t *testing.T) {
	for _, kind := range []string{"latency", "tcp_latency"} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reduced=%v", kind, reduced), func(t *testing.T) {
				base := minimizationTestScenario()
				base.Steps[1].Load.ChaosSchedule[1].Rules[1].Kind = kind
				if kind == "latency" {
					base.Steps[1].Load.ChaosSchedule[1].Rules[1].Port = 0
				}
				scenario := base
				original := minimizationTestReceipt(base, "original", true)
				index := 1
				if reduced {
					var err error
					scenario, err = removeTestFaultRules(base, []testFaultRuleRef{{"load", "fault", 0}})
					if err != nil {
						t.Fatal(err)
					}
					original = minimizationTestReceipt(scenario, "original", true)
					original.Replay = minimizedTestReplay(newTestReplayManifest("checkout", "cold", "", base), scenario, []testFaultRuleRef{{"load", "fault", 0}})
					index = 0
				}
				opts := minimizationTestOptions()
				opts.Mode = "latency"
				opts.RuleIndex = 1
				opts.Resolution = time.Millisecond
				calls := 0
				result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, opts, func(ctx context.Context, plan testRunPlan) testRunReceipt {
					calls++
					d, _ := time.ParseDuration(plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[index].Latency)
					return minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), d >= 60*time.Millisecond)
				})
				e := result.Minimization
				if result.Status != "passed" || e.Latency.BestMS != 60 || e.Latency.PassedBelowMS == nil || *e.Latency.PassedBelowMS != 59 || e.Replay.Version != 3 {
					t.Fatalf("result=%+v latency=%+v", e, e.Latency)
				}
				best, err := selectTestReplay([]testRunReceipt{result}, e.BestRunID)
				if err != nil {
					t.Fatal(err)
				}
				replay, err := prepareTestReplay(best, map[string]testScenario{"checkout": base}, ".")
				if err != nil {
					t.Fatal(err)
				}
				if replay.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[index].Latency != "60ms" || base.Steps[1].Load.ChaosSchedule[1].Rules[1].Latency != "100ms" {
					t.Fatal("replay mismatch or source mutated")
				}
			})
		}
	}
}
func TestLatencyMinimizationFloorAndIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"floor", "budget", "flaky", "unmatched", "cleanup", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			scenario := minimizationTestScenario()
			original := minimizationTestReceipt(scenario, "original", true)
			opts := minimizationTestOptions()
			opts.Mode = "latency"
			opts.RuleIndex = 1
			opts.Resolution = time.Millisecond
			if mode == "budget" {
				opts.MaxRuns = 2
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := runTestFaultMinimization(ctx, testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, opts, func(ctx context.Context, plan testRunPlan) testRunReceipt {
				calls++
				row := minimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), mode != "flaky" || calls != 4)
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
			if mode == "floor" {
				if e.Status != "minimized" || e.Latency.BestMS != 1 || calls != 4 {
					t.Fatalf("%+v", e)
				}
			} else if e.Status != "inconclusive" || e.Latency.BestMS != 100 {
				t.Fatalf("%+v", e)
			}
		})
	}
}
func TestLatencyReplayRejectsInvalidEdits(t *testing.T) {
	scenario := minimizationTestScenario()
	ref := testFaultRuleRef{"load", "fault", 1}
	for _, duration := range []string{"0ms", "100us", "101ms", "31s", "bad"} {
		if _, err := applyTestFaultReplayEdits(scenario, nil, []testFaultLatencyOverride{{ref, duration}}); err == nil {
			t.Fatalf("accepted %s", duration)
		}
	}
	for _, edits := range [][]testFaultLatencyOverride{{{ref, "1ms"}, {ref, "2ms"}}, {{testFaultRuleRef{"load", "fault", 30}, "1ms"}}} {
		if _, err := applyTestFaultReplayEdits(scenario, nil, edits); err == nil {
			t.Fatal("accepted invalid refs")
		}
	}
	if _, err := applyTestFaultReplayEdits(scenario, []testFaultRuleRef{ref}, []testFaultLatencyOverride{{ref, "1ms"}}); err == nil {
		t.Fatal("accepted removed override")
	}
	for _, args := range [][]string{{"--mode", "latency", "--validate"}, {"--mode", "latency", "--rule-index", "1", "--resolution", "0ms", "--validate"}, {"--rule-index", "1", "--validate"}, {"--resolution", "1ms", "--validate"}} {
		if cmdTestMinimize(args) == 0 {
			t.Fatal("accepted invalid flags")
		}
	}
}

func TestLatencyMinimizationRequiresReplayCapacity(t *testing.T) {
	scenario := minimizationTestScenario()
	manifest := newTestReplayManifest("checkout", "cold", "", scenario)
	for i := 0; i < 20; i++ {
		manifest.LatencyOverrides = append(manifest.LatencyOverrides, testFaultLatencyOverride{Rule: testFaultRuleRef{"other", "fault", i}, Latency: "1ms"})
	}
	options := minimizationTestOptions()
	options.Mode = "latency"
	options.RuleIndex = 1
	if _, _, err := testLatencySelection(scenario, manifest, options); err == nil {
		t.Fatal("accepted reduction that cannot be saved in replay")
	}
	manifest.LatencyOverrides[0].Rule = testFaultRuleRef{"load", "fault", 1}
	if _, _, err := testLatencySelection(scenario, manifest, options); err != nil {
		t.Fatal(err)
	}
}
