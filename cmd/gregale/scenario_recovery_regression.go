package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type testRecoveryRegressionSpec struct {
	P95IncreasePercent float64 `yaml:"p95_increase_percent"`
	MinSamples         int     `yaml:"min_samples"`
}
type testRecoveryRegressionEvidence struct {
	Status       string                        `json:"status"`
	ReportSHA256 string                        `json:"report_sha256"`
	Checks       []testRecoveryRegressionCheck `json:"checks,omitempty"`
	Error        string                        `json:"error,omitempty"`
}
type testRecoveryRegressionCheck struct {
	Request         string  `json:"request,omitempty"`
	Case            string  `json:"case,omitempty"`
	Step            string  `json:"step"`
	Phase           string  `json:"phase"`
	BaselineSamples int     `json:"baseline_samples"`
	CurrentSamples  int     `json:"current_samples"`
	BaselineP95MS   float64 `json:"baseline_p95_ms"`
	CurrentP95MS    float64 `json:"current_p95_ms"`
	IncreasePercent float64 `json:"increase_percent"`
	LimitMS         float64 `json:"limit_ms"`
	Passed          bool    `json:"passed"`
}

func recoveryWorkloadSHA256(scenario testScenario) string {
	ports := map[string][]int{scenario.Project: scenario.TCPPorts}
	for name, service := range scenario.Services {
		ports[name] = service.TCPPorts
	}
	body, _ := json.Marshal(struct {
		Version int
		Steps   []testScenarioStep
		Ports   map[string][]int
		Timeout string
	}{1, scenario.Steps, ports, scenario.Timeout})
	sum := sha256.Sum256(body)
	return "v1:" + hex.EncodeToString(sum[:])
}

func recoveryGroups(receipt *testRunReceipt) (map[string]*testRunReceipt, error) {
	groups := map[string]*testRunReceipt{}
	if receipt.FaultMatrix == nil {
		groups[""] = receipt
		return groups, nil
	}
	for i := range receipt.FaultMatrix.Cases {
		group := &receipt.FaultMatrix.Cases[i]
		if group.Case == "" || groups[group.Case] != nil {
			return nil, fmt.Errorf("missing or duplicate recovery case %q", group.Case)
		}
		groups[group.Case] = group
	}
	return groups, nil
}
func recoveryVariants(scenario testScenario) (map[string]testScenario, error) {
	variants := map[string]testScenario{}
	if len(scenario.Resilience.FaultMatrix) == 0 {
		variants[""] = scenario
		return variants, nil
	}
	for _, spec := range scenario.Resilience.FaultMatrix {
		v, err := applyTestFaultMatrixCase(scenario, spec)
		if err != nil {
			return nil, err
		}
		variants[spec.Name] = v
	}
	return variants, nil
}

