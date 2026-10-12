package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

const testResilienceMaxRuns = 20

type testResilienceSpec struct {
	CampaignCoverage   *testCampaignCoverageSpec `yaml:"campaign_coverage,omitempty" json:"campaign_coverage,omitempty"`
	coverageExpanded   bool
	campaignCase       bool
	SweepRegression    *testSweepRegressionSpec `yaml:"sweep_regression,omitempty" json:"sweep_regression,omitempty"`
	FaultSweep         *testFaultSweepSpec      `yaml:"fault_sweep,omitempty" json:"fault_sweep,omitempty"`
	sweepExpanded      bool
	RecoveryRegression *testRecoveryRegressionSpec `yaml:"recovery_regression,omitempty"`
	FaultMatrix        []testFaultMatrixCaseSpec   `yaml:"fault_matrix,omitempty"`
	Runs               int                         `yaml:"runs"`
	MinPassRate        float64                     `yaml:"min_pass_rate"`
}
type testResilienceEvidence struct {
	Runs           int                              `json:"runs"`
	Passed         int                              `json:"passed"`
	Failed         int                              `json:"failed"`
	Skipped        int                              `json:"skipped"`
	PassRate       float64                          `json:"pass_rate"`
	MinPassRate    float64                          `json:"min_pass_rate"`
	RequiredPasses int                              `json:"required_passes"`
	Recovery       []testResilienceRecoveryEvidence `json:"recovery,omitempty"`
	History        []testRunReceipt                 `json:"history"`
}
type testResilienceRecoveryEvidence struct {
	Request string  `json:"request,omitempty"`
	Step    string  `json:"step"`
	Phase   string  `json:"phase"`
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
}

func validateTestResilience(scenario testScenario) error {
	var err error
	scenario, err = expandTestFaultSweep(scenario)
	if err != nil {
		return err
	}
	spec := scenario.Resilience
	if spec == nil {
		return nil
	}
	if r := spec.RecoveryRegression; r != nil {
		if !finiteNonnegative(r.P95IncreasePercent) || r.P95IncreasePercent > 1000 || r.MinSamples < 2 || r.MinSamples > spec.Runs {
			return fmt.Errorf("resilience.recovery_regression requires p95_increase_percent between 0 and 1000 and min_samples between 2 and runs")
		}
	}
	minRuns := 2
	if spec.CampaignCoverage != nil || spec.campaignCase {
		minRuns = 1
	}
	if spec.Runs < minRuns || spec.Runs > testResilienceMaxRuns {
		return fmt.Errorf("resilience.runs must be between %d and %d", minRuns, testResilienceMaxRuns)
	}
	if math.IsNaN(spec.MinPassRate) || math.IsInf(spec.MinPassRate, 0) || spec.MinPassRate <= 0 || spec.MinPassRate > 1 {
		return fmt.Errorf("resilience.min_pass_rate must be greater than 0 and at most 1")
	}
	if len(scenario.Steps) == 0 {
		return fmt.Errorf("resilience requires a staged scenario with recovery assertions")
	}
	if len(scenario.Steps[0].Requests)+len(scenario.Steps[0].Checks) == 0 {
		return fmt.Errorf("resilience requires HTTP baseline assertions in the first step")
	}
	recovery := false
	for _, step := range scenario.Steps {
		if step.Load != nil {
			for _, phase := range step.Load.ChaosSchedule {
				recovery = recovery || phase.Recovery != nil || len(phase.StepRecovery) > 0
			}
		}
	}
	if !recovery {
		return fmt.Errorf("resilience requires at least one timed chaos recovery assertion")
	}
	if err := validateTestSweepRegression(scenario); err != nil {
		return err
	}
	return validateTestFaultMatrix(scenario)
}

func testResilienceRuns(scenario testScenario) int {
	if scenario.Resilience != nil {
		cases := len(scenario.Resilience.FaultMatrix)
		if scenario.Resilience.CampaignCoverage != nil {
			cases = len(scenario.Resilience.CampaignCoverage.Seeds)
		}
		if scenario.Resilience.FaultSweep != nil {
			cases = len(scenario.Resilience.FaultSweep.Values)
		}
		return scenario.Resilience.Runs * max(1, cases)
	}
	return 1
}

