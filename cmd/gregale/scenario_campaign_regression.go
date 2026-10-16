package main

import (
	"fmt"
	"reflect"
	"strings"
)

type testCampaignCoverageRegressionSpec struct {
	MaxDropPercentagePoints map[int]float64                         `yaml:"max_drop_percentage_points,omitempty" json:"max_drop_percentage_points,omitempty"`
	MinPlannedEvents        int                                     `yaml:"min_planned_events,omitempty" json:"min_planned_events,omitempty"`
	FromReport              string                                  `yaml:"from_report" json:"from_report"`
	RunID                   string                                  `yaml:"run_id" json:"run_id"`
	MinExercisedRate        float64                                 `yaml:"min_exercised_rate" json:"min_exercised_rate"`
	Baseline                *testCampaignCoverageRegressionBaseline `yaml:"-" json:"baseline,omitempty"`
	reportPath              string
}

type testCampaignCoverageRegressionBaseline struct {
	ReportSHA256 string   `json:"report_sha256"`
	RunID        string   `json:"run_id"`
	Scenario     string   `json:"scenario"`
	Profile      string   `json:"profile"`
	Planned      []int    `json:"planned"`
	Exercised    []int    `json:"exercised"`
	Seeds        []uint64 `json:"seeds"`
	RunsPerSeed  int      `json:"runs_per_seed"`
}

type testCampaignCoverageRegressionEvidence struct {
	MinPlannedEvents int                                    `json:"min_planned_events,omitempty"`
	Status           string                                 `json:"status"`
	ReportSHA256     string                                 `json:"report_sha256"`
	RunID            string                                 `json:"run_id"`
	MinExercisedRate float64                                `json:"min_exercised_rate"`
	Choices          []testCampaignCoverageRegressionChoice `json:"choices"`
	Error            string                                 `json:"error,omitempty"`
}

type testCampaignCoverageRegressionChoice struct {
	DropPercentagePoints    *float64 `json:"drop_percentage_points,omitempty"`
	MaxDropPercentagePoints *float64 `json:"max_drop_percentage_points,omitempty"`
	DropStatus              string   `json:"drop_status,omitempty"`
	Index                   int      `json:"index"`
	Planned                 int      `json:"planned"`
	BaselineExercised       int      `json:"baseline_exercised"`
	CurrentExercised        int      `json:"current_exercised"`
	BaselineRate            float64  `json:"baseline_rate"`
	CurrentRate             float64  `json:"current_rate"`
	LostCoverage            bool     `json:"lost_coverage"`
	Status                  string   `json:"status"`
	Error                   string   `json:"error,omitempty"`
}

func hasTestCampaignAggregatePolicy(spec *testCampaignCoverageSpec) bool {
	return len(spec.MinExercised) > 0 || spec.Regression != nil || spec.RecoveryRegression != nil
}

func expandTestCampaignCoverageRegression(s testScenario) (testScenario, error) {
	spec := s.Resilience.CampaignCoverage
	if spec.Regression == nil {
		return s, nil
	}
	reg := *spec.Regression
	if reg.FromReport == "" || reg.RunID == "" || !finiteNonnegative(reg.MinExercisedRate) || reg.MinExercisedRate > 1 {
		return s, fmt.Errorf("campaign coverage regression requires from_report, run_id and finite min_exercised_rate from 0 to 1")
	}
	batch, err := readTestCampaignGuidanceBatch(s, testCampaignGuidanceInput{FromReport: reg.FromReport, RunID: reg.RunID, reportPath: reg.reportPath}, nil)
	if err != nil {
		return s, fmt.Errorf("campaign coverage regression baseline: %w", err)
	}
	if batch.runsPerSeed != s.Resilience.Runs || !reflect.DeepEqual(batch.ExcludedSeeds, spec.Seeds) {
		return s, fmt.Errorf("campaign coverage regression baseline requires identical ordered seeds and runs per seed")
	}
	if len(reg.MaxDropPercentagePoints) == 0 && reg.MinPlannedEvents != 0 {
		return s, fmt.Errorf("min_planned_events requires max_drop_percentage_points")
	}
	if len(reg.MaxDropPercentagePoints) > 0 {
		if reg.MinPlannedEvents < 2 || reg.MinPlannedEvents > 180 {
			return s, fmt.Errorf("coverage drop comparisons require min_planned_events from 2 to 180")
		}
		for index, allowance := range reg.MaxDropPercentagePoints {
			if index < 0 || index >= len(batch.planned) || !finiteNonnegative(allowance) || allowance > 100 {
				return s, fmt.Errorf("max_drop_percentage_points must map valid choice indices to finite allowances from 0 to 100")
			}
			if batch.planned[index] < reg.MinPlannedEvents {
				return s, fmt.Errorf("choice %d has %d planned events; drop comparison requires at least %d", index, batch.planned[index], reg.MinPlannedEvents)
			}
		}
	}
	reg.Baseline = &testCampaignCoverageRegressionBaseline{ReportSHA256: batch.ReportSHA256, RunID: batch.RunID, Scenario: batch.Scenario, Profile: batch.Profile, Planned: append([]int(nil), batch.planned...), Exercised: append([]int(nil), batch.Observed...), Seeds: append([]uint64(nil), batch.ExcludedSeeds...), RunsPerSeed: batch.runsPerSeed}
	r := *s.Resilience
	c := *spec
	c.Regression = &reg
	r.CampaignCoverage = &c
	s.Resilience = &r
	return s, nil
}

