package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCoveragePlanDeficitsUseManifestIdentityAndResilienceMultiplier(t *testing.T) {
	// adr: 984 — coverage planning uses exact configurations and declared leaf counts.
	scenario := testScenario{Project: "checkout", Command: []string{"true"}, Resilience: &testResilienceSpec{Runs: 3}}
	old := digestComparisonReceipt("old", true, 50)
	old.Scenario, old.Profile = "checkout", "warm"
	old.ConfigurationSHA256 = newTestReplayManifest("checkout", "warm", "", scenario).ConfigurationSHA256
	current := old
	current.RunID = "new"
	policy := requiredCoverageTestPolicy(old)
	profile := "warm"
	policy.RequiredCoverage[0].Match.Profile = &profile
	policy.RequiredCoverage[0].MinExecutedRuns = 5
	plan, err := buildTestCampaignPolicyPlan(policy, map[string]testScenario{"checkout": scenario}, "manifest.yaml", []testRunReceipt{old}, []testRunReceipt{current, current})
	if err != nil || len(plan.Entries) != 1 {
		t.Fatalf("unexpected plan: %+v %v", plan, err)
	}
	entry := plan.Entries[0]
	if entry.Status != "needs_runs" || entry.BaselineAdditional != 4 || entry.CurrentAdditional != 3 ||
		entry.RunsPerRepetition != 3 || len(entry.Suggestions) != 2 ||
		entry.Suggestions[0].Repetitions != 2 || entry.Suggestions[1].Repetitions != 1 {
		t.Fatalf("wrong deficit or repeat multiplier: %+v", entry)
	}
	if entry.Suggestions[0].Argv[0] != "gregale" || entry.Suggestions[0].Side != "baseline" ||
		entry.Suggestions[1].Side != "current" {
		t.Fatal("suggestions lost their executable or source side")
	}
}

func TestCoveragePlanFlagsAbsentDeclarationsAndHistoricalConfigurations(t *testing.T) {
	// adr: 984 — changed manifests must not silently replace required historical contexts.
	r := digestComparisonReceipt("old", true, 50)
	r.Scenario, r.Profile = "checkout", "warm"
	policy := requiredCoverageTestPolicy(r)
	profile := "warm"
	policy.RequiredCoverage[0].Match.Profile = &profile
	scenario := testScenario{Project: "checkout", Command: []string{"true"}}
	plan, err := buildTestCampaignPolicyPlan(policy, nil, "manifest.yaml", nil, nil)
	if err != nil || len(plan.Entries) != 1 || plan.Entries[0].Status != "unsatisfiable" || len(plan.Entries[0].Suggestions) != 0 {
		t.Fatal("absent scenario received an executable plan")
	}
	plan, err = buildTestCampaignPolicyPlan(policy, map[string]testScenario{"checkout": scenario}, "manifest.yaml", []testRunReceipt{r}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range plan.Entries {
		if entry.Configuration == r.Replay.ConfigurationSHA256 {
			found = entry.Status == "unavailable_configuration" && len(entry.Suggestions) == 0
		}
	}
	if !found {
		t.Fatal("unavailable historical configuration disappeared")
	}
	phase, step, kind, assertion := "recovery", "exercise", "threshold", "p95_ms"
	m := testCampaignPolicySelector{Step: &step, Phase: &phase, Kind: &kind, Assertion: &assertion}
	if status, _ := testCampaignPlanDeclaration(scenario, m); status != "unsatisfiable" {
		t.Fatal("absent step accepted")
	}
	scenario.Steps = []testScenarioStep{{Name: step, Load: &testLoadSpec{ChaosSchedule: []testLoadChaosPhaseSpec{{Name: phase}}}}}
	if status, _ := testCampaignPlanDeclaration(scenario, m); status != "unsatisfiable" {
		t.Fatal("absent assertion accepted")
	}
	scenario.Steps[0].Load.ChaosSchedule[0].Thresholds.P95 = "100ms"
	if status, _ := testCampaignPlanDeclaration(scenario, m); status != "declared" {
		t.Fatal("declared assertion not recognized")
	}
}

func TestCoveragePlanBoundsSuggestionsAndRespectsProfileCaseSelectors(t *testing.T) {
	// adr: 984 — planning never invents profile selectors or exceeds repeat limits.
	r := digestComparisonReceipt("run", true, 50)
	r.Scenario = "checkout"
	policy := requiredCoverageTestPolicy(r)
	profile := "warm"
	policy.RequiredCoverage[0].Match.Profile = &profile
	policy.RequiredCoverage[0].MinExecutedRuns = 1000
	scenarios := map[string]testScenario{"checkout": {Project: "checkout", Command: []string{"true"}}}
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || len(plan.Entries) != 1 || len(plan.Entries[0].Suggestions) != 40 {
		t.Fatal("suggestions were not bounded per side")
	}
	for _, command := range plan.Entries[0].Suggestions {
		if command.Repetitions < 1 || command.Repetitions > 20 {
			t.Fatal("suggested repeat count exceeds CLI limits")
		}
	}
	if plan.Entries[0].Suggestions[19].RemainingRepetitions != 600 {
		t.Fatal("truncated plan lost remaining repetitions")
	}
	caseName := "missing-case"
	policy.RequiredCoverage[0].Match.Case = &caseName
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || plan.Entries[0].Status != "unsatisfiable" {
		t.Fatal("unknown case received commands")
	}
}

func TestCoveragePlanCLIIsReadOnly(t *testing.T) {
	// adr: 984 — planning reads declarations without invoking application commands.
	dir := t.TempDir()
	manifestPath, policyPath := filepath.Join(dir, "manifest.yaml"), filepath.Join(dir, "policy.yaml")
	manifest := "version: 1\nscenarios:\n  checkout:\n    project: checkout\n    source: .\n    command: [must-never-run-this-command]\n"
	policy := "schema_version: 1\nmin_executed_runs: 1\non_not_comparable: fail\non_insufficient_samples: fail\nrequired_coverage:\n  - name: checkout\n    match: {scenario: checkout, profile: warm}\n    min_executed_runs: 1\n"
	for path, body := range map[string]string{manifestPath: manifest, policyPath: policy} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if code := cmdTestPolicy([]string{"plan", "--policy", policyPath, "--manifest", manifestPath}); code != 0 {
		t.Fatalf("read-only plan returned %d", code)
	}
	for path, expected := range map[string]string{manifestPath: manifest, policyPath: policy} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != expected {
			t.Fatal("planner changed an input")
		}
	}
	if code := cmdTestPolicyPlan([]string{"--policy", policyPath, "--manifest", filepath.Join(dir, "absent.yaml")}); code != 1 {
		t.Fatal("missing manifest was accepted")
	}
}