func recoverySamples(group *testRunReceipt, scenario testScenario, minSamples int) ([]testResilienceRecoveryEvidence, error) {
	if group.Resilience == nil {
		return nil, fmt.Errorf("report lacks repeated recovery evidence")
	}
	if group.Resilience.Runs < 2 || group.Resilience.Runs > testResilienceMaxRuns || len(group.Resilience.History) != group.Resilience.Runs {
		return nil, fmt.Errorf("recovery report has incomplete run history")
	}
	seenAttempts := map[int]bool{}
	for _, run := range group.Resilience.History {
		if run.Attempt < 1 || run.Attempt > group.Resilience.Runs || seenAttempts[run.Attempt] {
			return nil, fmt.Errorf("recovery history contains invalid or duplicate attempts")
		}
		seenAttempts[run.Attempt] = true
		seenRecoveries := map[string]bool{}

		if run.CleanupError != "" || run.Status == "skipped" {
			return nil, fmt.Errorf("recovery history includes cleanup failure or skipped runs")
		}
		for _, step := range run.Steps {
			if step.Load != nil {
				for _, phase := range step.Load.ChaosPhases {
					for name, r := range testPhaseRecoveryEvidence(phase) {
						if r == nil || !r.Passed {
							continue
						}
						key := testRecoverySummaryKey(step.Name, phase.Name, name)
						if seenRecoveries[key] {
							return nil, fmt.Errorf("duplicate confirmed recovery %s", key)
						}
						seenRecoveries[key] = true
						if r.RecoveryMS == nil || !finiteNonnegative(*r.RecoveryMS) || *r.RecoveryMS <= 0 || *r.RecoveryMS > loadMilliseconds(testLoadMaxDuration) {
							return nil, fmt.Errorf("invalid confirmed recovery time in %s", key)
						}
					}
				}
			}
		}
	}
	summaries := summarizeTestResilienceRecovery(group.Resilience.History)
	index := map[string]testResilienceRecoveryEvidence{}
	for _, summary := range summaries {
		index[testRecoverySummaryKey(summary.Step, summary.Phase, summary.Request)] = summary
	}
	var result []testResilienceRecoveryEvidence
	for _, step := range scenario.Steps {
		if step.Load != nil {
			for _, phase := range step.Load.ChaosSchedule {
				names := testEndpointRecoveryNames(phase.StepRecovery)
				if phase.Recovery != nil {
					names = append([]string{""}, names...)
				}
				for _, name := range names {
					key := testRecoverySummaryKey(step.Name, phase.Name, name)
					summary, ok := index[key]
					if !ok || summary.Samples < minSamples {
						return nil, fmt.Errorf("recovery %s has %d confirmed samples; requires %d", key, summary.Samples, minSamples)
					}
					result = append(result, summary)
				}
			}
		}
	}
	return result, nil
}

func prepareTestRecoveryBaselines(path string, plans []testRunPlan, outputs ...string) error {
	if path == "" {
		return nil
	}
	receipts, digest, err := readTestBaselineReport(path, outputs)
	if err != nil {
		return err
	}
	index := map[testBaselineKey]*testRunReceipt{}
	for i := range receipts {
		r := &receipts[i]
		key := testReceiptBaselineKey(*r)
		if index[key] != nil {
			return fmt.Errorf("duplicate recovery baseline run %s/%s#%d", key.Scenario, key.Profile, key.Attempt)
		}
		index[key] = r
	}
	for i := range plans {
		plan := &plans[i]
		p := plan.Prepared
		if p.Engine != "real-vm" || p.Scenario.Resilience == nil || p.Scenario.Resilience.RecoveryRegression == nil {
			return fmt.Errorf("--recovery-baseline requires real-vm resilience.recovery_regression for scenario %q", p.Name)
		}
		key := testBaselineKey{p.Name, p.Engine, plan.Profile, plan.Case.Name, plan.Attempt}
		baseline := index[key]
		if baseline == nil {
			return fmt.Errorf("recovery baseline is missing %s/%s#%d", p.Name, plan.Profile, plan.Attempt)
		}
		if baseline.Status != "passed" || baseline.Error != "" || baseline.CleanupError != "" {
			return fmt.Errorf("recovery baseline %s/%s must have passed execution and cleanup", p.Name, plan.Profile)
		}
		groups, err := recoveryGroups(baseline)
		if err != nil {
			return err
		}
		variants, err := recoveryVariants(p.Scenario)
		if err != nil {
			return err
		}
		if len(groups) != len(variants) {
			return fmt.Errorf("recovery baseline cases do not match scenario %q", p.Name)
		}
		for name, variant := range variants {
			group := groups[name]
			if group == nil || group.Status != "passed" || group.Error != "" || group.CleanupError != "" {
				return fmt.Errorf("recovery baseline case %q must have passed execution and cleanup", name)
			}
			if group.Scenario != p.Name || group.Engine != p.Engine || group.Profile != plan.Profile {
				return fmt.Errorf("recovery baseline case %q has mismatched scenario, engine or profile", name)
			}
			if group.RecoveryWorkloadSHA256 == "" {
				return fmt.Errorf("recovery baseline case %q lacks workload metadata; record a baseline with this CLI", name)
			}
			if group.RecoveryWorkloadSHA256 != recoveryWorkloadSHA256(variant) {
				return fmt.Errorf("recovery baseline case %q workload mismatch; record matching request templates, chaos rules, schedules and recovery budgets", name)
			}
			if _, err := recoverySamples(group, variant, p.Scenario.Resilience.RecoveryRegression.MinSamples); err != nil {
				return fmt.Errorf("recovery baseline case %q: %w", name, err)
			}
		}
		plan.RecoveryBaseline = baseline
		plan.RecoveryBaselineSHA256 = digest
	}
	return nil
}

