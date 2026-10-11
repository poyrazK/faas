package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Index refers to the original manifest's rule order, before any removals.
type testFaultRuleRef struct {
	Step  string `json:"step"`
	Phase string `json:"phase"`
	Index int    `json:"index"`
}

type testMinimizationTarget struct {
	Step    string `json:"step"`
	Phase   string `json:"phase"`
	Request string `json:"request,omitempty"`
}

func isTestSeverityMinimization(mode string) bool {
	return mode == "latency" || mode == "bandwidth" || mode == "percent"
}

func isTestConfigurationMinimization(mode string) bool {
	return isTestSeverityMinimization(mode) || mode == "recovery" || mode == "stability" || mode == "duration"
}

type testFaultReplayOverrides struct {
	Duration  []testFaultDurationOverride
	Recovery  []testRecoveryDeadlineOverride
	Percent   []testFaultPercentOverride
	Latency   []testFaultLatencyOverride
	Bandwidth []testFaultBandwidthOverride
}

type testFaultMinimizationTrial struct {
	DurationOverrides  []testFaultDurationOverride    `json:"duration_overrides,omitempty"`
	RecoveryOverrides  []testRecoveryDeadlineOverride `json:"recovery_overrides,omitempty"`
	PercentOverrides   []testFaultPercentOverride     `json:"percent_overrides,omitempty"`
	BandwidthOverrides []testFaultBandwidthOverride   `json:"bandwidth_overrides,omitempty"`
	LatencyOverrides   []testFaultLatencyOverride     `json:"latency_overrides,omitempty"`
	RemovedRules       []testFaultRuleRef             `json:"removed_rules,omitempty"`
	Status             string                         `json:"status"`
	Reason             string                         `json:"reason,omitempty"`
	Runs               []testRunReceipt               `json:"runs,omitempty"`
}

type testFaultMinimizationEvidence struct {
	Duration                 *testDurationMinimizationEvidence         `json:"duration,omitempty"`
	Stability                *testStabilityMinimizationEvidence        `json:"stability,omitempty"`
	Recovery                 *testRecoveryDeadlineMinimizationEvidence `json:"recovery,omitempty"`
	Percent                  *testPercentMinimizationEvidence          `json:"percent,omitempty"`
	Bandwidth                *testBandwidthMinimizationEvidence        `json:"bandwidth,omitempty"`
	Mode                     string                                    `json:"mode,omitempty"`
	Latency                  *testLatencyMinimizationEvidence          `json:"latency,omitempty"`
	Status                   string                                    `json:"status"`
	Reason                   string                                    `json:"reason,omitempty"`
	Target                   testMinimizationTarget                    `json:"target"`
	OriginalRules            int                                       `json:"original_rules"`
	RemainingRules           int                                       `json:"remaining_rules"`
	RemovedRules             []testFaultRuleRef                        `json:"removed_rules,omitempty"`
	OriginalConfirmed        bool                                      `json:"original_confirmed"`
	RunsUsed                 int                                       `json:"runs_used"`
	MaxRuns                  int                                       `json:"max_runs"`
	EstimatedWorkloadMinutes int                                       `json:"estimated_workload_minutes"`
	MaxWorkloadMinutes       int                                       `json:"max_workload_minutes"`
	BestRunID                string                                    `json:"best_run_id,omitempty"`
	Replay                   *testReplayManifest                       `json:"replay,omitempty"`
	Trials                   []testFaultMinimizationTrial              `json:"trials"`
}

type testFaultMinimizationOptions struct {
	DurationResolution         time.Duration
	StabilityResolution        time.Duration
	MaxRecoveryWithin          time.Duration
	RecoveryResolution         time.Duration
	PercentResolution          int
	MaxRateKiBPerSecond        int64
	RateResolutionKiBPerSecond int64
	Mode                       string
	RuleIndex                  int
	Resolution                 time.Duration
	Target                     testMinimizationTarget
	MaxRuns                    int
	MaxWorkloadMinutes         int
}

