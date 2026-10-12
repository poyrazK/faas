package main

import (
	"context"
	"errors"
	"time"
)

type testStabilityMinimizationEvidence struct {
	BeforeMS      int64  `json:"before_ms"`
	BestMS        int64  `json:"best_ms"`
	PassedBelowMS *int64 `json:"passed_below_ms,omitempty"`
	ResolutionMS  int64  `json:"resolution_ms"`
}

func testSelectedRecoveryFailure(r *testLoadRecoveryEvidence, stability bool) bool {
	if stability {
		return r.Error == "recovery stability relapsed into an unhealthy window"
	}
	return r.Error == "no sustained healthy interval within the recovery deadline"
}

func testStabilitySelection(scenario testScenario, manifest *testReplayManifest, options testFaultMinimizationOptions) (int64, error) {
	spec, _, err := testRecoveryDeadlineSpec(scenario, options.Target)
	if err != nil {
		return 0, err
	}
	cfg := resolveTestLoadRecovery(spec)
	if cfg.Stability < 100*time.Millisecond || cfg.Stability > testLoadMaxDuration || cfg.Stability%time.Millisecond != 0 {
		return 0, errors.New("stability minimization requires an explicit stability budget from 100ms to 5m")
	}
	if options.StabilityResolution < time.Millisecond || options.StabilityResolution > testLoadMaxDuration || options.StabilityResolution%time.Millisecond != 0 {
		return 0, errors.New("stability resolution must be whole milliseconds from 1ms to 5m")
	}
	already := false
	for _, override := range manifest.RecoveryOverrides {
		already = already || override.Target == options.Target
	}
	if len(manifest.RecoveryOverrides) >= 20 && !already {
		return 0, errors.New("replay already contains the maximum 20 recovery overrides")
	}
	return cfg.Stability.Milliseconds(), nil
}

// Preserve edits of the deadline and of other recovery targets.
func testStabilityEdits(manifest *testReplayManifest, target testMinimizationTarget, duration string) testFaultReplayOverrides {
	edits := testFaultReplayOverrides{Duration: manifest.DurationOverrides, Latency: manifest.LatencyOverrides, Bandwidth: manifest.BandwidthOverrides, Percent: manifest.PercentOverrides, Recovery: append([]testRecoveryDeadlineOverride(nil), manifest.RecoveryOverrides...)}
	for i := range edits.Recovery {
		if edits.Recovery[i].Target == target {
			edits.Recovery[i].Stability = duration
			return edits
		}
	}
	edits.Recovery = append(edits.Recovery, testRecoveryDeadlineOverride{Target: target, Stability: duration})
	return edits
}

func testStabilityMinimizationOutcome(receipt testRunReceipt, options testFaultMinimizationOptions, manifest *testReplayManifest) string {
	if !testRecoveryDeadlineEvidenceMatches(receipt, options.Target, manifest) {
		return "inconclusive"
	}
	for _, step := range receipt.Steps {
		if step.Name != options.Target.Step || step.Load == nil {
			continue
		}
		for _, phase := range step.Load.ChaosPhases {
			if phase.Name != options.Target.Phase {
				continue
			}
			r := testPhaseRecoveryEvidence(phase)[options.Target.Request]
			if r == nil || r.Stability == nil || r.RecoveryMS == nil || !finiteNonnegative(*r.RecoveryMS) || *r.RecoveryMS <= 0 || *r.RecoveryMS > float64(r.WithinMS) {
				return "inconclusive"
			}
			s := r.Stability
			if s.StartedMS == nil || !finiteNonnegative(*s.StartedMS) || *s.StartedMS+0.000001 < *r.RecoveryMS+float64(r.SustainMS) || s.DurationMS < 100 || s.DurationMS > testLoadMaxDuration.Milliseconds() || s.WindowsChecked < 1 || s.ObservedMS < 1 || s.ObservedMS > s.DurationMS {
				return "inconclusive"
			}
			for _, override := range manifest.RecoveryOverrides {
				if override.Target == options.Target && override.Stability != "" {
					d, err := time.ParseDuration(override.Stability)
					if err != nil || d.Milliseconds() != s.DurationMS {
						return "inconclusive"
					}
				}
			}
			if r.Passed {
				if !s.Passed || s.ObservedMS != s.DurationMS || s.RelapseMS != nil {
					return "inconclusive"
				}
			} else {
				if s.Passed || s.RelapseMS == nil || !finiteNonnegative(*s.RelapseMS) || *s.RelapseMS+0.000001 < *s.StartedMS+float64(s.ObservedMS) || *s.RelapseMS >= *s.StartedMS+float64(s.ObservedMS)+1.000001 {
					return "inconclusive"
				}
			}
			if *s.StartedMS+float64(s.ObservedMS) > loadMilliseconds(phase.FinishedAt.Sub(phase.StartedAt)) {
				return "inconclusive"
			}
			return testMinimizationOutcome(receipt, options.Target, true)
		}
	}
	return "inconclusive"
}

func runTestStabilityMinimization(ctx context.Context, prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions, evidence *testFaultMinimizationEvidence, confirm func(testPreparedScenario, []testFaultRuleRef, ...testFaultReplayOverrides) testFaultMinimizationTrial) {
	before, _ := testStabilitySelection(prepared.Scenario, original.Replay, options)
	detail := &testStabilityMinimizationEvidence{BeforeMS: before, BestMS: before, ResolutionMS: options.StabilityResolution.Milliseconds()}
	evidence.Stability = detail
	low, high := int64(100), before
	floorTested := false
	for high > 100 {
		value := low
		if floorTested {
			if high-low <= detail.ResolutionMS {
				break
			}
			value = low + (high-low)/2
		}
		duration := (time.Duration(value) * time.Millisecond).String()
		variant, err := applyTestRecoveryDeadlineOverrides(prepared.Scenario, []testRecoveryDeadlineOverride{{Target: options.Target, Stability: duration}})
		if err == nil {
			err = validateTestScenarioSteps(variant, nil)
		}
		if err != nil {
			evidence.Reason = err.Error()
			return
		}
		edits := testStabilityEdits(original.Replay, options.Target, duration)
		candidate := prepared
		candidate.Scenario = variant
		trial := confirm(candidate, original.Replay.RemovedRules, edits)
		evidence.Trials = append(evidence.Trials, trial)
		if trial.Status == "inconclusive" {
			evidence.Reason = trial.Reason
			return
		}
		floorTested = true
		if trial.Status == "reproduced" {
			high = value
			detail.BestMS = value
			evidence.BestRunID = trial.Runs[1].RunID
			evidence.Replay = minimizedTestReplay(original.Replay, variant, original.Replay.RemovedRules, edits)
		} else {
			low = value
			passed := value
			detail.PassedBelowMS = &passed
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
