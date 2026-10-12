package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
)

type testCampaignGuidanceInput struct {
	FromReport string `yaml:"from_report" json:"from_report"`
	RunID      string `yaml:"run_id" json:"run_id"`
	reportPath string
}

type testCampaignGuidanceContribution struct {
	ReportSHA256 string   `json:"report_sha256"`
	RunID        string   `json:"run_id"`
	Observed     []int    `json:"observed"`
	Seeds        []uint64 `json:"seeds"`
}

type testCampaignCoverageGuidance struct {
	Contributions []testCampaignGuidanceContribution `json:"contributions,omitempty"`
	leafRunIDs    []string
	planned       []int
	runsPerSeed   int

	ReportSHA256  string      `json:"report_sha256"`
	RunID         string      `json:"run_id"`
	Scenario      string      `json:"scenario"`
	Profile       string      `json:"profile"`
	Observed      []int       `json:"observed"`
	Required      map[int]int `json:"required"`
	Remaining     map[int]int `json:"remaining"`
	ExcludedSeeds []uint64    `json:"excluded_seeds"`
}

func resolveTestCampaignGuidancePath(s testScenario, dir string) testScenario {
	if s.Resilience == nil || s.Resilience.CampaignCoverage == nil {
		return s
	}
	r := *s.Resilience
	c := *r.CampaignCoverage
	if c.RecoveryRegression != nil {
		reg := *c.RecoveryRegression
		reg.reportPath = reg.FromReport
		if !filepath.IsAbs(reg.reportPath) {
			reg.reportPath = filepath.Join(dir, reg.reportPath)
		}
		c.RecoveryRegression = &reg
	}
	if c.Regression != nil {
		reg := *c.Regression
		reg.reportPath = reg.FromReport
		if !filepath.IsAbs(reg.reportPath) {
			reg.reportPath = filepath.Join(dir, reg.reportPath)
		}
		c.Regression = &reg
	}
	if c.SeedSearch == nil {
		r.CampaignCoverage = &c
		s.Resilience = &r
		return s
	}
	search := *c.SeedSearch
	if search.FromReport != "" {
		search.reportPath = search.FromReport
		if !filepath.IsAbs(search.reportPath) {
			search.reportPath = filepath.Join(dir, search.reportPath)
		}
	}
	if search.Reports != nil {
		search.Reports = append([]testCampaignGuidanceInput{}, search.Reports...)
	}
	for i := range search.Reports {
		input := &search.Reports[i]
		input.reportPath = input.FromReport
		if !filepath.IsAbs(input.reportPath) {
			input.reportPath = filepath.Join(dir, input.reportPath)
		}
	}
	c.SeedSearch = &search
	r.CampaignCoverage = &c
	s.Resilience = &r
	return s
}