// Each callback runs a complete isolated scenario and returns after cleanup.
// Failed assertions may be tolerated; cleanup failures and cancellation may not.
func runTestResilience(ctx context.Context, plan testRunPlan, execute func(context.Context, testRunPlan) testRunReceipt) testRunReceipt {
	p := plan.Prepared
	spec := p.Scenario.Resilience
	started := time.Now().UTC()
	evidence := &testResilienceEvidence{Runs: spec.Runs, MinPassRate: spec.MinPassRate, RequiredPasses: max(1, int(math.Ceil(math.Nextafter(spec.MinPassRate*float64(spec.Runs), math.Inf(-1)))))}
	receipt := testRunReceipt{Scenario: p.Name, Engine: p.Engine, Profile: plan.Profile, StartedAt: started, Status: "failed", Resilience: evidence, RecoveryWorkloadSHA256: recoveryWorkloadSHA256(p.Scenario)}
	copyPrepared := *p
	copyPrepared.Scenario.Resilience = nil
	runPlan := plan
	runPlan.Prepared = &copyPrepared
	stopReason := ""
	for attempt := 1; attempt <= spec.Runs; attempt++ {
		if ctx.Err() != nil {
			stopReason = "resilience execution interrupted: " + ctx.Err().Error()
		}
		var run testRunReceipt
		if stopReason != "" {
			now := time.Now().UTC()
			run = testRunReceipt{Scenario: p.Name, Engine: p.Engine, Profile: plan.Profile, Status: "skipped", SkipReason: stopReason, StartedAt: now, FinishedAt: now}
		} else {
			runPlan.Attempt = attempt
			run = execute(ctx, runPlan)
			if run.CleanupError != "" {
				stopReason = "resilience stopped after cleanup failed"
				receipt.CleanupError = fmt.Sprintf("run %d: %s", attempt, run.CleanupError)
			}
		}
		run.CampaignSeed = runPlan.CampaignSeed
		run.Attempt = attempt
		evidence.History = append(evidence.History, run)
		switch {
		case run.Status == "passed" && run.CleanupError == "":
			evidence.Passed++
		case run.Status == "skipped":
			evidence.Skipped++
		default:
			evidence.Failed++
		}
	}
	if ctx.Err() != nil {
		stopReason = "resilience execution interrupted: " + ctx.Err().Error()
	}
	evidence.PassRate = float64(evidence.Passed) / float64(spec.Runs)
	evidence.Recovery = summarizeTestResilienceRecovery(evidence.History)
	receipt.FinishedAt = time.Now().UTC()
	receipt.DurationMS = receipt.FinishedAt.Sub(started).Milliseconds()
	if evidence.Passed >= evidence.RequiredPasses && evidence.Skipped == 0 && receipt.CleanupError == "" && ctx.Err() == nil {
		receipt.Status = "passed"
	} else {
		receipt.Error = fmt.Sprintf("resilience pass rate %.2f%% (%d/%d) requires %.2f%% (%d passes)", 100*evidence.PassRate, evidence.Passed, spec.Runs, 100*spec.MinPassRate, evidence.RequiredPasses)
		if evidence.Skipped > 0 && stopReason == "" {
			receipt.Error += "; one or more runs were skipped"
		}
		if stopReason != "" {
			receipt.Error += "; " + stopReason
		}
	}
	return receipt
}

func summarizeTestResilienceRecovery(history []testRunReceipt) []testResilienceRecoveryEvidence {
	type key struct{ step, phase, request string }
	samples := map[key][]float64{}
	for _, run := range history {
		for _, step := range run.Steps {
			if step.Load == nil {
				continue
			}
			for _, phase := range step.Load.ChaosPhases {
				for name, r := range phase.StepRecovery {
					if r != nil && r.Passed && r.RecoveryMS != nil {
						k := key{step.Name, phase.Name, name}
						samples[k] = append(samples[k], *r.RecoveryMS)
					}
				}
				r := phase.Recovery
				if r != nil && r.Passed && r.RecoveryMS != nil {
					k := key{step: step.Name, phase: phase.Name}
					samples[k] = append(samples[k], *r.RecoveryMS)
				}
			}
		}
	}
	keys := make([]key, 0, len(samples))
	for k := range samples {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].step != keys[j].step {
			return keys[i].step < keys[j].step
		}
		if keys[i].phase != keys[j].phase {
			return keys[i].phase < keys[j].phase
		}
		return keys[i].request < keys[j].request
	})
	result := make([]testResilienceRecoveryEvidence, 0, len(keys))
	for _, k := range keys {
		values := samples[k]
		sort.Float64s(values)
		percentile := func(p int) float64 { return values[(len(values)*p+99)/100-1] }
		result = append(result, testResilienceRecoveryEvidence{Step: k.step, Phase: k.phase, Request: k.request, Samples: len(values), P50MS: percentile(50), P95MS: percentile(95)})
	}
	return result
}
