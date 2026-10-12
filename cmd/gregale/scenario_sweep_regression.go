package main

import (
	"fmt"
	"math"
	"strings"
)

type testSweepRegressionSpec struct {
	MaxPassRateDrop float64                     `yaml:"max_pass_rate_drop"`
	Recovery        *testRecoveryRegressionSpec `yaml:"recovery,omitempty"`
}
type testSweepRegressionEvidence struct {
	Status       string                        `json:"status"`
	ReportSHA256 string                        `json:"report_sha256"`
	Checks       []testSweepRegressionCheck    `json:"checks,omitempty"`
	Recovery     []testRecoveryRegressionCheck `json:"recovery,omitempty"`
	Error        string                        `json:"error,omitempty"`
}
type testSweepRegressionCheck struct {
	Case               string                `json:"case"`
	Setting            testFaultSweepSetting `json:"setting"`
	BaselinePassed     int                   `json:"baseline_passed"`
	CurrentPassed      int                   `json:"current_passed"`
	Runs               int                   `json:"runs"`
	Drop               float64               `json:"drop"`
	MaxDrop            float64               `json:"max_drop"`
	LostPassingSetting bool                  `json:"lost_passing_setting"`
	Passed             bool                  `json:"passed"`
}

func validateTestSweepRegression(scenario testScenario) error {
	if scenario.Resilience == nil || scenario.Resilience.SweepRegression == nil {
		return nil
	}
	spec := scenario.Resilience.SweepRegression
	if scenario.Resilience.FaultSweep == nil {
		return fmt.Errorf("resilience.sweep_regression requires fault_sweep")
	}
	if !finiteNonnegative(spec.MaxPassRateDrop) || spec.MaxPassRateDrop > 1 {
		return fmt.Errorf("sweep_regression.max_pass_rate_drop must be between 0 and 1")
	}
	if r := spec.Recovery; r != nil && (!finiteNonnegative(r.P95IncreasePercent) || r.P95IncreasePercent > 1000 || r.MinSamples < 2 || r.MinSamples > scenario.Resilience.Runs) {
		return fmt.Errorf("sweep_regression.recovery requires p95_increase_percent from 0 to 1000 and min_samples from 2 to runs")
	}
	return nil
}