// Only raw, uniquely identified, compatible leaf history contributes coverage.
// Summary counts are checked against recomputed evidence before being used.
func readTestCampaignGuidanceBatch(s testScenario, input testCampaignGuidanceInput, outputs []string) (*testCampaignCoverageGuidance, error) {
	spec := s.Resilience.CampaignCoverage
	path := input.reportPath
	if path == "" {
		path = input.FromReport
	}
	receipts, digest, err := readTestBaselineReport(path, outputs)
	if err != nil {
		return nil, err
	}
	var prior *testRunReceipt
	for i := range receipts {
		if receipts[i].RunID == input.RunID {
			if prior != nil {
				return nil, fmt.Errorf("duplicate guidance run_id")
			}
			prior = &receipts[i]
		}
	}
	if prior == nil || prior.Scenario == "" || prior.Engine != "real-vm" || prior.Profile == "" || prior.CleanupError != "" || (prior.Status != "passed" && prior.Status != "failed") || prior.FaultMatrix == nil || prior.FaultMatrix.CampaignCoverage == nil {
		return nil, fmt.Errorf("guidance requires a completed real-vm campaign root with clean cleanup")
	}
	e := prior.FaultMatrix.CampaignCoverage
	if e.Step != spec.Step || e.RunsPerSeed < 1 || e.RunsPerSeed > 20 || len(e.Seeds) < 2 || len(e.Seeds) > 10 || len(prior.FaultMatrix.Cases) != len(e.Seeds) || len(e.Seeds)*e.RunsPerSeed > 20 {
		return nil, fmt.Errorf("guidance campaign scope or run history mismatch")
	}
	var campaign *testFaultCampaignSpec
	for _, step := range s.Steps {
		if step.Name == spec.Step && step.Load != nil {
			campaign = step.Load.FaultCampaign
		}
	}
	if campaign == nil || len(e.Choices) != len(campaign.Choices) {
		return nil, fmt.Errorf("guidance choices do not match manifest")
	}
	baselineSpec := *spec
	baselineSpec.SeedSearch = nil
	baselineSpec.Preview = nil
	baselineSpec.Seeds = nil
	baselineSpec.MinExercised = nil
	for i, choice := range e.Choices {
		if choice.Index != i || choice.Duration != campaign.Choices[i].Duration || !reflect.DeepEqual(choice.Rule, campaign.Choices[i].Rule) || choice.MinExercised < 0 {
			return nil, fmt.Errorf("guidance choice %d mismatch", i)
		}
		if choice.MinExercised > 0 {
			if baselineSpec.MinExercised == nil {
				baselineSpec.MinExercised = map[int]int{}
			}
			baselineSpec.MinExercised[i] = choice.MinExercised
		}
	}
	baseline := s
	r := *s.Resilience
	r.Runs = e.RunsPerSeed
	r.CampaignCoverage = &baselineSpec
	r.FaultMatrix = nil
	r.coverageExpanded = false
	baseline.Resilience = &r
	var leafRunIDs []string
	seenSeeds := map[uint64]bool{}
	seenIDs := map[string]bool{prior.RunID: true}
	for i, row := range e.Seeds {
		name := fmt.Sprintf("campaign-seed-%d", row.Seed)
		if seenSeeds[row.Seed] || row.Case != name {
			return nil, fmt.Errorf("duplicate or inconsistent guidance seed")
		}
		seenSeeds[row.Seed] = true
		baselineSpec.Seeds = append(baselineSpec.Seeds, row.Seed)
		c := testFaultMatrixCaseSpec{Name: name, Step: spec.Step, Campaign: &testCampaignSeedSetting{Step: spec.Step, Seed: row.Seed, MinExercised: baselineSpec.MinExercised}}
		r.FaultMatrix = append(r.FaultMatrix, c)
		group := &prior.FaultMatrix.Cases[i]
		if group.Case != name || group.Scenario != prior.Scenario || group.Engine != prior.Engine || group.Profile != prior.Profile || group.CleanupError != "" || group.Resilience == nil || group.Resilience.Runs != r.Runs || len(group.Resilience.History) != r.Runs {
			return nil, fmt.Errorf("guidance case has incomplete or incompatible history")
		}
		variant, err := applyTestFaultMatrixCase(baseline, c)
		if err != nil {
			return nil, err
		}
		if group.RecoveryWorkloadSHA256 == "" || group.RecoveryWorkloadSHA256 != recoveryWorkloadSHA256(variant) {
			return nil, fmt.Errorf("guidance workload changed")
		}
		attempts := map[int]bool{}
		passed, failed := 0, 0
		for _, leaf := range group.Resilience.History {
			if leaf.Attempt < 1 || leaf.Attempt > r.Runs || attempts[leaf.Attempt] || !reflect.DeepEqual(leaf.CampaignSeed, c.Campaign) || leaf.RunID == "" || seenIDs[leaf.RunID] || leaf.Scenario != prior.Scenario || leaf.Profile != prior.Profile || leaf.Engine != "real-vm" || leaf.CleanupError != "" || (leaf.Status != "passed" && leaf.Status != "failed") {
				return nil, fmt.Errorf("guidance contains skipped, duplicate or incomplete leaves")
			}
			seenIDs[leaf.RunID] = true
			leafRunIDs = append(leafRunIDs, leaf.RunID)
			attempts[leaf.Attempt] = true
			if leaf.Status == "passed" {
				passed++
			} else {
				failed++
			}
			for _, observedStep := range leaf.Steps {
				if observedStep.Name != spec.Step {
					continue
				}
				load := observedStep.Load
				if load == nil || load.OutageCycles == nil || load.OutageCycles.Count != campaign.Count || load.OutageCycles.Completed != campaign.Count || load.StopReason == "canceled" || load.StopReason == "scenario_timeout" || load.StopReason == "request_limit" {
					return nil, fmt.Errorf("guidance leaf did not complete its campaign")
				}
			}
			for _, step := range variant.Steps {
				if step.Name != spec.Step || step.Load == nil {
					continue
				}
				for _, phase := range step.Load.ChaosSchedule {
					observed := uniqueTestCampaignPhase(leaf, spec.Step, phase.Name)
					if observed == nil || observed.Cycle != phase.Cycle || observed.StartedAt.IsZero() || !observed.FinishedAt.After(observed.StartedAt) {
						return nil, fmt.Errorf("guidance leaf has incomplete fault/recovery history")
					}
				}
			}
			expected := newTestReplayManifest(prior.Scenario, prior.Profile, name, variant)
			if expected == nil || (leaf.Replay == nil && leaf.Status != "passed") || (leaf.Replay != nil && !reflect.DeepEqual(expected, leaf.Replay)) {
				return nil, fmt.Errorf("guidance workload or replay configuration changed")
			}
		}
		if group.Resilience.Passed != passed || group.Resilience.Failed != failed || group.Resilience.Skipped != 0 {
			return nil, fmt.Errorf("guidance run counts disagree with leaf history")
		}
	}
	computed := summarizeTestCampaignCoverage(baseline, prior.FaultMatrix)
	if !reflect.DeepEqual(computed.Seeds, e.Seeds) {
		return nil, fmt.Errorf("guidance seed summaries disagree with leaf history")
	}
	for i, choice := range computed.Choices {
		if choice.Planned != e.Choices[i].Planned || choice.Installed != e.Choices[i].Installed || choice.Exercised != e.Choices[i].Exercised {
			return nil, fmt.Errorf("guidance coverage summary disagrees with leaf evidence")
		}
	}
	g := &testCampaignCoverageGuidance{runsPerSeed: e.RunsPerSeed, leafRunIDs: leafRunIDs, ReportSHA256: digest, RunID: prior.RunID, Scenario: prior.Scenario, Profile: prior.Profile, Required: cloneTestCampaignRequirements(spec.MinExercised), Remaining: map[int]int{}, ExcludedSeeds: append([]uint64(nil), baselineSpec.Seeds...)}
	for i, choice := range computed.Choices {
		g.Observed = append(g.Observed, choice.Exercised)
		g.planned = append(g.planned, choice.Planned)
		if remaining := spec.MinExercised[i] - choice.Exercised; remaining > 0 {
			g.Remaining[i] = remaining
		}
	}
	return g, nil
}

