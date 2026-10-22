package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
)

type testCampaignSeedSearchSpec struct {
	Reports    []testCampaignGuidanceInput `yaml:"reports,omitempty" json:"reports,omitempty"`
	FromReport string                      `yaml:"from_report,omitempty" json:"from_report,omitempty"`
	RunID      string                      `yaml:"run_id,omitempty" json:"run_id,omitempty"`
	reportPath string

	StartSeed     uint64 `yaml:"start_seed" json:"start_seed"`
	MaxCandidates int    `yaml:"max_candidates" json:"max_candidates"`
	MaxSeeds      int    `yaml:"max_seeds" json:"max_seeds"`
}

type testCampaignSeedPreview struct {
	Guidance *testCampaignCoverageGuidance `json:"guidance,omitempty"`

	Seeds      []uint64 `json:"seeds"`
	Candidates int      `json:"candidates"`
	Planned    []int    `json:"planned"`
}

type testCampaignCoverageSpec struct {
	RecoveryRegression *testCampaignRecoveryRegressionSpec `yaml:"recovery_regression,omitempty" json:"recovery_regression,omitempty"`
	Regression         *testCampaignCoverageRegressionSpec `yaml:"regression,omitempty" json:"regression,omitempty"`
	SeedSearch         *testCampaignSeedSearchSpec         `yaml:"seed_search,omitempty" json:"seed_search,omitempty"`
	Preview            *testCampaignSeedPreview            `yaml:"-" json:"seed_selection,omitempty"`
	MinExercised       map[int]int                         `yaml:"min_exercised,omitempty" json:"min_exercised,omitempty"`
	Step               string                              `yaml:"step" json:"step"`
	Seeds              []uint64                            `yaml:"seeds" json:"seeds"`
	MaxRuns            int                                 `yaml:"max_runs" json:"max_runs"`
	MaxWorkloadMinutes int                                 `yaml:"max_workload_minutes" json:"max_workload_minutes"`
}

type testCampaignSeedSetting struct {
	MinExercised map[int]int `json:"min_exercised,omitempty"`
	Step         string      `json:"step"`
	Seed         uint64      `json:"seed"`
}

type testCampaignCoverageEvidence struct {
	RecoveryRegression *testCampaignRecoveryRegressionEvidence `json:"recovery_regression,omitempty"`
	Regression         *testCampaignCoverageRegressionEvidence `json:"regression,omitempty"`
	SeedSelection      *testCampaignSeedPreview                `json:"seed_selection,omitempty"`
	RequirementsStatus string                                  `json:"requirements_status,omitempty"`
	RequirementsError  string                                  `json:"requirements_error,omitempty"`
	MaxRuns            int                                     `json:"max_runs"`
	MaxWorkloadMinutes int                                     `json:"max_workload_minutes"`
	RunsPerSeed        int                                     `json:"runs_per_seed"`
	Step               string                                  `json:"step"`
	Choices            []testCampaignChoiceCoverage            `json:"choices"`
	Seeds              []testCampaignSeedEvidence              `json:"seeds"`
}

type testCampaignChoiceCoverage struct {
	MinExercised      int           `json:"min_exercised,omitempty"`
	RequirementStatus string        `json:"requirement_status,omitempty"`
	Index             int           `json:"index"` // zero-based source choice index, including duplicates
	Duration          string        `json:"duration"`
	Rule              testChaosRule `json:"rule"`
	Planned           int           `json:"planned"`
	Installed         int           `json:"installed"`
	Exercised         int           `json:"exercised"`
}

type testCampaignSeedEvidence struct {
	Seed    uint64                    `json:"seed"`
	Case    string                    `json:"case"`
	Status  string                    `json:"status"`
	Passed  int                       `json:"passed"`
	Failed  int                       `json:"failed"`
	Skipped int                       `json:"skipped"`
	Runs    []testCampaignCoverageRun `json:"runs,omitempty"`
}

type testCampaignCoverageRun struct {
	RunID            string                        `json:"run_id,omitempty"`
	Status           string                        `json:"status"`
	Error            string                        `json:"error,omitempty"`
	FirstFailedCycle int                           `json:"first_failed_cycle,omitempty"`
	RecoveryFailures []testCampaignRecoveryFailure `json:"recovery_failures,omitempty"`
}

type testCampaignRecoveryFailure struct {
	Phase   string `json:"phase"`
	Request string `json:"request,omitempty"`
	Error   string `json:"error,omitempty"`
}