// Recompute pass counts from complete leaf history. Summary fields cannot turn
// a skipped, corrupt or incompletely cleaned-up sweep into a usable baseline.
func sweepRegressionGroups(receipt *testRunReceipt, plan testRunPlan) (map[string]*testRunReceipt, error) {
	p := plan.Prepared
	scenario, err := expandTestFaultSweep(p.Scenario)
	if err != nil {
		return nil, err
	}
	if receipt.Scenario != p.Name || receipt.Engine != p.Engine || receipt.Profile != plan.Profile || receipt.Attempt != plan.Attempt || receipt.Case != plan.Case.Name || (receipt.Status == "passed" && receipt.Error != "") || receipt.CleanupError != "" || (receipt.Status != "passed" && receipt.Status != "failed") || receipt.FaultMatrix == nil || receipt.FaultMatrix.Sweep == nil {
		return nil, fmt.Errorf("sweep report has incompatible identity, cleanup or missing sweep evidence")
	}
	groups, err := recoveryGroups(receipt)
	if err != nil {
		return nil, err
	}
	if len(groups) != len(scenario.Resilience.FaultMatrix) {
		return nil, fmt.Errorf("sweep report settings do not match manifest")
	}
	seenIDs := map[string]bool{}
	totalPassed, totalFailed := 0, 0
	for _, c := range scenario.Resilience.FaultMatrix {
		group := groups[c.Name]
		if group == nil || group.Sweep == nil || *group.Sweep != *c.Sweep || group.Scenario != p.Name || group.Engine != p.Engine || group.Profile != plan.Profile || group.Attempt != plan.Attempt || (group.Status == "passed" && group.Error != "") || group.CleanupError != "" {
			return nil, fmt.Errorf("sweep case %q identity or setting mismatch", c.Name)
		}
		variant, err := applyTestFaultMatrixCase(scenario, c)
		if err != nil {
			return nil, err
		}
		if group.RecoveryWorkloadSHA256 == "" || group.RecoveryWorkloadSHA256 != recoveryWorkloadSHA256(variant) {
			return nil, fmt.Errorf("sweep case %q workload mismatch", c.Name)
		}
		e := group.Resilience
		if e == nil || e.Runs != scenario.Resilience.Runs || len(e.History) != e.Runs || e.MinPassRate != scenario.Resilience.MinPassRate {
			return nil, fmt.Errorf("sweep case %q lacks matching complete repeated runs or pass policy", c.Name)
		}
		attempts := map[int]bool{}
		passed := 0
		for _, run := range e.History {
			if run.Attempt < 1 || run.Attempt > e.Runs || attempts[run.Attempt] || run.RunID == "" || seenIDs[run.RunID] || run.Scenario != p.Name || run.Engine != p.Engine || run.Profile != plan.Profile || (run.Status == "passed" && run.Error != "") || run.CleanupError != "" || (run.Status != "passed" && run.Status != "failed") {
				return nil, fmt.Errorf("sweep case %q has incomplete, duplicate or incompatible leaf history", c.Name)
			}
			attempts[run.Attempt] = true
			seenIDs[run.RunID] = true
			if run.Status == "passed" {
				passed++
			}
		}
		required := max(1, int(math.Ceil(math.Nextafter(e.MinPassRate*float64(e.Runs), math.Inf(-1)))))
		expected := "failed"
		if passed >= required {
			expected = "passed"
			totalPassed++
		} else {
			totalFailed++
		}
		if e.Passed != passed || e.Failed != e.Runs-passed || e.Skipped != 0 || e.RequiredPasses != required || !finiteNonnegative(e.PassRate) || math.Abs(e.PassRate-float64(passed)/float64(e.Runs)) > 1e-12 || group.Status != expected {
			return nil, fmt.Errorf("sweep case %q summary disagrees with history", c.Name)
		}
	}
	matrix := receipt.FaultMatrix
	if matrix.Passed != totalPassed || matrix.Failed != totalFailed || matrix.Skipped != 0 {
		return nil, fmt.Errorf("sweep totals disagree with cases")
	}
	if totalFailed > 0 && receipt.Status != "failed" {
		return nil, fmt.Errorf("failed sweep settings masked by root status")
	}
	if totalFailed == 0 && receipt.Status != "passed" {
		return nil, fmt.Errorf("sweep root failed outside its setting outcomes")
	}
	return groups, nil
}

func prepareTestSweepBaselines(path string, plans []testRunPlan, outputs ...string) error {
	if path == "" {
		return nil
	}
	rows, digest, err := readTestBaselineReport(path, outputs)
	if err != nil {
		return err
	}
	index := map[testBaselineKey]*testRunReceipt{}
	for i := range rows {
		key := testReceiptBaselineKey(rows[i])
		if index[key] != nil {
			return fmt.Errorf("duplicate sweep baseline identity")
		}
		index[key] = &rows[i]
	}
	for i := range plans {
		plan := &plans[i]
		p := plan.Prepared
		if p.Engine != "real-vm" || p.Scenario.Resilience == nil || p.Scenario.Resilience.FaultSweep == nil || p.Scenario.Resilience.SweepRegression == nil {
			return fmt.Errorf("--sweep-baseline requires real-vm resilience.sweep_regression and fault_sweep for %q", p.Name)
		}
		baseline := index[testBaselineKey{p.Name, p.Engine, plan.Profile, plan.Case.Name, plan.Attempt}]
		if baseline == nil {
			return fmt.Errorf("sweep baseline missing %s/%s#%d", p.Name, plan.Profile, plan.Attempt)
		}
		if (baseline.SweepRegression != nil && baseline.SweepRegression.Status != "passed") || (baseline.RecoveryRegression != nil && baseline.RecoveryRegression.Status != "passed") {
			return fmt.Errorf("sweep baseline has an unsuccessful prior regression comparison")
		}
		groups, err := sweepRegressionGroups(baseline, *plan)
		if err != nil {
			return err
		}
		if spec := p.Scenario.Resilience.SweepRegression.Recovery; spec != nil {
			scenario, err := expandTestFaultSweep(p.Scenario)
			if err != nil {
				return err
			}
			for _, c := range scenario.Resilience.FaultMatrix {
				variant, err := applyTestFaultMatrixCase(scenario, c)
				if err != nil {
					return err
				}
				if _, err := recoverySamples(groups[c.Name], variant, spec.MinSamples); err != nil {
					return fmt.Errorf("baseline sweep case %q: %w", c.Name, err)
				}
			}
		}
		plan.SweepBaseline = baseline
		plan.SweepBaselineSHA256 = digest
	}
	return nil
}

