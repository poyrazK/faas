package main_test

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
	"time"
)

func TestLatencyMinimizationThresholdAndReplay(t *testing.T) {
	for _, kind := range []string{"latency", "tcp_latency"} {
		for _, reduced := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reduced=%v", kind, reduced), func(t *testing.T) {
				base := main.MinimizationTestScenario()
				base.Steps[1].Load.ChaosSchedule[1].Rules[1].Kind = kind
				if kind == "latency" {
					base.Steps[1].Load.ChaosSchedule[1].Rules[1].Port = 0
				}
				scenario := base
				original := main.MinimizationTestReceipt(base, "original", true)
				index := 1
				if reduced {
					var err error
					scenario, err = main.RemoveTestFaultRules(base, []main.TestFaultRuleRef{{"load", "fault", 0}})
					if err != nil {
						t.Fatal(err)
					}
					original = main.MinimizationTestReceipt(scenario, "original", true)
					original.Replay = main.MinimizedTestReplay(main.NewTestReplayManifest("checkout", "cold", "", base), scenario, []main.TestFaultRuleRef{{"load", "fault", 0}})
					index = 0
				}
				opts := main.MinimizationTestOptions()
				opts.Mode = "latency"
				opts.RuleIndex = 1
				opts.Resolution = time.Millisecond
				calls := 0
				result := main.RunTestFaultMinimization(context.Background(), main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, opts, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
					calls++
					d, _ := time.ParseDuration(plan.Prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[index].Latency)
					return main.MinimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), d >= 60*time.Millisecond)
				})
				e := result.Minimization
				if result.Status != "passed" || e.Latency.BestMS != 60 || e.Latency.PassedBelowMS == nil || *e.Latency.PassedBelowMS != 59 || e.Replay.Version != 3 {
					t.Fatalf("result=%+v latency=%+v", e, e.Latency)
				}
				best, err := main.SelectTestReplay([]main.TestRunReceipt{result}, e.BestRunID)
				if err != nil {
					t.Fatal(err)
				}
				replay, err := main.PrepareTestReplay(best, map[string]main.TestScenario{"checkout": base}, ".")
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
			scenario := main.MinimizationTestScenario()
			original := main.MinimizationTestReceipt(scenario, "original", true)
			opts := main.MinimizationTestOptions()
			opts.Mode = "latency"
			opts.RuleIndex = 1
			opts.Resolution = time.Millisecond
			if mode == "budget" {
				opts.MaxRuns = 2
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := main.RunTestFaultMinimization(ctx, main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, opts, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
				calls++
				row := main.MinimizationTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), mode != "flaky" || calls != 4)
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
	scenario := main.MinimizationTestScenario()
	ref := main.TestFaultRuleRef{"load", "fault", 1}
	for _, duration := range []string{"0ms", "100us", "101ms", "31s", "bad"} {
		if _, err := main.ApplyTestFaultReplayEdits(scenario, nil, []main.TestFaultLatencyOverride{{ref, duration}}); err == nil {
			t.Fatalf("accepted %s", duration)
		}
	}
	for _, edits := range [][]main.TestFaultLatencyOverride{{{ref, "1ms"}, {ref, "2ms"}}, {{main.TestFaultRuleRef{"load", "fault", 30}, "1ms"}}} {
		if _, err := main.ApplyTestFaultReplayEdits(scenario, nil, edits); err == nil {
			t.Fatal("accepted invalid refs")
		}
	}
	if _, err := main.ApplyTestFaultReplayEdits(scenario, []main.TestFaultRuleRef{ref}, []main.TestFaultLatencyOverride{{ref, "1ms"}}); err == nil {
		t.Fatal("accepted removed override")
	}
	for _, args := range [][]string{{"--mode", "latency", "--validate"}, {"--mode", "latency", "--rule-index", "1", "--resolution", "0ms", "--validate"}, {"--rule-index", "1", "--validate"}, {"--resolution", "1ms", "--validate"}} {
		if main.CmdTestMinimize(args) == 0 {
			t.Fatal("accepted invalid flags")
		}
	}
}

func TestLatencyMinimizationRequiresReplayCapacity(t *testing.T) {
	scenario := main.MinimizationTestScenario()
	manifest := main.NewTestReplayManifest("checkout", "cold", "", scenario)
	for i := 0; i < 20; i++ {
		manifest.LatencyOverrides = append(manifest.LatencyOverrides, main.TestFaultLatencyOverride{Rule: main.TestFaultRuleRef{"other", "fault", i}, Latency: "1ms"})
	}
	options := main.MinimizationTestOptions()
	options.Mode = "latency"
	options.RuleIndex = 1
	if _, _, err := main.TestLatencySelection(scenario, manifest, options); err == nil {
		t.Fatal("accepted reduction that cannot be saved in replay")
	}
	manifest.LatencyOverrides[0].Rule = main.TestFaultRuleRef{"load", "fault", 1}
	if _, _, err := main.TestLatencySelection(scenario, manifest, options); err != nil {
		t.Fatal(err)
	}
}