func expandTestCampaignCoverage(scenario testScenario) (testScenario, error) {
	if scenario.Resilience == nil || scenario.Resilience.CampaignCoverage == nil || scenario.Resilience.coverageExpanded {
		return scenario, nil
	}
	spec := scenario.Resilience.CampaignCoverage
	fail := func(message string) (testScenario, error) {
		return scenario, fmt.Errorf("resilience.campaign_coverage: %s", message)
	}
	if scenario.Resilience.FaultSweep != nil || len(scenario.Resilience.FaultMatrix) > 0 {
		return fail("cannot combine campaign_coverage with fault_sweep or fault_matrix")
	}
	if spec.MaxWorkloadMinutes < 1 {
		return fail("max_workload_minutes must be positive")
	}
	if scenario.Timeout != "" {
		if d, err := time.ParseDuration(scenario.Timeout); err != nil || d <= 0 {
			return fail("timeout must be positive")
		}
	}
	var load *testLoadSpec
	for _, step := range scenario.Steps {
		if step.Name == spec.Step {
			load = step.Load
		}
	}
	if load == nil || load.FaultCampaign == nil || !load.campaignExpanded {
		return fail("step must name a staged load with fault_campaign")
	}
	if spec.SeedSearch != nil {
		if len(spec.Seeds) != 0 {
			return fail("seed_search and explicit seeds are mutually exclusive")
		}
		selected, err := selectTestCampaignSeeds(scenario, load.FaultCampaign)
		if err != nil {
			return fail(err.Error())
		}
		copySpec := *spec
		copySpec.Seeds, copySpec.Preview = selected.Seeds, selected
		if selected.Guidance != nil {
			copySpec.MinExercised = cloneTestCampaignRequirements(selected.Guidance.Remaining)
		}
		spec = &copySpec
	}
	if len(spec.Seeds) < 2 || len(spec.Seeds) > testFaultMatrixMaxCases {
		return fail("seeds must contain 2..10 distinct explicit seeds")
	}
	if scenario.Resilience.Runs < 1 || scenario.Resilience.Runs > testResilienceMaxRuns || spec.MaxRuns < 2 || spec.MaxRuns > testResilienceMaxRuns || len(spec.Seeds)*scenario.Resilience.Runs > spec.MaxRuns {
		return fail("max_runs must be 2..20 and cover all seeds and repeated runs")
	}
	for index, minimum := range spec.MinExercised {
		if index < 0 || index >= len(load.FaultCampaign.Choices) || minimum < 1 || minimum > load.FaultCampaign.Count*len(spec.Seeds)*scenario.Resilience.Runs {
			return fail("min_exercised must map valid zero-based choice indices to positive minima no greater than the batch event count")
		}
	}
	seen := map[uint64]bool{}
	cases := make([]testFaultMatrixCaseSpec, 0, len(spec.Seeds))
	for _, seed := range spec.Seeds {
		if seen[seed] {
			return fail("seeds must be distinct")
		}
		seen[seed] = true
		cases = append(cases, testFaultMatrixCaseSpec{Name: fmt.Sprintf("campaign-seed-%d", seed), Step: spec.Step, Campaign: &testCampaignSeedSetting{Step: spec.Step, Seed: seed, MinExercised: spec.MinExercised}})
	}
	result := scenario
	resilience := *scenario.Resilience
	resilience.CampaignCoverage = spec
	resilience.FaultMatrix, resilience.coverageExpanded = cases, true
	result.Resilience = &resilience
	for _, c := range cases {
		variant, err := applyTestFaultMatrixCase(result, c)
		if err != nil {
			return fail(err.Error())
		}
		if err := validateTestScenarioSteps(variant, nil); err != nil {
			return fail(fmt.Sprintf("seed %d: %s", c.Campaign.Seed, err))
		}
	}
	if err := validateTestCampaignCoverageBatch(result, 1); err != nil {
		return fail(err.Error())
	}
	expandedResult, err := expandTestCampaignCoverageRegression(result)
	if err != nil {
		return result, err
	}
	return expandTestCampaignRecoveryRegression(expandedResult)
}

