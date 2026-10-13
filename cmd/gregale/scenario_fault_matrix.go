package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"
)

const testFaultMatrixMaxCases = 10

type testFaultMatrixCaseSpec struct {
	Campaign      *testCampaignSeedSetting `yaml:"-" json:"campaign,omitempty"`
	PhaseDuration string                   `yaml:"-" json:"phase_duration,omitempty"`
	Sweep         *testFaultSweepSetting   `yaml:"-" json:"sweep,omitempty"`
	Name          string                   `yaml:"name"`
	Step          string                   `yaml:"step"`
	Phase         string                   `yaml:"phase"`
	Rules         []testChaosRule          `yaml:"rules"`
	MinPassRate   *float64                 `yaml:"min_pass_rate,omitempty"`
	Recovery      *testLoadRecoverySpec    `yaml:"recovery,omitempty"`
}
type testFaultMatrixEvidence struct {
	CampaignCoverage *testCampaignCoverageEvidence `json:"campaign_coverage,omitempty"`
	Sweep            *testFaultSweepSpec           `json:"sweep,omitempty"`
	Passed           int                           `json:"passed"`
	Failed           int                           `json:"failed"`
	Skipped          int                           `json:"skipped"`
	Cases            []testRunReceipt              `json:"cases"`
}

// Copy every modified container so validating or executing a case never changes
// the template or another case. Unmodified fields remain read-only.
func applyTestFaultMatrixCase(scenario testScenario, spec testFaultMatrixCaseSpec) (testScenario, error) {
	result := scenario
	if scenario.Resilience == nil {
		return result, fmt.Errorf("fault_matrix requires resilience")
	}
	resilience := *scenario.Resilience
	resilience.FaultMatrix = nil
	resilience.FaultSweep = nil
	resilience.CampaignCoverage = nil
	resilience.coverageExpanded = false
	resilience.campaignCase = spec.Campaign != nil
	resilience.sweepExpanded = false
	if spec.MinPassRate != nil {
		resilience.MinPassRate = *spec.MinPassRate
	}
	result.Resilience = &resilience
	result.Steps = append([]testScenarioStep(nil), scenario.Steps...)
	if spec.Campaign != nil {
		return applyTestCampaignSeedCase(result, spec)
	}
	for i, step := range scenario.Steps {
		if step.Name != spec.Step || step.Load == nil {
			continue
		}
		load := *step.Load
		load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), step.Load.ChaosSchedule...)
		for j, phase := range load.ChaosSchedule {
			if phase.Name != spec.Phase {
				continue
			}
			if len(phase.Rules) == 0 {
				return result, fmt.Errorf("phase %q in step %q must already be a fault phase", spec.Phase, spec.Step)
			}
			if j+1 >= len(load.ChaosSchedule) || len(load.ChaosSchedule[j+1].Rules) > 0 || (load.ChaosSchedule[j+1].Recovery == nil && len(load.ChaosSchedule[j+1].StepRecovery) == 0) {
				return result, fmt.Errorf("target fault phase requires a recovery assertion immediately after it")
			}
			load.ChaosSchedule[j].Rules = append([]testChaosRule(nil), spec.Rules...)
			if spec.PhaseDuration != "" {
				if spec.Sweep == nil || spec.Sweep.Mode != "duration" {
					return result, fmt.Errorf("phase duration edits require a duration sweep")
				}
				if load.Duration == "" || len(load.Stages) > 0 {
					return result, fmt.Errorf("duration sweeps require a fixed duration load without stages")
				}
				before, err := time.ParseDuration(phase.Duration)
				if err != nil {
					return result, fmt.Errorf("invalid original fault phase duration")
				}
				after, err := time.ParseDuration(spec.PhaseDuration)
				if err != nil || after < time.Second || after > testLoadMaxDuration || after%time.Millisecond != 0 {
					return result, fmt.Errorf("invalid swept fault duration")
				}
				total, err := time.ParseDuration(load.Duration)
				if err != nil {
					return result, fmt.Errorf("invalid original load duration")
				}
				adjusted := total + after - before
				if adjusted <= 0 || adjusted > testLoadMaxDuration {
					return result, fmt.Errorf("swept load duration must remain at most 5m")
				}
				load.Duration = adjusted.String()
				load.ChaosSchedule[j].Duration = after.String()
			}
			if spec.Recovery != nil {
				recovery := *spec.Recovery
				load.ChaosSchedule[j+1].Recovery = &recovery
			}
			result.Steps[i].Load = &load
			return result, nil
		}
	}
	return result, fmt.Errorf("unknown fault phase %q in step %q", spec.Phase, spec.Step)
}

func validateTestFaultMatrix(scenario testScenario) error {
	cases := scenario.Resilience.FaultMatrix
	if len(cases) == 0 {
		return nil
	}
	if len(cases) < 2 || len(cases) > testFaultMatrixMaxCases {
		return fmt.Errorf("resilience.fault_matrix must contain between 2 and %d cases", testFaultMatrixMaxCases)
	}
	names := map[string]bool{}
	for _, spec := range cases {
		fail := func(err error) error { return fmt.Errorf("resilience.fault_matrix case %q: %w", spec.Name, err) }
		if !testHTTPNamePattern.MatchString(spec.Name) || names[spec.Name] {
			return fail(fmt.Errorf("invalid or duplicate name"))
		}
		names[spec.Name] = true
		if len(spec.Rules) == 0 && spec.Campaign == nil {
			return fail(fmt.Errorf("rules must not be empty"))
		}
		if spec.MinPassRate != nil && (math.IsNaN(*spec.MinPassRate) || math.IsInf(*spec.MinPassRate, 0) || *spec.MinPassRate <= 0 || *spec.MinPassRate > 1) {
			return fail(fmt.Errorf("min_pass_rate must be greater than 0 and at most 1"))
		}
		variant, err := applyTestFaultMatrixCase(scenario, spec)
		if err != nil {
			return fail(err)
		}
		if err := validateTestScenarioSteps(variant, nil); err != nil {
			return fail(err)
		}
	}
	return nil
}