func parseTestMinimizationTarget(value string) (testMinimizationTarget, error) {
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return testMinimizationTarget{}, errors.New("--assertion must be STEP/PHASE or STEP/PHASE/REQUEST")
	}
	for _, part := range parts {
		if !testHTTPNamePattern.MatchString(part) {
			return testMinimizationTarget{}, errors.New("assertion contains an invalid name")
		}
	}
	target := testMinimizationTarget{Step: parts[0], Phase: parts[1]}
	if len(parts) == 3 {
		target.Request = parts[2]
	}
	return target, nil
}

// Copies each modified container; shared maps and unaffected specs stay read-only.
func removeTestFaultRules(scenario testScenario, removed []testFaultRuleRef) (testScenario, error) {
	if len(removed) > 20 {
		return testScenario{}, errors.New("replay supports at most 20 rule removals")
	}
	refs := map[testFaultRuleRef]bool{}
	for _, ref := range removed {
		if ref.Index < 0 || refs[ref] {
			return testScenario{}, errors.New("invalid or duplicate rule removal")
		}
		refs[ref] = true
	}
	result := scenario
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	found := 0
	for i, step := range scenario.Steps {
		if step.Load == nil {
			continue
		}
		load := *step.Load
		load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), step.Load.ChaosSchedule...)
		for j, phase := range step.Load.ChaosSchedule {
			rules := make([]testChaosRule, 0, len(phase.Rules))
			for k, rule := range phase.Rules {
				if refs[testFaultRuleRef{step.Name, phase.Name, k}] {
					found++
				} else {
					rules = append(rules, rule)
				}
			}
			if len(rules) != len(phase.Rules) {
				if len(rules) == 0 && step.Load.FaultCampaign == nil {
					return testScenario{}, errors.New("rule removal must retain a fault rule in each phase")
				}
				load.ChaosSchedule[j].Rules = rules
			}
		}
		result.Steps[i].Load = &load
	}
	if found != len(removed) {
		return testScenario{}, errors.New("rule removal references an unknown rule")
	}
	return result, nil
}

func testMinimizationRules(scenario testScenario, target testMinimizationTarget, minimum ...int) ([]testFaultRuleRef, error) {
	minRules := 2
	if len(minimum) > 0 {
		minRules = minimum[0]
	}
	for _, step := range scenario.Steps {
		if step.Name != target.Step || step.Load == nil {
			continue
		}
		for i, phase := range step.Load.ChaosSchedule {
			if phase.Name != target.Phase {
				continue
			}
			budget := phase.Recovery
			if target.Request != "" {
				budget = phase.StepRecovery[target.Request]
			}
			if budget == nil || i < 1 {
				return nil, errors.New("selected assertion is not a declared recovery budget")
			}
			if step.Load.FaultCampaign != nil && minRules == 2 {
				var refs []testFaultRuleRef
				for _, prior := range step.Load.ChaosSchedule[:i] {
					for k := range prior.Rules {
						refs = append(refs, testFaultRuleRef{step.Name, prior.Name, k})
					}
				}
				if len(refs) < 2 || len(step.Load.ChaosSchedule[i-1].Rules) != 1 {
					return nil, errors.New("campaign sequence minimization requires at least two faults before the selected recovery")
				}
				return refs, nil
			}
			fault := step.Load.ChaosSchedule[i-1]
			if len(fault.Rules) < minRules || len(fault.Rules) > 20 {
				return nil, fmt.Errorf("minimization requires %d..20 rules in the immediately preceding fault phase", minRules)
			}
			refs := make([]testFaultRuleRef, len(fault.Rules))
			for k := range refs {
				refs[k] = testFaultRuleRef{step.Name, fault.Name, k}
			}
			return refs, nil
		}
	}
	return nil, errors.New("selected recovery assertion is absent from the scenario")
}

