package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
)

type testFaultLatencyOverride struct {
	Rule    testFaultRuleRef `json:"rule"`
	Latency string           `json:"latency"`
}
type testLatencyMinimizationEvidence struct {
	Rule          testFaultRuleRef `json:"rule"`
	BeforeMS      int64            `json:"before_ms"`
	BestMS        int64            `json:"best_ms"`
	PassedBelowMS *int64           `json:"passed_below_ms,omitempty"`
	ResolutionMS  int64            `json:"resolution_ms"`
}

func testEffectiveRuleRef(ref testFaultRuleRef, manifest *testReplayManifest) (testFaultRuleRef, error) {
	result := ref
	if ref.Index < 0 {
		return result, errors.New("invalid rule index")
	}
	for _, r := range manifest.RemovedRules {
		if r.Step == ref.Step && r.Phase == ref.Phase {
			if r.Index == ref.Index {
				return result, errors.New("selected rule was removed")
			}
			if r.Index < ref.Index {
				result.Index--
			}
		}
	}
	return result, nil
}
func applyTestFaultReplayEdits(scenario testScenario, removed []testFaultRuleRef, overrides []testFaultLatencyOverride, bandwidth ...[]testFaultBandwidthOverride) (testScenario, error) {
	if len(overrides) > 20 {
		return scenario, errors.New("at most 20 latency overrides are supported")
	}
	result := scenario
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	seen := map[testFaultRuleRef]bool{}
	for _, o := range overrides {
		if seen[o.Rule] || o.Rule.Index < 0 {
			return scenario, errors.New("invalid or duplicate latency override")
		}
		seen[o.Rule] = true
		for _, r := range removed {
			if r == o.Rule {
				return scenario, errors.New("cannot override a removed rule")
			}
		}
		duration, err := time.ParseDuration(o.Latency)
		if err != nil || duration < time.Millisecond || duration > chaos.MaxLatency || duration%time.Millisecond != 0 {
			return scenario, errors.New("latency must be positive whole milliseconds within the chaos limit")
		}
		found := false
		for i, step := range result.Steps {
			if step.Name != o.Rule.Step || step.Load == nil {
				continue
			}
			load := *step.Load
			load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), load.ChaosSchedule...)
			for j, phase := range load.ChaosSchedule {
				if phase.Name != o.Rule.Phase || o.Rule.Index >= len(phase.Rules) {
					continue
				}
				rules := append([]testChaosRule(nil), phase.Rules...)
				rule := rules[o.Rule.Index]
				before, err := time.ParseDuration(rule.Latency)
				if (rule.Kind != chaos.KindLatency && rule.Kind != chaos.KindTCPLatency) || err != nil || duration > before {
					return scenario, errors.New("override must reduce an HTTP or TCP latency rule")
				}
				rules[o.Rule.Index].Latency = o.Latency
				load.ChaosSchedule[j].Rules = rules
				found = true
			}
			result.Steps[i].Load = &load
		}
		if !found {
			return scenario, errors.New("latency override references an unknown rule")
		}
	}
	if len(bandwidth) > 0 {
		var err error
		result, err = applyTestBandwidthOverrides(result, removed, bandwidth[0])
		if err != nil {
			return scenario, err
		}
	}
	return removeTestFaultRules(result, removed)
}
func testLatencySelection(scenario testScenario, manifest *testReplayManifest, options testFaultMinimizationOptions) (testFaultRuleRef, int64, error) {
	refs, err := testMinimizationRules(scenario, options.Target, 1)
	if err != nil {
		return testFaultRuleRef{}, 0, err
	}
	original := refs[0]
	original.Index = options.RuleIndex
	alreadyOverridden := false
	for _, override := range manifest.LatencyOverrides {
		alreadyOverridden = alreadyOverridden || override.Rule == original
	}
	if len(manifest.LatencyOverrides) >= 20 && !alreadyOverridden {
		return original, 0, errors.New("replay already contains the maximum 20 latency overrides")
	}
	effective, err := testEffectiveRuleRef(original, manifest)
	if err != nil {
		return original, 0, err
	}
	resolution := options.Resolution
	if resolution == 0 {
		resolution = 25 * time.Millisecond
	}
	if resolution < time.Millisecond || resolution > chaos.MaxLatency || resolution%time.Millisecond != 0 {
		return original, 0, errors.New("resolution must be whole milliseconds between 1ms and 30s")
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
			d, err := time.ParseDuration(rule.Latency)
			if (rule.Kind != chaos.KindLatency && rule.Kind != chaos.KindTCPLatency) || err != nil || d < time.Millisecond || d > chaos.MaxLatency || d%time.Millisecond != 0 {
				return original, 0, errors.New("selected rule must be HTTP or TCP latency in whole milliseconds")
			}
			return original, d.Milliseconds(), nil
		}
	}
	return original, 0, errors.New("selected rule index is absent")
}
func testConfiguredMinimizationOutcome(receipt testRunReceipt, options testFaultMinimizationOptions, manifest *testReplayManifest) string {
	if options.Mode == "duration" {
		return testDurationMinimizationOutcome(receipt, options, manifest)
	}
	if options.Mode == "stability" {
		return testStabilityMinimizationOutcome(receipt, options, manifest)
	}
	if options.Mode == "recovery" && !testRecoveryDeadlineEvidenceMatches(receipt, options.Target, manifest) {
		return "inconclusive"
	}
	outcome := testMinimizationOutcome(receipt, options.Target)
	if !isTestSeverityMinimization(options.Mode) || outcome == "inconclusive" {
		return outcome
	}
	ref := testFaultRuleRef{Step: options.Target.Step, Index: options.RuleIndex}
	for _, step := range receipt.Steps {
		if step.Name != ref.Step || step.Load == nil {
			continue
		}
		for i, phase := range step.Load.ChaosPhases {
			if phase.Name != options.Target.Phase || i < 1 {
				continue
			}
			fault := step.Load.ChaosPhases[i-1]
			ref.Phase = fault.Name
			effective, err := testEffectiveRuleRef(ref, manifest)
			if err != nil || fault.Chaos == nil || effective.Index >= len(fault.Chaos.Matches) || fault.Chaos.Matches[effective.Index].Matches < 1 {
				return "inconclusive"
			}
			return outcome
		}
	}
	return "inconclusive"
}
func runTestLatencyMinimization(ctx context.Context, prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions, evidence *testFaultMinimizationEvidence, confirm func(testPreparedScenario, []testFaultRuleRef, ...testFaultReplayOverrides) testFaultMinimizationTrial) {
	ref, before, _ := testLatencySelection(prepared.Scenario, original.Replay, options)
	resolution := options.Resolution.Milliseconds()
	if resolution == 0 {
		resolution = 25
	}
	detail := &testLatencyMinimizationEvidence{Rule: ref, BeforeMS: before, BestMS: before, ResolutionMS: resolution}
	evidence.Latency = detail
	effective, _ := testEffectiveRuleRef(ref, original.Replay)
	low, high := int64(1), before
	for high > 1 {
		candidateMS := int64(1)
		if detail.PassedBelowMS != nil {
			if high-low <= resolution {
				break
			}
			candidateMS = low + (high-low)/2
		}
		variant, err := applyTestFaultReplayEdits(prepared.Scenario, nil, []testFaultLatencyOverride{{Rule: effective, Latency: (time.Duration(candidateMS) * time.Millisecond).String()}})
		if err == nil {
			err = validateTestScenarioSteps(variant, nil)
		}
		if err != nil {
			evidence.Reason = err.Error()
			return
		}
		overrides := append([]testFaultLatencyOverride(nil), original.Replay.LatencyOverrides...)
		replaced := false
		for i := range overrides {
			if overrides[i].Rule == ref {
				overrides[i].Latency = (time.Duration(candidateMS) * time.Millisecond).String()
				replaced = true
			}
		}
		if !replaced {
			overrides = append(overrides, testFaultLatencyOverride{Rule: ref, Latency: (time.Duration(candidateMS) * time.Millisecond).String()})
		}
		candidate := prepared
		candidate.Scenario = variant
		trial := confirm(candidate, original.Replay.RemovedRules, testFaultReplayOverrides{Duration: original.Replay.DurationOverrides, Latency: overrides, Bandwidth: original.Replay.BandwidthOverrides, Percent: original.Replay.PercentOverrides, Recovery: original.Replay.RecoveryOverrides})
		evidence.Trials = append(evidence.Trials, trial)
		if trial.Status == "inconclusive" {
			evidence.Reason = trial.Reason
			return
		}
		if trial.Status == "reproduced" {
			high = candidateMS
			detail.BestMS = high
			evidence.BestRunID = trial.Runs[1].RunID
			evidence.Replay = minimizedTestReplay(original.Replay, variant, original.Replay.RemovedRules, testFaultReplayOverrides{Duration: original.Replay.DurationOverrides, Latency: overrides, Bandwidth: original.Replay.BandwidthOverrides, Percent: original.Replay.PercentOverrides, Recovery: original.Replay.RecoveryOverrides})
		} else {
			low = candidateMS
			bound := low
			detail.PassedBelowMS = &bound
		}
	}
	if ctx.Err() != nil {
		evidence.Reason = "minimization interrupted"
		return
	}
	evidence.Status = "minimized"
	if detail.BestMS == before {
		evidence.Status = "unchanged"
	}
}