func validateTestCampaignGuidanceOptions(s testScenario, name string, profiles []string, outputs []string) error {
	if err := validateTestCampaignRecoveryRegressionOptions(s, name, profiles, outputs); err != nil {
		return err
	}
	if err := validateTestCampaignCoverageRegressionOptions(s, name, profiles, outputs); err != nil {
		return err
	}
	if s.Resilience == nil || s.Resilience.CampaignCoverage == nil || s.Resilience.CampaignCoverage.Preview == nil || s.Resilience.CampaignCoverage.Preview.Guidance == nil {
		return nil
	}
	g := s.Resilience.CampaignCoverage.Preview.Guidance
	if name != g.Scenario {
		return fmt.Errorf("guidance scenario does not match selected scenario")
	}
	for _, profile := range profiles {
		if profile != g.Profile {
			return fmt.Errorf("guidance profile must be %s", g.Profile)
		}
	}
	// Recheck every pinned input and prevent output paths from aliasing any report.
	inputs, err := testCampaignGuidanceInputs(s.Resilience.CampaignCoverage.SeedSearch)
	if err != nil {
		return err
	}
	if len(g.Contributions) > 0 && len(g.Contributions) != len(inputs) {
		return fmt.Errorf("guidance inputs changed after selection")
	}
	for i, input := range inputs {
		expectedSHA, expectedID := g.ReportSHA256, g.RunID
		if len(g.Contributions) > 0 {
			expectedSHA, expectedID = g.Contributions[i].ReportSHA256, g.Contributions[i].RunID
		}
		path := input.reportPath
		if path == "" {
			path = input.FromReport
		}
		_, digest, err := readTestBaselineReport(path, outputs)
		if err != nil {
			return err
		}
		if digest != expectedSHA || input.RunID != expectedID {
			return fmt.Errorf("guidance report changed after selection")
		}
	}

	return nil
}

