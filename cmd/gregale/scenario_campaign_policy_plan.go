package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

type testCampaignPolicyPlan struct {
	Execution *testCampaignExecutionBinding `json:"execution,omitempty"`
	Costs *testCampaignPlanCosts `json:"costs,omitempty"`
	Batches []testCampaignCostBatch `json:"batches,omitempty"`
	Preflight *testPlanPreflightSummary `json:"preflight,omitempty"`
	Environments []testEnvironmentPlanEntry `json:"environment_requirements"`
	SchemaVersion int                          `json:"schema_version"`
	Informational bool                         `json:"informational"`
	Notes []string `json:"notes"`
	Entries       []testCampaignPolicyPlanEntry `json:"entries"`
}

type testCampaignPolicyPlanEntry struct {
	Planned *testCampaignPlannedCoverage `json:"planned,omitempty"`
	EnvironmentBlockers []testEnvironmentPlanBlocker `json:"environment_blockers"`
	Requirement       string              `json:"requirement"`
	Configuration     string              `json:"configuration_sha256,omitempty"`
	Context           testFailureGroupKey `json:"context"`
	Status            string              `json:"status"`
	Reason            string              `json:"reason,omitempty"`
	MinCoveredRuns    int                 `json:"min_covered_runs"`
	BaselineCovered   int                 `json:"baseline_covered_runs"`
	CurrentCovered    int                 `json:"current_covered_runs"`
	BaselineAdditional int                `json:"baseline_additional_runs"`
	CurrentAdditional int                 `json:"current_additional_runs"`
	RunsPerRepetition int                 `json:"runs_per_repetition"`
	Suggestions       []testCampaignPlanCommand `json:"suggestions"`
}

type testCampaignPlanCommand struct {
	Side                string   `json:"side"`
	Repetitions         int      `json:"repetitions"`
	RemainingRepetitions int     `json:"remaining_repetitions_after_command"`
	Argv                []string `json:"argv"`
}

type testCampaignPlanCandidate struct {
	Scenario testScenario
	Case     string
	Hash     string
	Runs     int
}

func cmdTestPolicyPlan(args []string) int {
	fs := newFlagSet("test policy plan", flag.ContinueOnError)
	preflightPath := fs.String("preflight", "", "optional platform preflight JSON")
	targetsPath := fs.String("targets", "", "original mapping used for preflight")
	preflightSide := fs.String("preflight-side", "current", "side whose platform prerequisites may be resolved")
	preflightAge := fs.Duration("preflight-max-age", 15*time.Minute, "maximum preflight age (1s..1h)")
	maxRuns := fs.Int64("plan-max-runs", 0, "maximum planned leaf executions across both sides (0 unlimited)")
	maxDeployments := fs.Int64("plan-max-deployments", 0, "maximum planned workload deployments (0 unlimited)")
	maxMinutes := fs.Int64("plan-max-workload-minutes", 0, "maximum estimated workload minutes (0 unlimited)")
	path := fs.String("policy", "", "YAML campaign policy")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "scenario manifest")
	from := fs.String("from", "", "optional current raw JSON report")
	baseline := fs.String("baseline", "", "optional baseline raw JSON report")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *path == "" || (*baseline != "" && *from == "") {
		return printErr("Invalid policy plan options", errors.New("--policy is required; --baseline requires --from"))
	}
	preflightOptions := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "targets" || f.Name == "preflight-side" || f.Name == "preflight-max-age" { preflightOptions = true }
	})
	if (*preflightPath == "" && preflightOptions) || (*preflightPath != "" && *targetsPath == "") {
		return printErr("Invalid plan preflight options", errors.New("--preflight requires --targets; preflight settings require --preflight"))
	}
	policy, err := readTestCampaignPolicy(*path)
	if err != nil {
		return printErr("Invalid policy", err)
	}
	manifest, _, err := readTestManifestDocument(*manifestPath, func(_ string, scenario testScenario) []string {
		return testHTTPDataFields(scenario)
	})
	if err != nil {
		return printErr("Invalid scenario manifest", err)
	}
	var before, after []testRunReceipt
	if *from != "" {
		after, _, err = readTestBaselineReport(*from, nil)
		if err != nil {
			return printErr("Could not read current report", err)
		}
	}
	if *baseline != "" {
		before, _, err = readTestBaselineReport(*baseline, nil)
		if err != nil {
			return printErr("Could not read baseline", err)
		}
	}
	var preflight *testPlannerPreflight
	if *preflightPath != "" {
		preflight, err = readTestPlannerPreflight(*preflightPath, *targetsPath, manifest.Scenarios, *preflightSide, *preflightAge, time.Now().UTC())
		if err != nil { return printErr("Invalid planner preflight", err) }
	}
	plan, err := buildTestCampaignPolicyPlan(policy, manifest.Scenarios, *manifestPath, before, after, preflight)
	if err != nil {
		return printErr("Could not plan coverage", err)
	}
	if err := optimizeTestCampaignPlan(&plan, manifest.Scenarios, *manifestPath, testCampaignPlanBudget{Runs: *maxRuns, Deployments: *maxDeployments, WorkloadMinutes: *maxMinutes}); err != nil {
		return printErr("Could not budget coverage plan", err)
	}
	plan.Execution = &testCampaignExecutionBinding{ManifestSHA256:testCampaignJSONHash(manifest.Scenarios), PolicySHA256:testCampaignJSONHash(policy)}
	if *from != "" {
		_, plan.Execution.CurrentSHA256, err = readTestBaselineReport(*from, nil)
		if err != nil { return printErr("Could not bind current report", err) }
	}
	if *baseline != "" {
		_, plan.Execution.BaselineSHA256, err = readTestBaselineReport(*baseline, nil)
		if err != nil { return printErr("Could not bind baseline report", err) }
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(plan); err != nil {
		return printErr("Could not write policy plan", err)
	}
	return 0
}

