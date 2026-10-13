package main

import (
	"fmt"
	"reflect"
	"strings"
)

type testCampaignRecoveryRegressionCheckSpec struct {
	Choice  int                     `yaml:"choice" json:"choice"`
	Request string                  `yaml:"request,omitempty" json:"request,omitempty"`
	Limits  testRecoveryTrendConfig `yaml:"limits" json:"limits"`
}
type testCampaignRecoveryRegressionSpec struct {
	FromReport string                                    `yaml:"from_report" json:"from_report"`
	RunID      string                                    `yaml:"run_id" json:"run_id"`
	Checks     []testCampaignRecoveryRegressionCheckSpec `yaml:"checks" json:"checks"`
	Baseline   *testCampaignRecoveryRegressionBaseline   `yaml:"-" json:"baseline,omitempty"`
	reportPath string
}
type testCampaignRecoveryRegressionBaseline struct {
	Coverage testCampaignCoverageRegressionBaseline `json:"coverage"`
	Values   []testRecoveryTrendValue               `json:"values"`
}
type testCampaignRecoveryRegressionCheck struct {
	Choice   int                     `json:"choice"`
	Request  string                  `json:"request,omitempty"`
	Limits   testRecoveryTrendConfig `json:"limits"`
	Baseline testRecoveryTrendValue  `json:"baseline"`
	Current  testRecoveryTrendValue  `json:"current"`
}
type testCampaignRecoveryRegressionEvidence struct {
	Status       string                                `json:"status"`
	RunID        string                                `json:"run_id"`
	ReportSHA256 string                                `json:"report_sha256"`
	Checks       []testCampaignRecoveryRegressionCheck `json:"checks"`
	Error        string                                `json:"error,omitempty"`
}

