package main

import (
	"context"
	"errors"
	"time"
)

type testFaultDurationOverride struct {
	Step     string `json:"step"`
	Phase    string `json:"phase"`
	Duration string `json:"duration"`
}
type testDurationMinimizationEvidence struct {
	Step          string `json:"step"`
	Phase         string `json:"phase"`
	BeforeMS      int64  `json:"before_ms"`
	BestMS        int64  `json:"best_ms"`
	PassedBelowMS *int64 `json:"passed_below_ms,omitempty"`
	ResolutionMS  int64  `json:"resolution_ms"`
}

// Overrides reduce the exposure of an entire fault phase, preserving all rules
// and the time available for healthy and recovery observations.
func applyTestFaultDurationOverrides(scenario testScenario, overrides []testFaultDurationOverride) (testScenario, error) {
	if len(overrides) > 20 {
		return scenario, errors.New("replay supports at most 20 duration overrides")
	}
	result := scenario
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	seen := map[[2]string]bool{}
	for _, o := range overrides {
		key := [2]string{o.Step, o.Phase}
		d, err := time.ParseDuration(o.Duration)
		if seen[key] || err != nil || d < time.Second || d > testLoadMaxDuration || d%time.Millisecond != 0 {
			return scenario, errors.New("invalid or duplicate fault duration override; use whole milliseconds from 1s to 5m")
		}
		seen[key] = true
		found := false
		for i, step := range result.Steps {
			if step.Name != o.Step || step.Load == nil {
				continue
			}
			load := *step.Load
			if load.Duration == "" || len(load.Stages) > 0 {
				return scenario, errors.New("duration minimization requires a fixed duration load without stages")
			}
			load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), load.ChaosSchedule...)
			for j, phase := range load.ChaosSchedule {
				if phase.Name != o.Phase {
					continue
				}
				before, err := time.ParseDuration(phase.Duration)
				if err != nil || before < time.Second || before%time.Millisecond != 0 || d > before || len(phase.Rules) == 0 {
					return scenario, errors.New("duration override must reduce an existing fault phase")
				}
				total, err := time.ParseDuration(load.Duration)
				adjusted := total + d - before
				if err != nil || adjusted < time.Second || adjusted > testLoadMaxDuration {
					return scenario, errors.New("invalid adjusted load duration")
				}
				load.Duration = adjusted.String()
				load.ChaosSchedule[j].Duration = d.String()
				found = true
			}
			result.Steps[i].Load = &load
		}
		if !found {
			return scenario, errors.New("duration override references an unknown fault phase")
		}
	}
	return result, nil
}

func testDurationSelection(scenario testScenario, manifest *testReplayManifest, options testFaultMinimizationOptions) (testFaultDurationOverride, int64, error) {
	refs, err := testMinimizationRules(scenario, options.Target, 1)
	if err != nil {
		return testFaultDurationOverride{}, 0, err
	}
	selected := testFaultDurationOverride{Step: refs[0].Step, Phase: refs[0].Phase}
	if options.DurationResolution < time.Millisecond || options.DurationResolution > testLoadMaxDuration || options.DurationResolution%time.Millisecond != 0 {
		return selected, 0, errors.New("duration resolution must be whole milliseconds from 1ms to 5m")
	}
	already := false
	for _, o := range manifest.DurationOverrides {
		already = already || (o.Step == selected.Step && o.Phase == selected.Phase)
	}
	if len(manifest.DurationOverrides) >= 20 && !already {
		return selected, 0, errors.New("replay already contains the maximum 20 duration overrides")
	}
	for _, step := range scenario.Steps {
		if step.Name != selected.Step || step.Load == nil {
			continue
		}
		for _, phase := range step.Load.ChaosSchedule {
			if phase.Name != selected.Phase {
				continue
			}
			selected.Duration = phase.Duration
			if _, err := applyTestFaultDurationOverrides(scenario, []testFaultDurationOverride{selected}); err != nil {
				return selected, 0, err
			}
			d, _ := time.ParseDuration(phase.Duration)
			return selected, d.Milliseconds(), nil
		}
	}
	return selected, 0, errors.New("selected fault phase is absent")
}