func testCampaignPlanCandidates(scenario testScenario) ([]testCampaignPlanCandidate, error) {
	cases := []testCampaignPlanCandidate{}
	if scenario.Resilience != nil && len(scenario.Resilience.FaultMatrix) > 0 {
		for _, spec := range scenario.Resilience.FaultMatrix {
			variant, err := applyTestFaultMatrixCase(scenario, spec)
			if err != nil {
				return nil, err
			}
			runs := max(1, variant.Resilience.Runs)
			variant.Resilience = nil
			cases = append(cases, testCampaignPlanCandidate{Scenario: variant, Case: spec.Name, Runs: runs})
		}
	} else {
		runs := 1
		if scenario.Resilience != nil {
			runs = max(1, scenario.Resilience.Runs)
		}
		scenario.Resilience = nil
		cases = append(cases, testCampaignPlanCandidate{Scenario: scenario, Runs: runs})
	}
	for i := range cases {
		replay := newTestReplayManifest("", "", cases[i].Case, cases[i].Scenario)
		if replay == nil {
			return nil, errors.New("could not fingerprint manifest candidate")
		}
		cases[i].Hash = replay.ConfigurationSHA256
	}
	return cases, nil
}

func buildTestCampaignPolicyPlan(policy testCampaignPolicy, scenarios map[string]testScenario, manifestPath string, before, after []testRunReceipt, preflight ...*testPlannerPreflight) (testCampaignPolicyPlan, error) {
	plan := testCampaignPolicyPlan{SchemaVersion: 1, Informational: true, Notes: []string{
		"Commands are suggestions only; no environments are provisioned.",
		"Matrix commands execute all cases. Suggestions are independent and may duplicate work across requirements.",
		"Run each side against its intended source revision. Combine resulting receipt arrays with the matching side report before reevaluating coverage.",
		"Each command writes a separate report; use fresh output paths. Existing workload limits still apply.",
		"At most 20 commands per side/context are shown; remaining repetitions are reported explicitly.",
	}, Entries: make([]testCampaignPolicyPlanEntry, 0)}
	coverage := evaluateTestCampaignRequiredCoverage(policy, before, after)
	for requirementIndex, requirement := range policy.RequiredCoverage {
		name := testRequiredSelectorValue(requirement.Match.Scenario)
		scenario, exists := scenarios[name]
		base := testCampaignPolicyPlanEntry{Requirement: requirement.Name, MinCoveredRuns: requirement.MinExecutedRuns,
			Context: testFailureGroupingKeyForRequirement(testRunReceipt{Scenario: name}, requirement.Match),
			BaselineAdditional: requirement.MinExecutedRuns, CurrentAdditional: requirement.MinExecutedRuns,
			Suggestions: make([]testCampaignPlanCommand, 0)}
		if !exists {
			base.Status, base.Reason = "unsatisfiable", "required scenario is absent from manifest"
			plan.Entries = append(plan.Entries, base)
			continue
		}
		if engine := requirement.Match.Engine; engine != nil && *engine != "real-vm" {
			base.Status, base.Reason = "unsupported", "coverage planning requires real-vm configuration fingerprints"
			plan.Entries = append(plan.Entries, base)
			continue
		}
		profiles := []string{"warm", "cold", "restored"}
		if requirement.Match.Profile != nil {
			selected, err := selectedTestProfiles(*requirement.Match.Profile)
			if err != nil || *requirement.Match.Profile == "all" {
				base.Status, base.Reason = "unsatisfiable", "profile selector does not name one native lifecycle profile"
				plan.Entries = append(plan.Entries, base)
				continue
			}
			profiles = selected
		}
		candidates, err := testCampaignPlanCandidates(scenario)
		if err != nil {
			return plan, err
		}
		matchedContexts := make(map[testCampaignCoverageKey]bool)
		matched := 0
		for candidateIndex, candidate := range candidates {
			if requirement.Match.Case != nil && *requirement.Match.Case != candidate.Case {
				continue
			}
			if requirement.Match.Configuration != nil && *requirement.Match.Configuration != candidate.Hash {
				continue
			}
			for _, profile := range profiles {
				matched++
				entry := base
				entry.Configuration, entry.RunsPerRepetition = candidate.Hash, candidate.Runs
				entry.Context = testFailureGroupingKeyForRequirement(testRunReceipt{Scenario: name, Profile: profile, Engine: "real-vm", Case: candidate.Case}, requirement.Match)
				key := testCampaignCoverageKey{Hash: candidate.Hash, Scope: testFailureGroupScope{scenario: name, profile: profile, engine: "real-vm", caseName: candidate.Case}}
				matchedContexts[key] = true
				for _, observed := range coverage {
					if observed.Name == requirement.Name && observed.Configuration == candidate.Hash &&
						observed.Context.Profile == profile && observed.Context.Engine == "real-vm" && observed.Context.Case == candidate.Case {
						entry.BaselineCovered, entry.CurrentCovered = observed.Baseline.CoveredRuns, observed.Current.CoveredRuns
					}
				}
				entry.BaselineAdditional = max(0, requirement.MinExecutedRuns-entry.BaselineCovered)
				entry.CurrentAdditional = max(0, requirement.MinExecutedRuns-entry.CurrentCovered)
				entry.Status, entry.Reason = testCampaignPlanDeclaration(candidate.Scenario, requirement.Match)
				if entry.Status == "declared" {
					entry.Status = "needs_runs"
					if entry.BaselineAdditional == 0 && entry.CurrentAdditional == 0 {
						entry.Status = "covered"
					}
					for _, side := range []struct{name string; deficit int}{{"baseline", entry.BaselineAdditional}, {"current", entry.CurrentAdditional}} {
						if side.deficit == 0 {
							continue
						}
						remaining := (side.deficit-1)/candidate.Runs + 1
						for batch := 1; remaining > 0 && batch <= 20; batch++ {
							repeat := min(20, remaining)
							remaining -= repeat
							entry.Suggestions = append(entry.Suggestions, testCampaignPlanCommand{Side: side.name, Repetitions: repeat, RemainingRepetitions: remaining,
								Argv: []string{"gregale", "test", "--manifest", manifestPath, "--scenario", name, "--engine", "real-vm", "--profile", profile,
									"--repeat", strconv.Itoa(repeat), "--report", fmt.Sprintf("coverage-plan-%d-%d-%s-%s-%d.json", requirementIndex+1, candidateIndex+1, profile, side.name, batch)}})
						}
					}
				}
				plan.Entries = append(plan.Entries, entry)
			}
		}
		if matched == 0 {
			base.Status, base.Reason = "unsatisfiable", "required case or configuration fingerprint is absent from manifest candidates"
			plan.Entries = append(plan.Entries, base)
		}
		// Historical configurations cannot silently disappear when the manifest changes.
		for _, observed := range coverage {
			if observed.Name != requirement.Name || (observed.Baseline.ExecutedRuns == 0 && observed.Current.ExecutedRuns == 0) {
				continue
			}
			key := testCampaignCoverageKey{Hash: observed.Configuration, Scope: testFailureGroupScope{scenario: observed.Context.Scenario,
				profile: observed.Context.Profile, engine: observed.Context.Engine, caseName: observed.Context.Case}}
			if !matchedContexts[key] {
				entry := base
				entry.Configuration, entry.Context = observed.Configuration, observed.Context
				entry.BaselineCovered, entry.CurrentCovered = observed.Baseline.CoveredRuns, observed.Current.CoveredRuns
				entry.BaselineAdditional = max(0, requirement.MinExecutedRuns-entry.BaselineCovered)
				entry.CurrentAdditional = max(0, requirement.MinExecutedRuns-entry.CurrentCovered)
				entry.Status, entry.Reason = "unavailable_configuration", "observed context cannot be regenerated by the supplied manifest; use its original manifest"
				plan.Entries = append(plan.Entries, entry)
			}
		}
	}
	environments, err := buildTestEnvironmentPlan(policy, scenarios, before, after)
	if err != nil {
		return plan, err
	}
	plan.Environments = environments
	if len(preflight) > 1 { return plan, errors.New("only one side-specific preflight is supported per plan") }
	if len(preflight) == 1 && preflight[0] != nil {
		if err := applyTestPlannerPreflight(&plan, policy, preflight[0]); err != nil { return plan, err }
	}
	applyTestEnvironmentPlan(&plan)
	return plan, nil
}