// Match structured recovery evidence rather than transient error text from the
// runner. Interrupted load, unavailable evidence and different failures are not
// evidence that a candidate reproduces the chosen assertion.
func testMinimizationOutcome(receipt testRunReceipt, target testMinimizationTarget, stability ...bool) string {
	if receipt.CleanupError != "" || (receipt.Status != "passed" && receipt.Status != "failed") {
		return "inconclusive"
	}
	for _, step := range receipt.Steps {
		if step.Name != target.Step || step.Load == nil {
			continue
		}
		load := step.Load
		if load.Requests < 1 || (load.StopReason != "duration" && load.StopReason != "stages") {
			return "inconclusive"
		}
		for i, phase := range load.ChaosPhases {
			if phase.Name != target.Phase {
				continue
			}
			recovery := testPhaseRecoveryEvidence(phase)[target.Request]
			if recovery == nil || recovery.WindowsChecked < 1 || phase.StartedAt.IsZero() || !phase.FinishedAt.After(phase.StartedAt) || i < 1 {
				return "inconclusive"
			}
			faultPhase := load.ChaosPhases[i-1]
			if faultPhase.StartedAt.IsZero() || !faultPhase.FinishedAt.After(faultPhase.StartedAt) {
				return "inconclusive"
			}
			fault := faultPhase.Chaos
			if fault == nil || fault.RulesInstalled < 1 || len(fault.Matches) != fault.RulesInstalled {
				return "inconclusive"
			}
			var matches int64
			for _, match := range fault.Matches {
				matches += match.Matches
			}
			if matches < 1 {
				return "inconclusive"
			}
			if !recovery.Passed {
				if receipt.Status != "failed" || !testSelectedRecoveryFailure(recovery, len(stability) > 0 && stability[0]) {
					return "inconclusive"
				}
				return "reproduced"
			}
			if receipt.Status == "passed" {
				return "passed"
			}
			return "inconclusive"
		}
	}
	return "inconclusive"
}

func validateTestMinimization(prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions) ([]testFaultRuleRef, error) {
	if options.MaxRuns < 2 || options.MaxRuns > 20 {
		return nil, errors.New("--max-runs must be between 2 and 20 (including confirmations)")
	}
	if options.MaxWorkloadMinutes < 1 {
		return nil, errors.New("--max-workload-minutes must be positive")
	}
	if options.Mode != "" && options.Mode != "rules" && !isTestConfigurationMinimization(options.Mode) {
		return nil, errors.New("--mode must be rules, latency, bandwidth, percent, recovery, stability, or duration")
	}
	if original == nil || prepared.Engine != "real-vm" || original.Replay == nil || (!isTestConfigurationMinimization(options.Mode) && (len(original.Replay.RemovedRules) > 0 || len(original.Replay.LatencyOverrides) > 0 || len(original.Replay.BandwidthOverrides) > 0 || len(original.Replay.PercentOverrides) > 0 || len(original.Replay.RecoveryOverrides) > 0 || len(original.Replay.DurationOverrides) > 0)) {
		return nil, errors.New("select an original, unreduced real-VM replayable failure")
	}
	if err := validateTestScenarioSteps(prepared.Scenario, nil); err != nil {
		return nil, err
	}
	minRules := 2
	if isTestConfigurationMinimization(options.Mode) {
		minRules = 1
	}
	refs, err := testMinimizationRules(prepared.Scenario, options.Target, minRules)
	if err != nil {
		return nil, err
	}
	if options.Mode == "duration" {
		selected, _, err := testDurationSelection(prepared.Scenario, original.Replay, options)
		if err != nil {
			return nil, err
		}
		pinned := minimizedTestReplay(original.Replay, prepared.Scenario, original.Replay.RemovedRules, testDurationEdits(original.Replay, selected))
		if testDurationMinimizationOutcome(*original, options, pinned) != "reproduced" {
			return nil, errors.New("original run lacks the selected completed fault duration and recovery failure")
		}
	}
	if options.Mode == "latency" {
		if _, _, err := testLatencySelection(prepared.Scenario, original.Replay, options); err != nil {
			return nil, err
		}
	}
	if options.Mode == "bandwidth" {
		if _, _, err := testBandwidthSelection(prepared.Scenario, original.Replay, options); err != nil {
			return nil, err
		}
	}
	if options.Mode == "percent" {
		if _, _, err := testPercentSelection(prepared.Scenario, original.Replay, options); err != nil {
			return nil, err
		}
	}
	if options.Mode == "stability" {
		if _, err := testStabilitySelection(prepared.Scenario, original.Replay, options); err != nil {
			return nil, err
		}
		spec, _, _ := testRecoveryDeadlineSpec(prepared.Scenario, options.Target)
		pinned := minimizedTestReplay(original.Replay, prepared.Scenario, original.Replay.RemovedRules, testStabilityEdits(original.Replay, options.Target, spec.Stability))
		if testStabilityMinimizationOutcome(*original, options, pinned) != "reproduced" {
			return nil, errors.New("original run lacks the selected completed stability relapse")
		}
	}
	if options.Mode == "recovery" {
		if _, _, err := testRecoveryDeadlineSelection(prepared.Scenario, original.Replay, options); err != nil {
			return nil, err
		}
	}
	if options.Mode != "duration" && testConfiguredMinimizationOutcome(*original, options, original.Replay) != "reproduced" {
		return nil, errors.New("original run lacks a completed, matched fault and the selected recovery failure")
	}
	if cost := 2 * estimateTestWorkloadMinutes(prepared.Scenario, 1); cost > options.MaxWorkloadMinutes {
		return nil, fmt.Errorf("two confirmation runs require up to %d workload-minutes, exceeding the limit", cost)
	}
	return refs, nil
}