func applyTestSweepBaseline(plan testRunPlan, receipt *testRunReceipt) {
	if plan.SweepBaseline == nil {
		return
	}
	evidence := &testSweepRegressionEvidence{Status: "passed", ReportSHA256: plan.SweepBaselineSHA256}
	receipt.SweepRegression = evidence
	fail := func(message string) {
		evidence.Status = "failed"
		evidence.Error = message
		receipt.Status = "failed"
		if receipt.Error != "" {
			receipt.Error += "; "
		}
		receipt.Error += "sweep regression failed: " + message
	}
	previous, err := sweepRegressionGroups(plan.SweepBaseline, plan)
	if err != nil {
		fail(err.Error())
		return
	}
	current, err := sweepRegressionGroups(receipt, plan)
	if err != nil {
		fail(err.Error())
		return
	}
	scenario, err := expandTestFaultSweep(plan.Prepared.Scenario)
	if err != nil {
		fail(err.Error())
		return
	}
	spec := scenario.Resilience.SweepRegression
	var failures []string
	for _, c := range scenario.Resilience.FaultMatrix {
		before, now := previous[c.Name], current[c.Name]
		drop := before.Resilience.PassRate - now.Resilience.PassRate
		lost := before.Status == "passed" && now.Status != "passed"
		check := testSweepRegressionCheck{Case: c.Name, Setting: *c.Sweep, BaselinePassed: before.Resilience.Passed, CurrentPassed: now.Resilience.Passed, Runs: now.Resilience.Runs, Drop: drop, MaxDrop: spec.MaxPassRateDrop, LostPassingSetting: lost, Passed: !lost && drop <= spec.MaxPassRateDrop+1e-12}
		evidence.Checks = append(evidence.Checks, check)
		if !check.Passed {
			if lost {
				failures = append(failures, fmt.Sprintf("%s=%s: previously passing setting now fails", c.Sweep.Mode, c.Sweep.Value))
			}
			if drop > spec.MaxPassRateDrop+1e-12 {
				failures = append(failures, fmt.Sprintf("%s=%s: pass-rate drop %.2f percentage points exceeds allowance %.2f", c.Sweep.Mode, c.Sweep.Value, drop*100, spec.MaxPassRateDrop*100))
			}
		}
		if spec.Recovery == nil {
			continue
		}
		variant, err := applyTestFaultMatrixCase(scenario, c)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		old, err := recoverySamples(before, variant, spec.Recovery.MinSamples)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		samples, err := recoverySamples(now, variant, spec.Recovery.MinSamples)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s=%s: %v", c.Sweep.Mode, c.Sweep.Value, err))
			continue
		}
		index := map[string]testResilienceRecoveryEvidence{}
		for _, s := range old {
			index[testRecoverySummaryKey(s.Step, s.Phase, s.Request)] = s
		}
		for _, s := range samples {
			b := index[testRecoverySummaryKey(s.Step, s.Phase, s.Request)]
			limit := b.P95MS * (1 + spec.Recovery.P95IncreasePercent/100)
			r := testRecoveryRegressionCheck{Case: c.Name, Step: s.Step, Phase: s.Phase, Request: s.Request, BaselineSamples: b.Samples, CurrentSamples: s.Samples, BaselineP95MS: b.P95MS, CurrentP95MS: s.P95MS, IncreasePercent: 100 * (s.P95MS/b.P95MS - 1), LimitMS: limit, Passed: s.P95MS <= limit+1e-12*math.Max(1, limit)}
			evidence.Recovery = append(evidence.Recovery, r)
			if !r.Passed {
				failures = append(failures, fmt.Sprintf("%s=%s recovery %s/%s p95 %.3fms exceeds %.3fms", c.Sweep.Mode, c.Sweep.Value, s.Step, testRecoveryDisplayPhase(s.Phase, s.Request), s.P95MS, limit))
			}
		}
	}
	if len(failures) > 0 {
		fail(strings.Join(failures, "; "))
	}
}