// Checks declarations only. Runtime traffic, fault matches and assertion evidence
// remain necessary before any suggested execution can satisfy required coverage.
func testCampaignPlanDeclaration(scenario testScenario, m testCampaignPolicySelector) (string, string) {
	load := scenario.Load
	if m.Step != nil && *m.Step != "" {
		found := 0
		for _, step := range scenario.Steps {
			if step.Name == *m.Step {
				found++
				load = step.Load
			}
		}
		if found != 1 {
			return "unsatisfiable", "required staged step is absent or ambiguous"
		}
	}
	if m.Phase == nil {
		return "declared", ""
	}
	if load == nil {
		return "unsatisfiable", "selected step has no declared load"
	}
	index, count := -1, 0
	for i, phase := range load.ChaosSchedule {
		if phase.Name == *m.Phase {
			index, count = i, count+1
		}
	}
	if count != 1 {
		return "unsatisfiable", "required phase is absent or ambiguous in the selected load"
	}
	if m.Kind == nil {
		return "declared", ""
	}
	phase := load.ChaosSchedule[index]
	request, kind, assertion := testRequiredSelectorValue(m.Request), *m.Kind, *m.Assertion
	present := false
	switch kind {
	case "threshold":
		budget := phase.Thresholds
		if request != "" {
			var exists bool
			budget, exists = phase.StepThresholds[request]
			if !exists {
				return "unsatisfiable", "required named request threshold is not declared in phase"
			}
		}
		switch assertion {
		case "p95_ms":
			present = budget.P95 != ""
		case "p99_ms":
			present = budget.P99 != ""
		case "error_rate":
			present = budget.ErrorRate != nil
		case "success_rate":
			present = budget.SuccessRate != nil
		case "min_samples":
			present = budget.MinSamples > 0
		}
	case "http_attempts", "per_request_attempts":
		present = phase.RetryAmplification != nil && phase.RetryAmplification.Request == request
		if kind == "per_request_attempts" {
			present = present && phase.RetryAmplification.PerRequest
		}
	case "tcp_attempts":
		present = phase.ConnectionAttempts != nil && phase.ConnectionAttempts.Request == request
	case "recovery":
		present = phase.Recovery != nil
		if assertion == "step_recovery" {
			present = phase.StepRecovery[request] != nil
		}
	case "request_deadline":
		_, present = phase.RequestDeadlines[request]
	case "circuit_breaker":
		present = phase.CircuitBreaker != nil
	case "connection_leaks", "pool_exhaustion":
		return "needs_review", "resource assertion placement requires manual review; no execution is suggested"
	}
	if !present {
		return "unsatisfiable", "required assertion is not declared in selected phase"
	}
	return "declared", ""
}