func runTestFaultMatrix(ctx context.Context, plan testRunPlan, execute func(context.Context, testRunPlan) testRunReceipt) testRunReceipt {
	prepared := *plan.Prepared
	expanded, err := expandTestFaultSweep(prepared.Scenario)
	if err != nil {
		return testRunReceipt{Scenario: prepared.Name, Engine: prepared.Engine, Profile: plan.Profile, Status: "failed", Error: err.Error()}
	}
	prepared.Scenario = expanded
	if err := validateTestCampaignGuidanceOptions(expanded, prepared.Name, []string{plan.Profile}, nil); err != nil {
		return testRunReceipt{Scenario: prepared.Name, Engine: prepared.Engine, Profile: plan.Profile, Status: "failed", Error: err.Error()}
	}
	p := &prepared
	started := time.Now().UTC()
	evidence := &testFaultMatrixEvidence{Sweep: p.Scenario.Resilience.FaultSweep}
	receipt := testRunReceipt{Scenario: p.Name, Engine: p.Engine, Profile: plan.Profile, Status: "passed", StartedAt: started, FaultMatrix: evidence}
	if coverage := p.Scenario.Resilience.CampaignCoverage; coverage != nil && hasTestCampaignAggregatePolicy(coverage) {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			receipt.Status = "failed"
			receipt.Error = fmt.Sprintf("create campaign run identity: %v", err)
			return receipt
		}
		receipt.RunID = hex.EncodeToString(random)
	}
	receipt.ReplayOf = plan.ReplayOf
	stopReason := ""
	var failures []string
	for _, spec := range p.Scenario.Resilience.FaultMatrix {
		if ctx.Err() != nil {
			stopReason = "fault matrix interrupted: " + ctx.Err().Error()
		}
		var run testRunReceipt
		if stopReason != "" {
			now := time.Now().UTC()
			run = testRunReceipt{Scenario: p.Name, Engine: p.Engine, Profile: plan.Profile, Status: "skipped", SkipReason: stopReason, StartedAt: now, FinishedAt: now}
		} else {
			variant, err := applyTestFaultMatrixCase(p.Scenario, spec)
			if err != nil {
				run = testRunReceipt{Scenario: p.Name, Engine: p.Engine, Profile: plan.Profile, Status: "failed", Error: err.Error()}
			} else {
				prepared := *p
				prepared.Scenario = variant
				casePlan := plan
				casePlan.Prepared = &prepared
				if plan.ReplayManifest != nil && plan.ReplayManifest.CampaignCoverage {
					casePlan.ReplayManifest = nil
				}
				casePlan.ReplayCase = spec.Name
				casePlan.ReplaySweep = spec.Sweep
				casePlan.CampaignSeed = spec.Campaign
				run = execute(ctx, casePlan)
			}
			if run.CleanupError != "" {
				stopReason = "fault matrix stopped after cleanup failed"
				receipt.CleanupError = fmt.Sprintf("case %q: %s", spec.Name, run.CleanupError)
			}
		}
		run.Case = spec.Name
		run.Sweep = spec.Sweep
		run.CampaignSeed = spec.Campaign
		run.Attempt = plan.Attempt
		evidence.Cases = append(evidence.Cases, run)
		switch {
		case run.Status == "passed" && run.CleanupError == "":
			evidence.Passed++
		case run.Status == "skipped":
			evidence.Skipped++
			failures = append(failures, fmt.Sprintf("case %q skipped: %s", spec.Name, run.SkipReason))
		default:
			evidence.Failed++
			failures = append(failures, fmt.Sprintf("case %q failed: %s", spec.Name, run.Error))
		}
	}
	if len(failures) > 0 || ctx.Err() != nil || receipt.CleanupError != "" {
		receipt.Status = "failed"
		receipt.Error = "fault matrix failed: " + strings.Join(failures, "; ")
		if ctx.Err() != nil {
			receipt.Error += "; " + ctx.Err().Error()
		}
	}
	if p.Scenario.Resilience.CampaignCoverage != nil {
		evidence.CampaignCoverage = summarizeTestCampaignCoverage(p.Scenario, evidence)
		if err := evaluateTestCampaignCoverageRequirements(evidence.CampaignCoverage); err != nil {
			receipt.Status = "failed"
			if receipt.Error != "" {
				receipt.Error += "; "
			}
			receipt.Error += err.Error()
		}
		if err := evaluateTestCampaignCoverageRegression(p.Scenario, evidence.CampaignCoverage); err != nil {
			receipt.Status = "failed"
			if receipt.Error != "" {
				receipt.Error += "; "
			}
			receipt.Error += err.Error()
		}
		if err := evaluateTestCampaignRecoveryRegression(p.Scenario, &receipt); err != nil {
			receipt.Status = "failed"
			if receipt.Error != "" {
				receipt.Error += "; "
			}
			receipt.Error += err.Error()
		}
		if receipt.Status == "failed" && hasTestCampaignAggregatePolicy(p.Scenario.Resilience.CampaignCoverage) {
			receipt.Replay = newTestReplayManifest(p.Name, plan.Profile, "", p.Scenario)
		}
	}
	receipt.FinishedAt = time.Now().UTC()
	receipt.DurationMS = receipt.FinishedAt.Sub(started).Milliseconds()
	return receipt
}
