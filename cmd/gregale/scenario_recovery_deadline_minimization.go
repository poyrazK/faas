package main

import (
	"context"
	"errors"
	"time"
)

type testRecoveryDeadlineOverride struct {
	Target    testMinimizationTarget `json:"target"`
	Within    string                 `json:"within,omitempty"`
	Stability string                 `json:"stability,omitempty"`
}
type testRecoveryDeadlineMinimizationEvidence struct {
	BeforeWithinMS int64  `json:"before_within_ms"`
	BestWithinMS   *int64 `json:"best_within_ms,omitempty"`
	FailedWithinMS int64  `json:"failed_within_ms"`
	FailedRunID    string `json:"failed_run_id"`
	MaxWithinMS    int64  `json:"max_within_ms"`
	ResolutionMS   int64  `json:"resolution_ms"`
}

func testRecoveryDeadlineSpec(scenario testScenario, target testMinimizationTarget) (*testLoadRecoverySpec, time.Duration, error) {
	for _, step := range scenario.Steps {
		if step.Name != target.Step || step.Load == nil {
			continue
		}
		for _, phase := range step.Load.ChaosSchedule {
			if phase.Name != target.Phase {
				continue
			}
			spec := phase.Recovery
			if target.Request != "" {
				spec = phase.StepRecovery[target.Request]
			}
			duration, err := time.ParseDuration(phase.Duration)
			if spec == nil || err != nil {
				return nil, 0, errors.New("selected recovery budget is absent or phase duration is invalid")
			}
			return spec, duration, nil
		}
	}
	return nil, 0, errors.New("selected recovery assertion is absent")
}
func applyTestRecoveryDeadlineOverrides(scenario testScenario, overrides []testRecoveryDeadlineOverride) (testScenario, error) {
	if len(overrides) > 20 {
		return scenario, errors.New("at most 20 recovery deadline overrides are supported")
	}
	result := scenario
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	seen := map[testMinimizationTarget]bool{}
	for _, override := range overrides {
		if seen[override.Target] {
			return scenario, errors.New("duplicate recovery deadline override")
		}
		seen[override.Target] = true
		spec, phaseDuration, err := testRecoveryDeadlineSpec(result, override.Target)
		if err != nil {
			return scenario, err
		}
		if override.Within == "" && override.Stability == "" {
			return scenario, errors.New("empty recovery override")
		}
		updated := *spec
		if override.Within != "" {
			updated.Within = override.Within
		}
		if override.Stability != "" {
			updated.Stability = override.Stability
		}
		if err := validateTestLoadRecovery(testLoadChaosPhaseSpec{Name: override.Target.Phase, Recovery: &updated}, true, phaseDuration); err != nil {
			return scenario, err
		}

		for i, step := range result.Steps {
			if step.Name != override.Target.Step || step.Load == nil {
				continue
			}
			load := *step.Load
			load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), load.ChaosSchedule...)
			for j, phase := range load.ChaosSchedule {
				if phase.Name != override.Target.Phase {
					continue
				}
				if override.Target.Request == "" {
					load.ChaosSchedule[j].Recovery = &updated
				} else {
					budgets := make(map[string]*testLoadRecoverySpec, len(phase.StepRecovery))
					for name, budget := range phase.StepRecovery {
						budgets[name] = budget
					}
					budgets[override.Target.Request] = &updated
					load.ChaosSchedule[j].StepRecovery = budgets
				}
			}
			result.Steps[i].Load = &load
		}
	}
	return result, nil
}
func testRecoveryDeadlineSelection(scenario testScenario, manifest *testReplayManifest, options testFaultMinimizationOptions) (int64, int64, error) {
	spec, phaseDuration, err := testRecoveryDeadlineSpec(scenario, options.Target)
	if err != nil {
		return 0, 0, err
	}
	cfg := resolveTestLoadRecovery(spec)
	already := false
	for _, override := range manifest.RecoveryOverrides {
		already = already || override.Target == options.Target
	}
	if len(manifest.RecoveryOverrides) >= 20 && !already {
		return 0, 0, errors.New("replay already contains the maximum 20 recovery deadline overrides")
	}
	if options.RecoveryResolution < time.Millisecond || options.RecoveryResolution > testLoadMaxDuration || options.RecoveryResolution%time.Millisecond != 0 || options.MaxRecoveryWithin < 100*time.Millisecond || options.MaxRecoveryWithin > testLoadMaxDuration || options.MaxRecoveryWithin%time.Millisecond != 0 {
		return 0, 0, errors.New("recovery maximum and resolution must be bounded whole-millisecond durations")
	}
	if options.MaxRecoveryWithin <= cfg.Within || options.MaxRecoveryWithin+cfg.Sustain+cfg.Stability > phaseDuration {
		return 0, 0, errors.New("maximum recovery deadline must exceed the failed deadline and fit inside the existing phase with sustain and stability")
	}
	return cfg.Within.Milliseconds(), options.MaxRecoveryWithin.Milliseconds(), nil
}
func runTestRecoveryDeadlineMinimization(ctx context.Context, prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions, evidence *testFaultMinimizationEvidence, confirm func(testPreparedScenario, []testFaultRuleRef, ...testFaultReplayOverrides) testFaultMinimizationTrial) {
	before, ceiling, _ := testRecoveryDeadlineSelection(prepared.Scenario, original.Replay, options)
	detail := &testRecoveryDeadlineMinimizationEvidence{BeforeWithinMS: before, FailedWithinMS: before, FailedRunID: evidence.Trials[0].Runs[1].RunID, MaxWithinMS: ceiling, ResolutionMS: options.RecoveryResolution.Milliseconds()}
	evidence.Recovery = detail
	low, high := before, ceiling
	for {
		within := high
		if detail.BestWithinMS != nil {
			if high-low <= detail.ResolutionMS {
				break
			}
			within = low + (high-low)/2
		}
		duration := (time.Duration(within) * time.Millisecond).String()
		variant, err := applyTestRecoveryDeadlineOverrides(prepared.Scenario, []testRecoveryDeadlineOverride{{Target: options.Target, Within: duration}})
		if err == nil {
			err = validateTestScenarioSteps(variant, nil)
		}
		if err != nil {
			evidence.Reason = err.Error()
			return
		}
		overrides := append([]testRecoveryDeadlineOverride(nil), original.Replay.RecoveryOverrides...)
		replaced := false
		for i := range overrides {
			if overrides[i].Target == options.Target {
				overrides[i].Within = duration
				replaced = true
			}
		}
		if !replaced {
			overrides = append(overrides, testRecoveryDeadlineOverride{Target: options.Target, Within: duration})
		}
		edits := testFaultReplayOverrides{Duration: original.Replay.DurationOverrides, Latency: original.Replay.LatencyOverrides, Bandwidth: original.Replay.BandwidthOverrides, Percent: original.Replay.PercentOverrides, Recovery: overrides}
		candidate := prepared
		candidate.Scenario = variant
		trial := confirm(candidate, original.Replay.RemovedRules, edits)
		evidence.Trials = append(evidence.Trials, trial)
		if trial.Status == "inconclusive" {
			evidence.Reason = trial.Reason
			return
		}
		if trial.Status == "passed" {
			high = within
			best := within
			detail.BestWithinMS = &best
			evidence.BestRunID = trial.Runs[1].RunID
			evidence.Replay = minimizedTestReplay(original.Replay, variant, original.Replay.RemovedRules, edits)
		} else {
			low = within
			detail.FailedWithinMS = within
			detail.FailedRunID = trial.Runs[1].RunID
			if detail.BestWithinMS == nil {
				evidence.Reason = "no passing recovery deadline confirmed within the requested ceiling"
				return
			}
		}
	}
	if ctx.Err() != nil {
		evidence.Reason = "minimization interrupted"
		return
	}
	evidence.Status = "minimized"
}

func testRecoveryDeadlineEvidenceMatches(receipt testRunReceipt, target testMinimizationTarget, manifest *testReplayManifest) bool {
	for _, override := range manifest.RecoveryOverrides {
		if override.Target != target || override.Within == "" {
			continue
		}
		within, err := time.ParseDuration(override.Within)
		if err != nil {
			return false
		}
		for _, step := range receipt.Steps {
			if step.Name != target.Step || step.Load == nil {
				continue
			}
			for _, phase := range step.Load.ChaosPhases {
				if phase.Name != target.Phase {
					continue
				}
				evidence := testPhaseRecoveryEvidence(phase)[target.Request]
				return evidence != nil && evidence.WithinMS == within.Milliseconds()
			}
		}
		return false
	}
	return true
}
