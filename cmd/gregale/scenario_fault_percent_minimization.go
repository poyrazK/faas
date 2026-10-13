package main

import (
	"context"
	"errors"
)

type testFaultPercentOverride struct {
	Rule    testFaultRuleRef `json:"rule"`
	Percent int              `json:"percent"`
}
type testPercentMinimizationEvidence struct {
	Rule                       testFaultRuleRef `json:"rule"`
	BeforePercent              int              `json:"before_percent"`
	BestPercent                int              `json:"best_percent"`
	PassedBelowPercent         *int             `json:"passed_below_percent,omitempty"`
	ResolutionPercentagePoints int              `json:"resolution_percentage_points"`
}

func applyTestFaultReplayOverrides(scenario testScenario, removed []testFaultRuleRef, overrides testFaultReplayOverrides) (testScenario, error) {
	if len(removed) > 20 || len(overrides.Latency) > 20 || len(overrides.Bandwidth) > 20 || len(overrides.Percent) > 20 || len(overrides.Recovery) > 20 {
		return scenario, errors.New("replay supports at most 20 removals and 20 overrides of each severity type")
	}
	for _, ref := range removed {
		for _, override := range overrides.Latency {
			if override.Rule == ref {
				return scenario, errors.New("cannot override a removed rule")
			}
		}
		for _, override := range overrides.Bandwidth {
			if override.Rule == ref {
				return scenario, errors.New("cannot override a removed rule")
			}
		}
	}
	result, err := applyTestFaultReplayEdits(scenario, nil, overrides.Latency, overrides.Bandwidth)
	if err != nil {
		return scenario, err
	}
	result, err = applyTestPercentOverrides(result, removed, overrides.Percent)
	if err != nil {
		return scenario, err
	}
	result, err = applyTestRecoveryDeadlineOverrides(result, overrides.Recovery)
	if err != nil {
		return scenario, err
	}
	result, err = applyTestFaultDurationOverrides(result, overrides.Duration)
	if err != nil {
		return scenario, err
	}
	return removeTestFaultRules(result, removed)
}

// References original rule indices. Copy each changed container to keep the
// source configuration and inherited severity edits read-only.
func applyTestPercentOverrides(scenario testScenario, removed []testFaultRuleRef, overrides []testFaultPercentOverride) (testScenario, error) {
	if len(overrides) > 20 {
		return scenario, errors.New("at most 20 percentage overrides are supported")
	}
	result := scenario
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	seen := map[testFaultRuleRef]bool{}
	for _, override := range overrides {
		ref := override.Rule
		if ref.Index < 0 || seen[ref] || override.Percent < 1 || override.Percent > 100 {
			return scenario, errors.New("invalid or duplicate percentage override")
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
				if rules[ref.Index].Percent < 1 || rules[ref.Index].Percent > 100 || override.Percent > rules[ref.Index].Percent {
					return scenario, errors.New("percentage override must reduce a valid fault rule's percentage")
				}
				rules[ref.Index].Percent = override.Percent
				load.ChaosSchedule[j].Rules = rules
				found = true
			}
			result.Steps[i].Load = &load
		}
		if !found {
			return scenario, errors.New("percentage override references an unknown rule")
		}
	}
	return result, nil
}
func testPercentSelection(scenario testScenario, manifest *testReplayManifest, options testFaultMinimizationOptions) (testFaultRuleRef, int, error) {
	refs, err := testMinimizationRules(scenario, options.Target, 1)
	if err != nil {
		return testFaultRuleRef{}, 0, err
	}
	ref := refs[0]
	ref.Index = options.RuleIndex
	alreadyOverridden := false
	for _, override := range manifest.PercentOverrides {
		alreadyOverridden = alreadyOverridden || override.Rule == ref
	}
	if len(manifest.PercentOverrides) >= 20 && !alreadyOverridden {
		return ref, 0, errors.New("replay already contains the maximum 20 percentage overrides")
	}
	effective, err := testEffectiveRuleRef(ref, manifest)
	if err != nil {
		return ref, 0, err
	}
	if options.PercentResolution < 1 || options.PercentResolution > 100 {
		return ref, 0, errors.New("percentage resolution must be between 1 and 100 percentage points")
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
			if rule.Percent < 1 || rule.Percent > 100 {
				return ref, 0, errors.New("selected rule must have a percentage between 1 and 100")
			}
			return ref, rule.Percent, nil
		}
	}
	return ref, 0, errors.New("selected rule index is absent")
}
func runTestPercentMinimization(ctx context.Context, prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions, evidence *testFaultMinimizationEvidence, confirm func(testPreparedScenario, []testFaultRuleRef, ...testFaultReplayOverrides) testFaultMinimizationTrial) {
	ref, before, _ := testPercentSelection(prepared.Scenario, original.Replay, options)
	detail := &testPercentMinimizationEvidence{Rule: ref, BeforePercent: before, BestPercent: before, ResolutionPercentagePoints: options.PercentResolution}
	evidence.Percent = detail
	effective, _ := testEffectiveRuleRef(ref, original.Replay)
	low, high := 1, before
	for high > 1 {
		percent := 1
		if detail.PassedBelowPercent != nil {
			if high-low <= options.PercentResolution {
				break
			}
			percent = low + (high-low)/2
		}
		variant, err := applyTestFaultReplayOverrides(prepared.Scenario, nil, testFaultReplayOverrides{Percent: []testFaultPercentOverride{{Rule: effective, Percent: percent}}})
		if err == nil {
			err = validateTestScenarioSteps(variant, nil)
		}
		if err != nil {
			evidence.Reason = err.Error()
			return
		}
		overrides := append([]testFaultPercentOverride(nil), original.Replay.PercentOverrides...)
		replaced := false
		for i := range overrides {
			if overrides[i].Rule == ref {
				overrides[i].Percent = percent
				replaced = true
			}
		}
		if !replaced {
			overrides = append(overrides, testFaultPercentOverride{Rule: ref, Percent: percent})
		}
		edits := testFaultReplayOverrides{Duration: original.Replay.DurationOverrides, Latency: original.Replay.LatencyOverrides, Bandwidth: original.Replay.BandwidthOverrides, Percent: overrides, Recovery: original.Replay.RecoveryOverrides}
		candidate := prepared
		candidate.Scenario = variant
		trial := confirm(candidate, original.Replay.RemovedRules, edits)
		evidence.Trials = append(evidence.Trials, trial)
		if trial.Status == "inconclusive" {
			evidence.Reason = trial.Reason
			return
		}
		if trial.Status == "reproduced" {
			high = percent
			detail.BestPercent = percent
			evidence.BestRunID = trial.Runs[1].RunID
			evidence.Replay = minimizedTestReplay(original.Replay, variant, original.Replay.RemovedRules, edits)
		} else {
			low = percent
			bound := percent
			detail.PassedBelowPercent = &bound
		}
	}
	if ctx.Err() != nil {
		evidence.Reason = "minimization interrupted"
		return
	}
	evidence.Status = "minimized"
	if detail.BestPercent == before {
		evidence.Status = "unchanged"
	}
}
