package main

import "testing"

func environmentPlanTestInputs() (testCampaignPolicy, map[string]testScenario, testRunReceipt) {
	scenario := testScenario{Project: "checkout", Command: []string{"true"}}
	r := environmentTestReceipt("run", true)
	r.Scenario, r.Profile = "checkout", "warm"
	r.ConfigurationSHA256 = newTestReplayManifest("checkout", "warm", "", scenario).ConfigurationSHA256
	policy := requiredCoverageTestPolicy(r)
	profile := "warm"
	policy.RequiredCoverage[0].Match.Profile = &profile
	policy.RequiredCoverage[0].MinExecutedRuns = 2
	policy.RequiredEnvironments = requiredEnvironmentTestPolicy(r).RequiredEnvironments
	policy.RequiredEnvironments[0].Match.Profile = &profile
	return policy, map[string]testScenario{"checkout": scenario}, r
}

func TestEnvironmentPlanBlocksMissingDependencies(t *testing.T) {
	// adr: 984 — additional load must not be suggested before correcting declared dependencies.
	policy, scenarios, _ := environmentPlanTestInputs()
	policy.RequiredEnvironments[0].Expect.Workloads["database"] = testWorkloadExpectation{}
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || len(plan.Environments) != 1 || len(plan.Entries) != 1 {
		t.Fatalf("unexpected plan: %+v %v", plan, err)
	}
	if plan.Environments[0].Status != "manifest_change_required" || plan.Entries[0].Status != "environment_blocked" ||
		len(plan.Entries[0].Suggestions) != 0 || len(plan.Entries[0].EnvironmentBlockers) != 2 {
		t.Fatal("missing dependency still received execution suggestions")
	}
	found := false
	for _, check := range plan.Environments[0].ManifestChecks {
		if check.Field == "workloads.database.present" && check.Source == "manifest" &&
			check.Status == "change_required" && string(check.Expected) == "true" && string(check.Declared) == "false" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing dependency did not identify the manifest change")
	}
}

func TestEnvironmentPlanSeparatesPlatformEvidenceFromDeclarations(t *testing.T) {
	// adr: 984 — manifests cannot prove API-derived resource limits or runtime labels.
	policy, scenarios, r := environmentPlanTestInputs()
	memory := 128
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{RAMMB: &memory}
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || plan.Environments[0].Baseline.Action != "platform_evidence_required" ||
		len(plan.Environments[0].PlatformFields) != 1 || len(plan.Entries[0].Suggestions) != 0 {
		t.Fatal("planner inferred resources from scenario declarations")
	}
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", []testRunReceipt{r}, []testRunReceipt{r})
	if err != nil || plan.Environments[0].Status != "ready" || len(plan.Entries[0].Suggestions) != 2 {
		t.Fatalf("matching platform evidence did not unblock planning: %+v %v", plan, err)
	}
}

func TestEnvironmentPlanBlocksOnlyNoncompliantReportSide(t *testing.T) {
	// adr: 984 — extra repetitions do not remove already recorded environment failures.
	policy, scenarios, good := environmentPlanTestInputs()
	bad := environmentTestReceipt("bad", true)
	bad.Scenario, bad.Profile, bad.ConfigurationSHA256 = good.Scenario, good.Profile, good.ConfigurationSHA256
	workload := bad.Environment.Workloads["checkout"]
	workload.RAMMB = 256
	bad.Environment.Workloads["checkout"] = workload
	memory := 128
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{RAMMB: &memory}
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", []testRunReceipt{good}, []testRunReceipt{bad})
	if err != nil || plan.Environments[0].Baseline.Action != "ready" ||
		plan.Environments[0].Current.Action != "platform_change_required" || !plan.Environments[0].Current.ReportReplacementRequired {
		t.Fatalf("noncompliant report action was lost: %+v %v", plan, err)
	}
	entry := plan.Entries[0]
	if entry.Status != "partially_environment_blocked" || len(entry.Suggestions) != 1 ||
		entry.Suggestions[0].Side != "baseline" || entry.EnvironmentBlockers[0].Side != "current" {
		t.Fatal("one blocked side removed valid suggestions or retained invalid ones")
	}
}