func applyTestRecoveryBaseline(plan testRunPlan, receipt *testRunReceipt) {
	if plan.RecoveryBaseline == nil {
		return
	}
	evidence := &testRecoveryRegressionEvidence{Status: "passed", ReportSHA256: plan.RecoveryBaselineSHA256}
	receipt.RecoveryRegression = evidence
	if receipt.Status != "passed" || receipt.CleanupError != "" {
		evidence.Status = "not_compared"
		evidence.Error = "current resilience run did not pass execution and cleanup"
		return
	}
	variants, err := recoveryVariants(plan.Prepared.Scenario)
	if err != nil {
		evidence.Error = err.Error()
	}
	previous, err := recoveryGroups(plan.RecoveryBaseline)
	if err != nil {
		evidence.Error = err.Error()
	}
	current, err := recoveryGroups(receipt)
	if err != nil {
		evidence.Error = err.Error()
	}
	spec := plan.Prepared.Scenario.Resilience.RecoveryRegression
	// Preserve configured case order in reports.
	names := []string{""}
	if len(plan.Prepared.Scenario.Resilience.FaultMatrix) > 0 {
		names = nil
		for _, c := range plan.Prepared.Scenario.Resilience.FaultMatrix {
			names = append(names, c.Name)
		}
	}
	var failures []string
	if evidence.Error == "" {
		for _, name := range names {
			variant := variants[name]
			if current[name] == nil || previous[name] == nil {
				failures = append(failures, fmt.Sprintf("missing recovery case %q", name))
				continue
			}
			now, err := recoverySamples(current[name], variant, spec.MinSamples)
			if err != nil {
				failures = append(failures, fmt.Sprintf("case %q: %v", name, err))
				continue
			}
			before, err := recoverySamples(previous[name], variant, spec.MinSamples)
			if err != nil {
				failures = append(failures, err.Error())
				continue
			}
			old := map[string]testResilienceRecoveryEvidence{}
			for _, s := range before {
				old[testRecoverySummaryKey(s.Step, s.Phase, s.Request)] = s
			}
			for _, s := range now {
				b := old[testRecoverySummaryKey(s.Step, s.Phase, s.Request)]
				limit := b.P95MS * (1 + spec.P95IncreasePercent/100)
				check := testRecoveryRegressionCheck{Case: name, Step: s.Step, Phase: s.Phase, Request: s.Request, BaselineSamples: b.Samples, CurrentSamples: s.Samples, BaselineP95MS: b.P95MS, CurrentP95MS: s.P95MS, IncreasePercent: 100 * (s.P95MS/b.P95MS - 1), LimitMS: limit, Passed: s.P95MS <= limit+1e-12*math.Max(1, limit)}
				evidence.Checks = append(evidence.Checks, check)
				if !check.Passed {
					failures = append(failures, fmt.Sprintf("case %q recovery %s/%s p95 %.3fms exceeds %.3fms (previous %.3fms, change %+.2f%%)", name, s.Step, testRecoveryDisplayPhase(s.Phase, s.Request), s.P95MS, limit, b.P95MS, check.IncreasePercent))
				}
			}
		}
	}
	if len(failures) > 0 {
		evidence.Error = strings.Join(failures, "; ")
	}
	if evidence.Error != "" {
		evidence.Status = "failed"
		receipt.Status = "failed"
		receipt.Error = "recovery regression failed: " + evidence.Error
	}
}