func campaignRecoveryValue(s testScenario, root *testRunReceipt, check testCampaignRecoveryRegressionCheckSpec) (testRecoveryTrendValue, error) {
	values, err := collectTestRecoveryTrendValues(s, root, check.Limits.MinSamples)
	if err != nil {
		return testRecoveryTrendValue{}, err
	}
	for _, v := range values {
		if v.Choice == check.Choice && v.Request == check.Request {
			return v, nil
		}
	}
	return testRecoveryTrendValue{}, fmt.Errorf("choice %d recovery target %q is not configured", check.Choice, check.Request)
}
func expandTestCampaignRecoveryRegression(s testScenario) (testScenario, error) {
	spec := s.Resilience.CampaignCoverage
	if spec.RecoveryRegression == nil {
		return s, nil
	}
	reg := *spec.RecoveryRegression
	reg.Checks = append([]testCampaignRecoveryRegressionCheckSpec(nil), reg.Checks...)
	for i := range reg.Checks {
		limits := &reg.Checks[i].Limits
		for _, pointer := range []**float64{&limits.MaxRecoveryIncreasePercent, &limits.MaxP95IncreasePercent, &limits.MaxErrorRateIncrease} {
			if *pointer != nil {
				value := **pointer
				*pointer = &value
			}
		}
	}
	if reg.FromReport == "" || reg.RunID == "" || len(reg.Checks) < 1 || len(reg.Checks) > 32 {
		return s, fmt.Errorf("recovery regression requires from_report, run_id and 1 to 32 checks")
	}
	seen := map[string]bool{}
	for _, check := range reg.Checks {
		key := fmt.Sprintf("%d/%s", check.Choice, check.Request)
		if check.Choice < 0 || seen[key] {
			return s, fmt.Errorf("recovery regression choices must be nonnegative and targets unique")
		}
		seen[key] = true
		if err := validateTestRecoveryTrendConfig(check.Limits); err != nil {
			return s, err
		}
		if check.Limits.MaxRecoveryIncreasePercent == nil && check.Limits.MaxP95IncreasePercent == nil && check.Limits.MaxErrorRateIncrease == nil {
			return s, fmt.Errorf("each recovery regression check requires at least one tolerance")
		}
	}
	batch, err := readTestCampaignGuidanceBatch(s, testCampaignGuidanceInput{FromReport: reg.FromReport, RunID: reg.RunID, reportPath: reg.reportPath}, nil)
	if err != nil {
		return s, fmt.Errorf("recovery regression baseline: %w", err)
	}
	if batch.runsPerSeed != s.Resilience.Runs || !reflect.DeepEqual(batch.ExcludedSeeds, spec.Seeds) {
		return s, fmt.Errorf("recovery regression requires identical ordered seeds and repetitions")
	}
	path := reg.reportPath
	if path == "" {
		path = reg.FromReport
	}
	receipts, digest, err := readTestBaselineReport(path, nil)
	if err != nil {
		return s, err
	}
	if digest != batch.ReportSHA256 {
		return s, fmt.Errorf("recovery baseline changed during validation")
	}
	var root *testRunReceipt
	for i := range receipts {
		if receipts[i].RunID == reg.RunID {
			root = &receipts[i]
		}
	}
	if root == nil {
		return s, fmt.Errorf("missing recovery baseline root")
	}
	baseline := &testCampaignRecoveryRegressionBaseline{Coverage: testCampaignCoverageRegressionBaseline{ReportSHA256: digest, RunID: batch.RunID, Scenario: batch.Scenario, Profile: batch.Profile, Planned: batch.planned, Exercised: batch.Observed, Seeds: batch.ExcludedSeeds, RunsPerSeed: batch.runsPerSeed}}
	for _, check := range reg.Checks {
		value, err := campaignRecoveryValue(s, root, check)
		if err != nil {
			return s, err
		}
		if value.Status != "passed" || value.Events < 1 || value.RecoveryMS == nil || value.P95MS == nil || value.ErrorRate == nil {
			return s, fmt.Errorf("recovery baseline choice %d / %q is unavailable: %s", check.Choice, check.Request, value.Error)
		}
		baseline.Values = append(baseline.Values, value)
	}
	reg.Baseline = baseline
	r := *s.Resilience
	c := *spec
	c.RecoveryRegression = &reg
	r.CampaignCoverage = &c
	s.Resilience = &r
	return s, nil
}
func validateTestCampaignRecoveryRegressionOptions(s testScenario, name string, profiles, outputs []string) error {
	if s.Resilience == nil || s.Resilience.CampaignCoverage == nil || s.Resilience.CampaignCoverage.RecoveryRegression == nil {
		return nil
	}
	reg := s.Resilience.CampaignCoverage.RecoveryRegression
	if reg.Baseline == nil || reg.Baseline.Coverage.Scenario != name {
		return fmt.Errorf("recovery regression baseline scenario mismatch or missing baseline")
	}
	b := reg.Baseline.Coverage
	for _, profile := range profiles {
		if profile != b.Profile {
			return fmt.Errorf("recovery regression requires baseline profile %s", b.Profile)
		}
	}
	path := reg.reportPath
	if path == "" {
		path = reg.FromReport
	}
	_, digest, err := readTestBaselineReport(path, outputs)
	if err != nil {
		return err
	}
	if digest != b.ReportSHA256 || reg.RunID != b.RunID {
		return fmt.Errorf("recovery regression baseline changed after validation")
	}
	return nil
}
func evaluateTestCampaignRecoveryRegression(s testScenario, root *testRunReceipt) error {
	reg := s.Resilience.CampaignCoverage.RecoveryRegression
	if reg == nil {
		return nil
	}
	evidence := &testCampaignRecoveryRegressionEvidence{Status: "passed", RunID: reg.RunID}
	root.FaultMatrix.CampaignCoverage.RecoveryRegression = evidence
	if reg.Baseline == nil || len(reg.Baseline.Values) != len(reg.Checks) {
		evidence.Status = "failed"
		evidence.Error = "recovery regression validated baseline unavailable"
		return fmt.Errorf("%s", evidence.Error)
	}
	evidence.ReportSHA256 = reg.Baseline.Coverage.ReportSHA256
	complete := len(root.FaultMatrix.Cases) == len(s.Resilience.CampaignCoverage.Seeds)
	for _, group := range root.FaultMatrix.Cases {
		if group.Resilience == nil || len(group.Resilience.History) != s.Resilience.Runs {
			complete = false
			continue
		}
		for _, leaf := range group.Resilience.History {
			if leaf.CampaignSeed == nil || leaf.Status == "skipped" {
				complete = false
			}
		}
	}
	var failures []string
	for i, check := range reg.Checks {
		current := testRecoveryTrendValue{Choice: check.Choice, Target: reg.Baseline.Values[i].Target, Status: "failed", Error: "complete campaign history unavailable"}
		if complete {
			value, err := campaignRecoveryValue(s, root, check)
			if err != nil {
				current.Error = err.Error()
			} else {
				current = value
				compareTestRecoveryTrendValue(&current, reg.Baseline.Values[i], check.Limits)
			}
		}
		if current.Status != "passed" {
			failures = append(failures, fmt.Sprintf("choice %d / %s: %s", check.Choice, current.Target, current.Error))
		}
		evidence.Checks = append(evidence.Checks, testCampaignRecoveryRegressionCheck{Choice: check.Choice, Request: check.Request, Limits: check.Limits, Baseline: reg.Baseline.Values[i], Current: current})
	}
	if len(failures) > 0 {
		evidence.Status = "failed"
		evidence.Error = "recovery regression failed: " + strings.Join(failures, "; ")
		return fmt.Errorf("%s", evidence.Error)
	}
	return nil
}
func testCampaignRecoveryRegressionSpecForScenario(s testScenario) *testCampaignRecoveryRegressionSpec {
	if s.Resilience == nil || s.Resilience.CampaignCoverage == nil {
		return nil
	}
	return s.Resilience.CampaignCoverage.RecoveryRegression
}