func TestEnvironmentPlanCannotInventVersionCollection(t *testing.T) {
	// adr: 984 — native capture limitations must remain visible in repeat planning.
	policy, scenarios, r := environmentPlanTestInputs()
	policy.RequiredEnvironments[0].Expect.RequirePlatformVersion = true
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || plan.Environments[0].Status != "version_evidence_required" || len(plan.Entries[0].Suggestions) != 0 {
		t.Fatal("planner suggested runs that cannot collect required versions")
	}
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", []testRunReceipt{r}, []testRunReceipt{r})
	if err != nil || plan.Environments[0].Status != "ready_existing_evidence" || len(plan.Entries[0].Suggestions) != 0 {
		t.Fatal("existing version evidence was mistaken for future capture capability")
	}
	policy.RequiredCoverage[0].MinExecutedRuns = 1
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", []testRunReceipt{r}, []testRunReceipt{r})
	if err != nil || plan.Entries[0].Status != "covered" || len(plan.Entries[0].EnvironmentBlockers) != 0 {
		t.Fatal("already satisfied reports acquired a new-run version blocker")
	}
}

func TestEnvironmentPlanAllowsMatchingDeclarationsWithoutInventingProof(t *testing.T) {
	// adr: 984 — declared settings can guide execution while remaining distinct from runtime proof.
	policy, scenarios, _ := environmentPlanTestInputs()
	no, zero, ports := false, 0, []int{}
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{Postgres: &no, FailFirst: &zero, TCPPorts: &ports}
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || plan.Environments[0].Status != "collect_execution_evidence" || len(plan.Entries[0].Suggestions) != 2 {
		t.Fatalf("matching manifest declarations were blocked: %+v %v", plan, err)
	}
	policy.RequiredCoverage = nil
	plan, err = buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || len(plan.Entries) != 0 || len(plan.Environments) != 1 {
		t.Fatal("environment-only policy disappeared from the plan")
	}
}

func TestEnvironmentPlanAppliesMatrixBlockerToSiblingCommands(t *testing.T) {
	// adr: 984 — scenario commands execute the full matrix, so sibling prerequisites matter.
	plan := testCampaignPolicyPlan{
		Entries: []testCampaignPolicyPlanEntry{{
			Context: testFailureGroupKey{Scenario: "checkout", Engine: "real-vm", Profile: "warm", Case: "case-b"},
			Status:  "needs_runs", Suggestions: []testCampaignPlanCommand{{Side: "baseline"}, {Side: "current"}},
		}},
		Environments: []testEnvironmentPlanEntry{{
			Requirement: "case-a-resources", Context: testFailureGroupKey{Scenario: "checkout", Engine: "real-vm", Profile: "warm", Case: "case-a"},
			Baseline: testEnvironmentPlanSide{Action: "ready"},
			Current:  testEnvironmentPlanSide{Action: "platform_evidence_required", Reason: "confirm resources"},
		}},
	}
	applyTestEnvironmentPlan(&plan)
	if len(plan.Entries[0].Suggestions) != 1 || plan.Entries[0].Suggestions[0].Side != "baseline" ||
		plan.Entries[0].Status != "partially_environment_blocked" {
		t.Fatal("a sibling case blocker was bypassed by a full-matrix command")
	}
}

func TestEnvironmentPlanInvalidProfilePreventsFutileBatches(t *testing.T) {
	// adr: 984 — an unexecutable profile requirement needs a policy correction, not more native runs.
	policy, scenarios, _ := environmentPlanTestInputs()
	profile := "all"
	policy.RequiredEnvironments[0].Match.Profile = &profile
	plan, err := buildTestCampaignPolicyPlan(policy, scenarios, "manifest.yaml", nil, nil)
	if err != nil || !plan.Environments[0].AllProfiles || len(plan.Entries[0].Suggestions) != 0 {
		t.Fatal("unexecutable exact profile selector received additional batches")
	}
}