func validateTestCampaignCoverageBatch(scenario testScenario, executions int) error {
	if scenario.Resilience == nil || scenario.Resilience.CampaignCoverage == nil {
		return nil
	}
	spec := scenario.Resilience.CampaignCoverage
	if executions < 1 || testResilienceRuns(scenario)*executions > spec.MaxRuns {
		return fmt.Errorf("campaign coverage exceeds max_runs across selected profiles and attempts")
	}
	if estimateTestWorkloadMinutes(scenario, executions) > spec.MaxWorkloadMinutes {
		return fmt.Errorf("campaign coverage estimated workload-minutes exceed max_workload_minutes across selected profiles and attempts")
	}
	return nil
}

// Reseed the already normalized template. Recovery budgets, baseline, names,
// and cycle identities stay fixed; every fault window is redrawn exactly once.
func applyTestCampaignSeedCase(scenario testScenario, c testFaultMatrixCaseSpec) (testScenario, error) {
	if c.Campaign.Step != c.Step || c.Name != fmt.Sprintf("campaign-seed-%d", c.Campaign.Seed) {
		return scenario, fmt.Errorf("inconsistent campaign seed case")
	}
	for i, step := range scenario.Steps {
		if step.Name != c.Step || step.Load == nil || step.Load.FaultCampaign == nil {
			continue
		}
		load := *step.Load
		if !load.campaignExpanded || len(load.ChaosSchedule) != 1+2*load.FaultCampaign.Count {
			return scenario, fmt.Errorf("campaign template is not expanded")
		}
		load.ChaosSchedule = append([]testLoadChaosPhaseSpec(nil), load.ChaosSchedule...)
		campaign := *load.FaultCampaign
		seed := c.Campaign.Seed
		campaign.Seed = &seed
		campaign.CoverageRequirements = cloneTestCampaignRequirements(c.Campaign.MinExercised)
		load.FaultCampaign = &campaign
		state := seed
		for cycle := 1; cycle <= campaign.Count; cycle++ {
			choice := campaign.Choices[testFaultCampaignChoiceIndex(&campaign, &state, cycle-1)]
			rule := choice.Rule
			rule.Seed = testFaultCampaignDraw(&state)
			load.ChaosSchedule[2*cycle-1].Duration = choice.Duration
			load.ChaosSchedule[2*cycle-1].Rules = []testChaosRule{rule}
		}
		var total time.Duration
		for _, phase := range load.ChaosSchedule {
			d, err := time.ParseDuration(phase.Duration)
			if err != nil {
				return scenario, err
			}
			total += d
		}
		load.Duration = total.String()
		if err := validateTestLoadSpec(&load); err != nil {
			return scenario, err
		}
		scenario.Steps[i].Load = &load
		return scenario, nil
	}
	return scenario, fmt.Errorf("campaign load step %q is absent", c.Step)
}