func validateTestCampaignCoverageRegressionOptions(s testScenario, name string, profiles []string, outputs []string) error {
	if s.Resilience == nil || s.Resilience.CampaignCoverage == nil || s.Resilience.CampaignCoverage.Regression == nil {
		return nil
	}
	reg := s.Resilience.CampaignCoverage.Regression
	b := reg.Baseline
	if b == nil || b.Scenario != name {
		return fmt.Errorf("campaign coverage regression baseline scenario mismatch or missing validated baseline")
	}
	for _, profile := range profiles {
		if profile != b.Profile {
			return fmt.Errorf("campaign coverage regression requires baseline profile %s", b.Profile)
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
		return fmt.Errorf("campaign coverage regression baseline changed after validation")
	}
	return nil
}

func evaluateTestCampaignCoverageRegression(s testScenario, e *testCampaignCoverageEvidence) error {
	reg := s.Resilience.CampaignCoverage.Regression
	if reg == nil {
		return nil
	}
	b := reg.Baseline
	r := &testCampaignCoverageRegressionEvidence{Status: "passed", RunID: reg.RunID, MinExercisedRate: reg.MinExercisedRate, MinPlannedEvents: reg.MinPlannedEvents}
	if b != nil {
		r.ReportSHA256 = b.ReportSHA256
	}
	e.Regression = r
	if b == nil || len(e.Choices) != len(b.Planned) || len(b.Exercised) != len(b.Planned) {
		r.Status = "failed"
		r.Error = "campaign coverage regression has incompatible choice evidence"
		return fmt.Errorf("%s", r.Error)
	}
	var failures []string
	for i, c := range e.Choices {
		check := testCampaignCoverageRegressionChoice{Index: i, Planned: c.Planned, BaselineExercised: b.Exercised[i], CurrentExercised: c.Exercised, Status: "passed"}
		if c.Index != i || c.Planned != b.Planned[i] || c.Exercised < 0 || c.Exercised > c.Planned {
			check.Status = "failed"
			check.Error = "incompatible planned or exercised counts"
		} else if c.Planned == 0 {
			check.Status = "not_planned"
		} else {
			check.BaselineRate = float64(b.Exercised[i]) / float64(c.Planned)
			check.CurrentRate = float64(c.Exercised) / float64(c.Planned)
			check.LostCoverage = b.Exercised[i] > 0 && c.Exercised == 0
			if check.LostCoverage {
				check.Status = "failed"
				check.Error = "previously exercised choice has no traffic matches"
			}
			if check.CurrentRate < reg.MinExercisedRate {
				check.Status = "failed"
				if check.Error != "" {
					check.Error += "; "
				}
				check.Error += fmt.Sprintf("exercised-event rate %.6f is below minimum %.6f", check.CurrentRate, reg.MinExercisedRate)
			}
		}
		if allowance, configured := reg.MaxDropPercentagePoints[i]; configured {
			check.MaxDropPercentagePoints = &allowance
			check.DropStatus = "passed"
			if c.Planned < reg.MinPlannedEvents || c.Planned < 1 || c.Planned != b.Planned[i] || c.Index != i || c.Exercised < 0 || c.Exercised > c.Planned {
				check.DropStatus = "failed"
				if check.Error != "" {
					check.Error += "; "
				}
				check.Error += "insufficient or incompatible planned events for coverage drop comparison"
			} else {
				drop := float64(b.Exercised[i]-c.Exercised) * 100 / float64(c.Planned)
				check.DropPercentagePoints = &drop
				if drop > allowance {
					check.DropStatus = "failed"
					if check.Error != "" {
						check.Error += "; "
					}
					check.Error += fmt.Sprintf("coverage dropped %.6f percentage points; maximum %.6f", drop, allowance)
				}
			}
			if check.DropStatus == "failed" {
				check.Status = "failed"
			}
		}
		if check.Status == "failed" {
			r.Status = "failed"
			failures = append(failures, fmt.Sprintf("choice %d: %s", i, check.Error))
		}
		r.Choices = append(r.Choices, check)
	}
	if len(failures) > 0 {
		r.Error = "campaign coverage regression failed: " + strings.Join(failures, "; ")
		return fmt.Errorf("%s", r.Error)
	}
	return nil
}

func testCampaignRegressionSpecForScenario(s testScenario) *testCampaignCoverageRegressionSpec {
	if s.Resilience != nil && s.Resilience.CampaignCoverage != nil {
		return s.Resilience.CampaignCoverage.Regression
	}
	return nil
}