func testDurationEdits(manifest *testReplayManifest, selected testFaultDurationOverride) testFaultReplayOverrides {
	edits := testFaultReplayOverrides{Duration: append([]testFaultDurationOverride(nil), manifest.DurationOverrides...), Latency: manifest.LatencyOverrides, Bandwidth: manifest.BandwidthOverrides, Percent: manifest.PercentOverrides, Recovery: manifest.RecoveryOverrides}
	for i, o := range edits.Duration {
		if o.Step == selected.Step && o.Phase == selected.Phase {
			edits.Duration[i] = selected
			return edits
		}
	}
	edits.Duration = append(edits.Duration, selected)
	return edits
}

func runTestDurationMinimization(ctx context.Context, prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions, evidence *testFaultMinimizationEvidence, confirm func(testPreparedScenario, []testFaultRuleRef, ...testFaultReplayOverrides) testFaultMinimizationTrial) {
	selected, before, _ := testDurationSelection(prepared.Scenario, original.Replay, options)
	detail := &testDurationMinimizationEvidence{Step: selected.Step, Phase: selected.Phase, BeforeMS: before, BestMS: before, ResolutionMS: options.DurationResolution.Milliseconds()}
	evidence.Duration = detail
	low, high := int64(1000), before
	for high > 1000 {
		candidateMS := int64(1000)
		if detail.PassedBelowMS != nil {
			if high-low <= detail.ResolutionMS {
				break
			}
			candidateMS = low + (high-low)/2
		}
		selected.Duration = (time.Duration(candidateMS) * time.Millisecond).String()
		variant, err := applyTestFaultDurationOverrides(prepared.Scenario, []testFaultDurationOverride{selected})
		if err == nil {
			err = validateTestScenarioSteps(variant, nil)
		}
		if err != nil {
			evidence.Reason = err.Error()
			return
		}
		candidate := prepared
		candidate.Scenario = variant
		edits := testDurationEdits(original.Replay, selected)
		trial := confirm(candidate, original.Replay.RemovedRules, edits)
		evidence.Trials = append(evidence.Trials, trial)
		if trial.Status == "inconclusive" {
			evidence.Reason = trial.Reason
			return
		}
		if trial.Status == "reproduced" {
			high = candidateMS
			detail.BestMS = high
			evidence.BestRunID = trial.Runs[1].RunID
			evidence.Replay = minimizedTestReplay(original.Replay, variant, original.Replay.RemovedRules, edits)
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

func testDurationMinimizationOutcome(receipt testRunReceipt, options testFaultMinimizationOptions, manifest *testReplayManifest) string {
	if !testRecoveryDeadlineEvidenceMatches(receipt, options.Target, manifest) {
		return "inconclusive"
	}
	for _, step := range receipt.Steps {
		if step.Name != options.Target.Step || step.Load == nil {
			continue
		}
		for i, phase := range step.Load.ChaosPhases {
			if phase.Name != options.Target.Phase || i < 1 {
				continue
			}
			fault := step.Load.ChaosPhases[i-1]
			if fault.PlannedEndMS-fault.PlannedStartMS < 1000 {
				return "inconclusive"
			}
			matched := false
			for _, o := range manifest.DurationOverrides {
				if o.Step == step.Name && o.Phase == fault.Name {
					matched = true
					d, err := time.ParseDuration(o.Duration)
					if err != nil || d.Milliseconds() != fault.PlannedEndMS-fault.PlannedStartMS {
						return "inconclusive"
					}
				}
			}
			if !matched {
				return "inconclusive"
			}
			return testMinimizationOutcome(receipt, options.Target)
		}
	}
	return "inconclusive"
}