func summarizeTestCampaignCoverage(scenario testScenario, matrix *testFaultMatrixEvidence) *testCampaignCoverageEvidence {
	spec := scenario.Resilience.CampaignCoverage
	e := &testCampaignCoverageEvidence{SeedSelection: spec.Preview, Step: spec.Step, MaxRuns: spec.MaxRuns, MaxWorkloadMinutes: spec.MaxWorkloadMinutes, RunsPerSeed: scenario.Resilience.Runs}
	var campaign *testFaultCampaignSpec
	for _, step := range scenario.Steps {
		if step.Name == spec.Step {
			campaign = step.Load.FaultCampaign
		}
	}
	for i, choice := range campaign.Choices {
		e.Choices = append(e.Choices, testCampaignChoiceCoverage{Index: i, Duration: choice.Duration, Rule: choice.Rule, MinExercised: spec.MinExercised[i]})
	}
	for i, c := range scenario.Resilience.FaultMatrix {
		group := matrix.Cases[i]
		row := testCampaignSeedEvidence{Seed: c.Campaign.Seed, Case: c.Name, Status: group.Status}
		var history []testRunReceipt
		if group.Resilience != nil {
			history = group.Resilience.History
			row.Passed, row.Failed, row.Skipped = group.Resilience.Passed, group.Resilience.Failed, group.Resilience.Skipped
		} else if group.Status == "skipped" {
			row.Skipped = scenario.Resilience.Runs
		} else {
			row.Failed = scenario.Resilience.Runs
		}
		variant, _ := applyTestFaultMatrixCase(scenario, c) // every case was validated before execution
		var planned *testLoadSpec
		for _, step := range variant.Steps {
			if step.Name == spec.Step {
				planned = step.Load
			}
		}
		state := c.Campaign.Seed
		for cycle := 1; cycle <= campaign.Count; cycle++ {
			index := testFaultCampaignChoiceIndex(campaign, &state, cycle-1)
			_ = testFaultCampaignDraw(&state)
			choice := &e.Choices[index]
			choice.Planned += scenario.Resilience.Runs
			phase := planned.ChaosSchedule[2*cycle-1]
			plan, _ := (&testChaosSpec{Duration: phase.Duration, Rules: phase.Rules}).plan()
			for _, run := range history {
				if run.Status == "skipped" {
					continue
				}
				observed := uniqueTestCampaignPhase(run, spec.Step, phase.Name)
				if observed == nil || observed.Cycle != phase.Cycle || observed.StartedAt.IsZero() || !observed.FinishedAt.After(observed.StartedAt) || observed.Chaos == nil {
					continue
				}
				x := observed.Chaos
				if x.RulesInstalled != 1 || len(x.Rules) != 1 || chaos.RuleID(x.Rules[0]) != chaos.RuleID(plan.Rules[0]) {
					continue
				}
				choice.Installed++
				if len(x.Matches) == 1 && x.Matches[0].Matches > 0 && chaos.RuleID(x.Matches[0].Rule) == chaos.RuleID(plan.Rules[0]) {
					choice.Exercised++
				}
			}
		}
		for _, run := range history {
			r := testCampaignCoverageRun{RunID: run.RunID, Status: run.Status, Error: run.Error}
			for _, step := range run.Steps {
				if step.Name != spec.Step || step.Load == nil {
					continue
				}
				if step.Load.OutageCycles != nil {
					r.FirstFailedCycle = step.Load.OutageCycles.FirstFailedCycle
				}
				for _, phase := range step.Load.ChaosPhases {
					for _, name := range testEndpointRecoveryNames(testPhaseRecoveryEvidence(phase)) {
						recovery := testPhaseRecoveryEvidence(phase)[name]
						if recovery != nil && !recovery.Passed {
							r.RecoveryFailures = append(r.RecoveryFailures, testCampaignRecoveryFailure{Phase: phase.Name, Request: name, Error: recovery.Error})
						}
					}
				}
			}
			row.Runs = append(row.Runs, r)
		}
		e.Seeds = append(e.Seeds, row)
	}
	return e
}