func printTestCampaignGuidance(g *testCampaignCoverageGuidance) string {
	body, _ := json.Marshal(g.Remaining)
	if len(g.Contributions) > 0 {
		return fmt.Sprintf("guided by %d campaign batches (%s): cumulative exercised %v; remaining targets %s", len(g.Contributions), g.Profile, g.Observed, body)
	}
	return fmt.Sprintf("guided by run %s (%s): prior exercised %v; remaining targets %s", g.RunID, g.Profile, g.Observed, body)
}

func testCampaignGuidanceInputs(search *testCampaignSeedSearchSpec) ([]testCampaignGuidanceInput, error) {
	if search.Reports != nil {
		if search.FromReport != "" || search.RunID != "" {
			return nil, fmt.Errorf("seed_search.reports cannot combine with from_report or run_id")
		}
		if len(search.Reports) < 2 || len(search.Reports) > 10 {
			return nil, fmt.Errorf("seed_search.reports requires 2..10 selected campaign batches")
		}
		for _, input := range search.Reports {
			if input.FromReport == "" || input.RunID == "" {
				return nil, fmt.Errorf("each guidance report requires from_report and run_id")
			}
		}
		return search.Reports, nil
	}
	if search.FromReport == "" || search.RunID == "" {
		return nil, fmt.Errorf("seed_search requires both from_report and run_id")
	}
	return []testCampaignGuidanceInput{{FromReport: search.FromReport, RunID: search.RunID, reportPath: search.reportPath}}, nil
}

// Sum only explicitly selected batches; nested guidance is provenance, not
// additional observed evidence. All run identities must be disjoint.
func readTestCampaignGuidance(s testScenario, outputs []string) (*testCampaignCoverageGuidance, error) {
	inputs, err := testCampaignGuidanceInputs(s.Resilience.CampaignCoverage.SeedSearch)
	if err != nil {
		return nil, err
	}
	seenIDs := map[string]bool{}
	for _, input := range inputs {
		if seenIDs[input.RunID] {
			return nil, fmt.Errorf("duplicate guidance root run_id")
		}
		seenIDs[input.RunID] = true
	}
	var result *testCampaignCoverageGuidance
	seenSeeds := map[uint64]bool{}
	for _, input := range inputs {
		batch, err := readTestCampaignGuidanceBatch(s, input, outputs)
		if err != nil {
			return nil, err
		}
		for _, id := range batch.leafRunIDs {
			if seenIDs[id] {
				return nil, fmt.Errorf("duplicate guidance leaf or root run_id across batches")
			}
			seenIDs[id] = true
		}
		batch.leafRunIDs = nil
		if len(inputs) == 1 {
			result = batch
			break
		}
		if result == nil {
			result = &testCampaignCoverageGuidance{Scenario: batch.Scenario, Profile: batch.Profile, Observed: make([]int, len(batch.Observed)), Required: cloneTestCampaignRequirements(s.Resilience.CampaignCoverage.MinExercised), Remaining: map[int]int{}}
		}
		if batch.Scenario != result.Scenario || batch.Profile != result.Profile || len(batch.Observed) != len(result.Observed) {
			return nil, fmt.Errorf("guidance batches must share scenario, profile and choices")
		}
		for i, count := range batch.Observed {
			result.Observed[i] += count
		}
		for _, seed := range batch.ExcludedSeeds {
			seenSeeds[seed] = true
		}
		result.Contributions = append(result.Contributions, testCampaignGuidanceContribution{ReportSHA256: batch.ReportSHA256, RunID: batch.RunID, Observed: append([]int(nil), batch.Observed...), Seeds: append([]uint64(nil), batch.ExcludedSeeds...)})
	}
	if len(inputs) > 1 {
		for seed := range seenSeeds {
			result.ExcludedSeeds = append(result.ExcludedSeeds, seed)
		}
		sort.Slice(result.ExcludedSeeds, func(i, j int) bool { return result.ExcludedSeeds[i] < result.ExcludedSeeds[j] })
		for i, minimum := range result.Required {
			if remaining := minimum - result.Observed[i]; remaining > 0 {
				result.Remaining[i] = remaining
			}
		}
	}
	if len(result.Remaining) == 0 {
		return nil, fmt.Errorf("guidance coverage targets are already satisfied")
	}
	return result, nil
}
