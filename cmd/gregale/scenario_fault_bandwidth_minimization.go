package main

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

type testFaultBandwidthOverride struct {
	Rule             testFaultRuleRef `json:"rule"`
	RateKiBPerSecond int64            `json:"rate_kib_per_second"`
}
type testBandwidthMinimizationEvidence struct {
	Rule                    testFaultRuleRef `json:"rule"`
	BeforeKiBPerSecond      int64            `json:"before_kib_per_second"`
	BestKiBPerSecond        int64            `json:"best_kib_per_second"`
	PassedAboveKiBPerSecond *int64           `json:"passed_above_kib_per_second,omitempty"`
	MaxKiBPerSecond         int64            `json:"max_kib_per_second"`
	ResolutionKiBPerSecond  int64            `json:"resolution_kib_per_second"`
}

// Apply edits at original indices before removals, copying modified containers.
func applyTestBandwidthOverrides(scenario testScenario, removed []testFaultRuleRef, overrides []testFaultBandwidthOverride) (testScenario, error) {
	if len(overrides) > 20 {
		return scenario, errors.New("at most 20 bandwidth overrides are supported")
	}
	result := scenario
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	seen := map[testFaultRuleRef]bool{}
	for _, override := range overrides {
		ref := override.Rule
		rate := override.RateKiBPerSecond
		if ref.Index < 0 || seen[ref] || rate < 1 || rate > api.ScenarioTCPChaosMaxRateKiBPerSecond {
			return scenario, errors.New("invalid or duplicate bandwidth override")
		}
		seen[ref] = true
		for _, r := range removed {
			if r == ref {
				return scenario, errors.New("cannot override a removed rule")
			}
		}
		found := false
		for i, step := range result.Steps {
			if step.Name != ref.Step || step.Load == nil {
				continue
			}
			load := *step.Load
			load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), load.ChaosSchedule...)
			for j, phase := range load.ChaosSchedule {
				if phase.Name != ref.Phase || ref.Index >= len(phase.Rules) {
					continue
				}
				rules := append([]testChaosRule(nil), phase.Rules...)
				if rules[ref.Index].Kind != chaos.KindTCPBandwidth || rate < rules[ref.Index].RateKiBPerSecond {
					return scenario, errors.New("override must increase a TCP bandwidth rule's allowed rate")
				}
				rules[ref.Index].RateKiBPerSecond = rate
				load.ChaosSchedule[j].Rules = rules
				found = true
			}
			result.Steps[i].Load = &load
		}
		if !found {
			return scenario, errors.New("bandwidth override references an unknown rule")
		}
	}
	return result, nil
}
func testBandwidthSelection(scenario testScenario, manifest *testReplayManifest, options testFaultMinimizationOptions) (testFaultRuleRef, int64, error) {
	refs, err := testMinimizationRules(scenario, options.Target, 1)
	if err != nil {
		return testFaultRuleRef{}, 0, err
	}
	ref := refs[0]
	ref.Index = options.RuleIndex
	alreadyOverridden := false
	for _, override := range manifest.BandwidthOverrides {
		alreadyOverridden = alreadyOverridden || override.Rule == ref
	}
	if len(manifest.BandwidthOverrides) >= 20 && !alreadyOverridden {
		return ref, 0, errors.New("replay already contains the maximum 20 bandwidth overrides")
	}
	effective, err := testEffectiveRuleRef(ref, manifest)
	if err != nil {
		return ref, 0, err
	}
	if options.MaxRateKiBPerSecond < 1 || options.MaxRateKiBPerSecond > api.ScenarioTCPChaosMaxRateKiBPerSecond || options.RateResolutionKiBPerSecond < 1 || options.RateResolutionKiBPerSecond > api.ScenarioTCPChaosMaxRateKiBPerSecond {
		return ref, 0, errors.New("maximum rate and rate resolution must be between 1 and 1000000 KiB/s")
	}
	for _, step := range scenario.Steps {
		if step.Name != effective.Step || step.Load == nil {
			continue
		}
		for _, phase := range step.Load.ChaosSchedule {
			if phase.Name != effective.Phase || effective.Index >= len(phase.Rules) {
				continue
			}
			rule := phase.Rules[effective.Index]
			if rule.Kind != chaos.KindTCPBandwidth || rule.RateKiBPerSecond < 1 || rule.RateKiBPerSecond > api.ScenarioTCPChaosMaxRateKiBPerSecond {
				return ref, 0, errors.New("selected rule must be TCP bandwidth")
			}
			if options.MaxRateKiBPerSecond <= rule.RateKiBPerSecond {
				return ref, 0, errors.New("maximum rate must exceed the selected rule's starting rate")
			}
			return ref, rule.RateKiBPerSecond, nil
		}
	}
	return ref, 0, errors.New("selected rule index is absent")
}
func runTestBandwidthMinimization(ctx context.Context, prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions, evidence *testFaultMinimizationEvidence, confirm func(testPreparedScenario, []testFaultRuleRef, ...testFaultReplayOverrides) testFaultMinimizationTrial) {
	ref, before, _ := testBandwidthSelection(prepared.Scenario, original.Replay, options)
	detail := &testBandwidthMinimizationEvidence{Rule: ref, BeforeKiBPerSecond: before, BestKiBPerSecond: before, MaxKiBPerSecond: options.MaxRateKiBPerSecond, ResolutionKiBPerSecond: options.RateResolutionKiBPerSecond}
	evidence.Bandwidth = detail
	effective, _ := testEffectiveRuleRef(ref, original.Replay)
	low, high := before, options.MaxRateKiBPerSecond
	for {
		rate := high
		if detail.PassedAboveKiBPerSecond != nil {
			if high-low <= options.RateResolutionKiBPerSecond {
				break
			}
			rate = low + (high-low)/2
		}
		variant, err := applyTestFaultReplayEdits(prepared.Scenario, nil, nil, []testFaultBandwidthOverride{{Rule: effective, RateKiBPerSecond: rate}})
		if err == nil {
			err = validateTestScenarioSteps(variant, nil)
		}
		if err != nil {
			evidence.Reason = err.Error()
			return
		}
		overrides := append([]testFaultBandwidthOverride(nil), original.Replay.BandwidthOverrides...)
		replaced := false
		for i := range overrides {
			if overrides[i].Rule == ref {
				overrides[i].RateKiBPerSecond = rate
				replaced = true
			}
		}
		if !replaced {
			overrides = append(overrides, testFaultBandwidthOverride{Rule: ref, RateKiBPerSecond: rate})
		}
		edits := testFaultReplayOverrides{Duration: original.Replay.DurationOverrides, Latency: original.Replay.LatencyOverrides, Bandwidth: overrides, Percent: original.Replay.PercentOverrides, Recovery: original.Replay.RecoveryOverrides}
		candidate := prepared
		candidate.Scenario = variant
		trial := confirm(candidate, original.Replay.RemovedRules, edits)
		evidence.Trials = append(evidence.Trials, trial)
		if trial.Status == "inconclusive" {
			evidence.Reason = trial.Reason
			return
		}
		if trial.Status == "reproduced" {
			low = rate
			detail.BestKiBPerSecond = rate
			evidence.BestRunID = trial.Runs[1].RunID
			evidence.Replay = minimizedTestReplay(original.Replay, variant, original.Replay.RemovedRules, edits)
			if rate == options.MaxRateKiBPerSecond {
				break
			}
		} else {
			high = rate
			bound := rate
			detail.PassedAboveKiBPerSecond = &bound
		}
	}
	if ctx.Err() != nil {
		evidence.Reason = "minimization interrupted"
		return
	}
	evidence.Status = "minimized"
	if detail.BestKiBPerSecond == before {
		evidence.Status = "unchanged"
	}
}