func printTestCampaignCoverage(out io.Writer, e *testCampaignCoverageEvidence) {
	if r := e.RecoveryRegression; r != nil {
		_, _ = fmt.Fprintf(out, "  recovery regression: %s; baseline %s; report SHA256 %s; %s\n", r.Status, r.RunID, r.ReportSHA256, r.Error)
		for _, c := range r.Checks {
			_, _ = fmt.Fprintf(out, "      limits: recovery %s%%, p95 %s%%, error rate %s; minimum %d samples\n", recoveryTrendNumber(c.Limits.MaxRecoveryIncreasePercent), recoveryTrendNumber(c.Limits.MaxP95IncreasePercent), recoveryTrendNumber(c.Limits.MaxErrorRateIncrease), c.Limits.MinSamples)
			_, _ = fmt.Fprintf(out, "    choice %d / %s: baseline recovery %v ms, p95 %v ms, rate %v; current recovery %v ms, p95 %v ms, rate %v; %s %s\n", c.Choice, c.Current.Target, recoveryTrendNumber(c.Baseline.RecoveryMS), recoveryTrendNumber(c.Baseline.P95MS), recoveryTrendNumber(c.Baseline.ErrorRate), recoveryTrendNumber(c.Current.RecoveryMS), recoveryTrendNumber(c.Current.P95MS), recoveryTrendNumber(c.Current.ErrorRate), c.Current.Status, c.Current.Error)
		}
	}

	if r := e.Regression; r != nil {
		_, _ = fmt.Fprintf(out, "  coverage regression: %s; baseline %s; report SHA256 %s; %s\n", r.Status, r.RunID, r.ReportSHA256, r.Error)
		for _, c := range r.Choices {
			_, _ = fmt.Fprintf(out, "    choice %d: baseline %d/%d, current %d/%d (%.2f%%), minimum %.2f%%; lost coverage %t; %s\n", c.Index, c.BaselineExercised, c.Planned, c.CurrentExercised, c.Planned, 100*c.CurrentRate, 100*r.MinExercisedRate, c.LostCoverage, c.Status)
			if c.MaxDropPercentagePoints != nil {
				if c.DropPercentagePoints != nil {
					_, _ = fmt.Fprintf(out, "      drop %.6f percentage points; allowed %.6f; minimum %d planned events (%s)\n", *c.DropPercentagePoints, *c.MaxDropPercentagePoints, r.MinPlannedEvents, c.DropStatus)
				} else {
					_, _ = fmt.Fprintf(out, "      drop unavailable; allowed %.6f; minimum %d planned events (%s)\n", *c.MaxDropPercentagePoints, r.MinPlannedEvents, c.DropStatus)
				}
			}
		}
	}
	if e.SeedSelection != nil {
		if g := e.SeedSelection.Guidance; g != nil {
			_, _ = fmt.Fprintln(out, "  "+printTestCampaignGuidance(g))
			for _, batch := range g.Contributions {
				_, _ = fmt.Fprintf(out, "    batch %s: observed %v; seeds %v; report SHA256 %s\n", batch.RunID, batch.Observed, batch.Seeds, batch.ReportSHA256)
			}
		}
		_, _ = fmt.Fprintf(out, "  selected seeds %v after %d candidates; planned events %v\n", e.SeedSelection.Seeds, e.SeedSelection.Candidates, e.SeedSelection.Planned)
	}
	_, _ = fmt.Fprintf(out, "  campaign coverage (%s): %d seeds\n", e.Step, len(e.Seeds))
	for _, choice := range e.Choices {
		_, _ = fmt.Fprintf(out, "    choice %d (%s, %s): %d planned, %d installed, %d exercised\n", choice.Index, choice.Rule.Kind, choice.Duration, choice.Planned, choice.Installed, choice.Exercised)
	}
	if e.RequirementsStatus != "" {
		_, _ = fmt.Fprintf(out, "  coverage requirements: %s %s\n", e.RequirementsStatus, e.RequirementsError)
		for _, choice := range e.Choices {
			if choice.MinExercised > 0 {
				_, _ = fmt.Fprintf(out, "    choice %d: %d exercised, requires %d (%s)\n", choice.Index, choice.Exercised, choice.MinExercised, choice.RequirementStatus)
			}
		}
	}
	for _, seed := range e.Seeds {
		_, _ = fmt.Fprintf(out, "    seed %d: %s; %d passed, %d failed, %d skipped\n", seed.Seed, seed.Status, seed.Passed, seed.Failed, seed.Skipped)
		for _, run := range seed.Runs {
			if run.Status == "passed" {
				continue
			}
			_, _ = fmt.Fprintf(out, "      run %s: %s; first failed cycle %d; %s\n", run.RunID, run.Status, run.FirstFailedCycle, run.Error)
			for _, failure := range run.RecoveryFailures {
				_, _ = fmt.Fprintf(out, "        recovery %s: %s\n", testRecoveryDisplayPhase(failure.Phase, failure.Request), failure.Error)
			}
		}
	}
}

func cloneTestCampaignRequirements(original map[int]int) map[int]int {
	if len(original) == 0 {
		return nil
	}
	result := make(map[int]int, len(original))
	for index, minimum := range original {
		result[index] = minimum
	}
	return result
}