func minimizedTestReplay(original *testReplayManifest, scenario testScenario, removed []testFaultRuleRef, overrides ...testFaultReplayOverrides) *testReplayManifest {
	manifest := *original
	manifest.DurationOverrides = append([]testFaultDurationOverride(nil), original.DurationOverrides...)
	manifest.RemovedRules = append([]testFaultRuleRef(nil), removed...)
	manifest.LatencyOverrides = append([]testFaultLatencyOverride(nil), original.LatencyOverrides...)
	manifest.BandwidthOverrides = append([]testFaultBandwidthOverride(nil), original.BandwidthOverrides...)
	manifest.PercentOverrides = append([]testFaultPercentOverride(nil), original.PercentOverrides...)
	manifest.RecoveryOverrides = append([]testRecoveryDeadlineOverride(nil), original.RecoveryOverrides...)
	if len(overrides) > 0 {
		manifest.DurationOverrides = append([]testFaultDurationOverride(nil), overrides[0].Duration...)
		manifest.LatencyOverrides = append([]testFaultLatencyOverride(nil), overrides[0].Latency...)
		manifest.BandwidthOverrides = append([]testFaultBandwidthOverride(nil), overrides[0].Bandwidth...)
		manifest.PercentOverrides = append([]testFaultPercentOverride(nil), overrides[0].Percent...)
		manifest.RecoveryOverrides = append([]testRecoveryDeadlineOverride(nil), overrides[0].Recovery...)
	}
	manifest.ReducedConfigurationSHA256 = ""
	manifest.Version = testFaultReplayVersion(&manifest)
	if manifest.Version > 1 {
		manifest.ReducedConfigurationSHA256 = newTestReplayManifest(original.Scenario, original.Profile, original.MatrixCase, scenario).ConfigurationSHA256
	}

	return &manifest
}