func evaluateTestCampaignCoverageRequirements(e *testCampaignCoverageEvidence) error {
	e.RequirementsStatus, e.RequirementsError = "", ""
	var failures []string
	for i := range e.Choices {
		choice := &e.Choices[i]
		choice.RequirementStatus = ""
		if choice.MinExercised == 0 {
			continue
		}
		if e.RequirementsStatus == "" {
			e.RequirementsStatus = "passed"
		}
		choice.RequirementStatus = "passed"
		if choice.Exercised < choice.MinExercised {
			choice.RequirementStatus, e.RequirementsStatus = "failed", "failed"
			failures = append(failures, fmt.Sprintf("choice %d exercised %d events; requires at least %d", choice.Index, choice.Exercised, choice.MinExercised))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	e.RequirementsError = "campaign coverage requirements failed: " + strings.Join(failures, "; ")
	return fmt.Errorf("%s", e.RequirementsError)
}

// An event may count once per isolated run. Ambiguous duplicate records cannot
// satisfy a coverage requirement, even if their rule and timestamps agree.
func uniqueTestCampaignPhase(run testRunReceipt, stepName, phaseName string) *testLoadChaosPhaseEvidence {
	var load *testLoadEvidence
	found := false
	for _, step := range run.Steps {
		if step.Name != stepName {
			continue
		}
		if found {
			return nil
		}
		found, load = true, step.Load
	}
	if load == nil {
		return nil
	}
	var result *testLoadChaosPhaseEvidence
	for i := range load.ChaosPhases {
		if load.ChaosPhases[i].Name != phaseName {
			continue
		}
		if result != nil {
			return nil
		}
		result = &load.ChaosPhases[i]
	}
	return result
}

// A bounded greedy search: keep the first valid seed that reduces a deficit.
// This is reproducible, but does not promise a globally minimal seed set.
func selectTestCampaignSeeds(scenario testScenario, campaign *testFaultCampaignSpec) (*testCampaignSeedPreview, error) {
	original := scenario.Resilience.CampaignCoverage
	specCopy := *original
	spec := &specCopy
	search := spec.SeedSearch
	runs := scenario.Resilience.Runs
	if search.MaxCandidates < 2 || search.MaxCandidates > 10000 || search.MaxSeeds < 2 || search.MaxSeeds > 10 || runs < 1 || runs > 20 || spec.MaxRuns < 2 || spec.MaxRuns > 20 || search.MaxSeeds*runs > spec.MaxRuns || uint64(search.MaxCandidates-1) > ^uint64(0)-search.StartSeed {
		return nil, fmt.Errorf("seed_search requires 2..10000 candidates without seed overflow, 2..10 max_seeds, and max_runs covering max_seeds times runs")
	}
	if len(spec.MinExercised) == 0 {
		return nil, fmt.Errorf("seed_search requires min_exercised targets")
	}
	for index, minimum := range spec.MinExercised {
		if index < 0 || index >= len(campaign.Choices) || minimum < 1 || (search.FromReport == "" && search.Reports == nil && minimum > campaign.Count*search.MaxSeeds*runs) {
			return nil, fmt.Errorf("invalid seed_search min_exercised target")
		}
	}
	var guidance *testCampaignCoverageGuidance
	var err error
	if search.FromReport != "" || search.RunID != "" || search.Reports != nil {
		guidance, err = readTestCampaignGuidance(scenario, nil)
		if err != nil {
			return nil, err
		}
		spec.MinExercised = guidance.Remaining
		for _, minimum := range spec.MinExercised {
			if minimum > campaign.Count*search.MaxSeeds*runs {
				return nil, fmt.Errorf("remaining coverage exceeds seed_search event limit")
			}
		}
	}
	excluded := map[uint64]bool{}
	if guidance != nil {
		for _, seed := range guidance.ExcludedSeeds {
			excluded[seed] = true
		}
	}
	result := &testCampaignSeedPreview{Guidance: guidance, Planned: make([]int, len(campaign.Choices))}
	met := func() bool {
		for index, minimum := range spec.MinExercised {
			if result.Planned[index] < minimum {
				return false
			}
		}
		return true
	}
	for offset := 0; offset < search.MaxCandidates && len(result.Seeds) < search.MaxSeeds; offset++ {
		seed := search.StartSeed + uint64(offset)
		result.Candidates++
		if excluded[seed] {
			continue
		}
		counts := make([]int, len(campaign.Choices))
		state := seed
		for cycle := 0; cycle < campaign.Count; cycle++ {
			counts[testFaultCampaignChoiceIndex(campaign, &state, cycle)] += runs
			_ = testFaultCampaignDraw(&state)
		}
		useful := len(result.Seeds) < 2
		for index, minimum := range spec.MinExercised {
			if result.Planned[index] < minimum && counts[index] > 0 {
				useful = true
			}
		}
		if !useful {
			continue
		}
		c := testFaultMatrixCaseSpec{Name: fmt.Sprintf("campaign-seed-%d", seed), Step: spec.Step, Campaign: &testCampaignSeedSetting{Step: spec.Step, Seed: seed, MinExercised: spec.MinExercised}}
		variant, err := applyTestFaultMatrixCase(scenario, c)
		if err != nil {
			continue
		}
		if err := validateTestScenarioSteps(variant, nil); err != nil {
			continue
		}
		result.Seeds = append(result.Seeds, seed)
		for index, count := range counts {
			result.Planned[index] += count
		}
		if len(result.Seeds) >= 2 && met() {
			return result, nil
		}
	}
	return nil, fmt.Errorf("seed_search could not meet planned coverage within candidate and seed limits")
}