// The callback owns a complete fresh run and returns after cleanup. A successful
// diagnostic search is reported as passed even though its reproducer fails.
func runTestFaultMinimization(ctx context.Context, prepared testPreparedScenario, original *testRunReceipt, options testFaultMinimizationOptions, execute func(context.Context, testRunPlan) testRunReceipt) (receipt testRunReceipt) {
	started := time.Now().UTC()
	evidence := &testFaultMinimizationEvidence{Status: "inconclusive", Target: options.Target, MaxRuns: options.MaxRuns, MaxWorkloadMinutes: options.MaxWorkloadMinutes, Mode: options.Mode}
	receipt = testRunReceipt{Scenario: prepared.Name, Engine: "real-vm", Profile: original.Profile, ReplayOf: original.RunID, Status: "failed", StartedAt: started, Minimization: evidence}
	defer func() {
		receipt.FinishedAt = time.Now().UTC()
		receipt.DurationMS = receipt.FinishedAt.Sub(started).Milliseconds()
		if evidence.Status == "inconclusive" {
			receipt.Error = "fault minimization inconclusive: " + evidence.Reason
		}
	}()
	refs, err := validateTestMinimization(prepared, original, options)
	if err != nil {
		evidence.Reason = err.Error()
		return
	}
	evidence.OriginalRules, evidence.RemainingRules = len(refs), len(refs)
	perRun := estimateTestWorkloadMinutes(prepared.Scenario, 1)
	confirm := func(candidate testPreparedScenario, removed []testFaultRuleRef, overrides ...testFaultReplayOverrides) testFaultMinimizationTrial {
		trial := testFaultMinimizationTrial{RemovedRules: append([]testFaultRuleRef(nil), removed...), Status: "inconclusive"}
		if evidence.RunsUsed+2 > options.MaxRuns || perRun > (options.MaxWorkloadMinutes-evidence.EstimatedWorkloadMinutes)/2 {
			trial.Reason = "run or workload-minute budget exhausted before a confirmation pair"
			return trial
		}
		manifest := minimizedTestReplay(original.Replay, candidate.Scenario, removed, overrides...)
		trial.DurationOverrides = append([]testFaultDurationOverride(nil), manifest.DurationOverrides...)
		trial.LatencyOverrides = append([]testFaultLatencyOverride(nil), manifest.LatencyOverrides...)
		trial.BandwidthOverrides = append([]testFaultBandwidthOverride(nil), manifest.BandwidthOverrides...)
		trial.PercentOverrides = append([]testFaultPercentOverride(nil), manifest.PercentOverrides...)
		trial.RecoveryOverrides = append([]testRecoveryDeadlineOverride(nil), manifest.RecoveryOverrides...)
		outcomes := make([]string, 0, 2)
		for n := 0; n < 2; n++ {
			if ctx.Err() != nil {
				trial.Reason = "minimization interrupted"
				return trial
			}
			plan := testRunPlan{Prepared: &candidate, Profile: original.Profile, Attempt: evidence.RunsUsed + 1, ReplayOf: original.RunID, ReplayCase: original.Replay.MatrixCase, ReplayManifest: manifest}
			run := execute(ctx, plan)
			run.Attempt = plan.Attempt
			run.ReplayOf = original.RunID
			if run.Status == "failed" || (run.Status == "passed" && (len(manifest.DurationOverrides) > 0 || len(manifest.RecoveryOverrides) > 0 || manifest.Sweep != nil)) {
				run.Replay = manifest
			}
			evidence.RunsUsed++
			evidence.EstimatedWorkloadMinutes += perRun
			trial.Runs = append(trial.Runs, run)
			if run.CleanupError != "" {
				receipt.CleanupError = run.CleanupError
				trial.Reason = "cleanup failed; no further runs started"
				return trial
			}
			if ctx.Err() != nil {
				trial.Reason = "minimization interrupted"
				return trial
			}
			outcome := testConfiguredMinimizationOutcome(run, options, manifest)
			if outcome == "inconclusive" {
				trial.Reason = "unrelated failure or incomplete fault/recovery evidence"
				return trial
			}
			outcomes = append(outcomes, outcome)
		}
		if outcomes[0] != outcomes[1] {
			trial.Reason = "confirmation runs disagree; failure is not repeatable"
			return trial
		}
		trial.Status = outcomes[0]
		return trial
	}
	initialRemoved := []testFaultRuleRef(nil)
	if isTestConfigurationMinimization(options.Mode) {
		initialRemoved = original.Replay.RemovedRules
	}
	var initial testFaultMinimizationTrial
	if options.Mode == "duration" {
		selected, _, _ := testDurationSelection(prepared.Scenario, original.Replay, options)
		initial = confirm(prepared, initialRemoved, testDurationEdits(original.Replay, selected))
	} else if options.Mode == "stability" {
		spec, _, _ := testRecoveryDeadlineSpec(prepared.Scenario, options.Target)
		initial = confirm(prepared, initialRemoved, testStabilityEdits(original.Replay, options.Target, spec.Stability))
	} else {
		initial = confirm(prepared, initialRemoved)
	}
	evidence.Trials = append(evidence.Trials, initial)
	if initial.Status != "reproduced" {
		evidence.Reason = initial.Reason
		if initial.Status == "passed" {
			evidence.Reason = "original failure did not reproduce in two fresh runs"
		}
		return
	}
	evidence.OriginalConfirmed = true
	evidence.BestRunID = initial.Runs[1].RunID
	evidence.Replay = initial.Runs[1].Replay
	if options.Mode != "stability" && options.Mode != "duration" {
		evidence.Replay = minimizedTestReplay(original.Replay, prepared.Scenario, initialRemoved)
	}
	if options.Mode == "recovery" {
		evidence.RemovedRules = append([]testFaultRuleRef(nil), initialRemoved...)
		evidence.BestRunID = ""
		evidence.Replay = nil
		runTestRecoveryDeadlineMinimization(ctx, prepared, original, options, evidence, confirm)
		if evidence.Status != "inconclusive" {
			receipt.Status = "passed"
		}
		return
	}
	if isTestConfigurationMinimization(options.Mode) {
		evidence.RemovedRules = append([]testFaultRuleRef(nil), initialRemoved...)
		if options.Mode == "latency" {
			runTestLatencyMinimization(ctx, prepared, original, options, evidence, confirm)
		} else if options.Mode == "bandwidth" {
			runTestBandwidthMinimization(ctx, prepared, original, options, evidence, confirm)
		} else if options.Mode == "duration" {
			runTestDurationMinimization(ctx, prepared, original, options, evidence, confirm)
		} else if options.Mode == "stability" {
			runTestStabilityMinimization(ctx, prepared, original, options, evidence, confirm)
		} else {
			runTestPercentMinimization(ctx, prepared, original, options, evidence, confirm)
		}
		if evidence.Status != "inconclusive" {
			receipt.Status = "passed"
		}
		return
	}
	campaignSequence := false
	for _, step := range prepared.Scenario.Steps {
		campaignSequence = campaignSequence || (step.Name == options.Target.Step && step.Load != nil && step.Load.FaultCampaign != nil)
	}
	removed := []testFaultRuleRef{}
	// Restart after an accepted removal: a rule rejected earlier can become
	// redundant after another removal. This proves only constrained 1-minimality.
	for {
		changed := false
		for _, ref := range refs {
			// Preserve the triggering fault; remove only its preceding campaign history.
			if campaignSequence && ref == refs[len(refs)-1] {
				continue
			}
			alreadyRemoved := false
			for _, r := range removed {
				alreadyRemoved = alreadyRemoved || r == ref
			}
			if alreadyRemoved || len(refs)-len(removed) <= 1 {
				continue
			}
			proposal := append(append([]testFaultRuleRef(nil), removed...), ref)
			variant, err := removeTestFaultRules(prepared.Scenario, proposal)
			if err == nil {
				err = validateTestScenarioSteps(variant, nil)
			}
			if err != nil {
				evidence.Trials = append(evidence.Trials, testFaultMinimizationTrial{RemovedRules: proposal, Status: "invalid", Reason: "removal violates scenario validation"})
				continue
			}
			candidate := prepared
			candidate.Scenario = variant
			trial := confirm(candidate, proposal)
			evidence.Trials = append(evidence.Trials, trial)
			if trial.Status == "inconclusive" {
				evidence.Reason = trial.Reason
				return
			}
			if trial.Status == "reproduced" {
				removed = proposal
				evidence.RemovedRules = append([]testFaultRuleRef(nil), removed...)
				evidence.RemainingRules = len(refs) - len(removed)
				evidence.BestRunID = trial.Runs[1].RunID
				evidence.Replay = minimizedTestReplay(original.Replay, variant, removed)
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}
	if ctx.Err() != nil {
		evidence.Reason = "minimization interrupted"
		return
	}
	evidence.Status = "minimized"
	if len(removed) == 0 {
		evidence.Status = "unchanged"
	}
	receipt.Status = "passed"
	return
}
