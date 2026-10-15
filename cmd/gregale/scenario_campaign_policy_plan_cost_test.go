package main

import "testing"

func TestCampaignCostPlanSharesRequirementsWithoutPoolingRevisionSides(t *testing.T) {
	// adr: 984 — one execution may exercise multiple declarations, but revisions stay separate.
	scenario := testScenario{Project:"checkout", Command:[]string{"true"}, Timeout:"2m",
		Services:map[string]testService{"database":{}}, Resilience:&testResilienceSpec{Runs:3}}
	r := digestComparisonReceipt("seed",true,50)
	r.Scenario = "checkout"
	policy := requiredCoverageTestPolicy(r)
	profile := "warm"
	policy.RequiredCoverage[0].Match.Profile = &profile
	policy.RequiredCoverage[0].MinExecutedRuns = 5
	second := policy.RequiredCoverage[0]
	second.Name,second.MinExecutedRuns = "also-checkout",8
	policy.RequiredCoverage = append(policy.RequiredCoverage,second)
	scenarios := map[string]testScenario{"checkout":scenario}
	plan,err := buildTestCampaignPolicyPlan(policy,scenarios,"manifest.yaml",nil,nil)
	if err != nil { t.Fatal(err) }
	if err := optimizeTestCampaignPlan(&plan,scenarios,"manifest.yaml",testCampaignPlanBudget{}); err != nil { t.Fatal(err) }
	if len(plan.Batches) != 2 || plan.Batches[0].Repetitions != 3 || plan.Batches[1].Repetitions != 3 {
		t.Fatalf("shared demands should use max, not sum: %+v",plan.Batches)
	}
	if plan.Batches[0].Side == plan.Batches[1].Side || plan.Costs.Estimated.Runs != 18 ||
		plan.Costs.Estimated.Deployments != 36 || plan.Costs.Estimated.WorkloadMinutes != 72 {
		t.Fatalf("incorrect side or workload accounting: %+v",plan.Costs)
	}
	for _,entry := range plan.Entries {
		if len(entry.Suggestions) != 0 || len(entry.Planned.BatchIDs) != 2 ||
			entry.Planned.BaselineRemaining != 0 || entry.Planned.CurrentRemaining != 0 ||
			entry.BaselineCovered != 0 || entry.CurrentCovered != 0 {
			t.Fatalf("planned evidence was confused with observed evidence: %+v",entry)
		}
	}
}

func TestCampaignCostBudgetIncludesUnselectedMatrixCases(t *testing.T) {
	// adr: 984 — a filtered requirement cannot discount sibling cases executed by the CLI.
	scenario := testScenario{Project:"checkout",Command:[]string{"true"},Timeout:"1m",
		Steps:[]testScenarioStep{{Name:"exercise",Load:&testLoadSpec{ChaosSchedule:[]testLoadChaosPhaseSpec{
			{Name:"fault",Rules:[]testChaosRule{{}}}, {Name:"recovery",Recovery:&testLoadRecoverySpec{}},
		}}}},
		Resilience:&testResilienceSpec{Runs:2,FaultMatrix:[]testFaultMatrixCaseSpec{
			{Name:"first",Step:"exercise",Phase:"fault",Rules:[]testChaosRule{{}}},
			{Name:"second",Step:"exercise",Phase:"fault",Rules:[]testChaosRule{{}}},
		}}}
	entry := testCampaignPolicyPlanEntry{Context:testFailureGroupKey{Scenario:"checkout",Profile:"warm",Engine:"real-vm",Case:"first"},
		RunsPerRepetition:2,BaselineAdditional:5,CurrentAdditional:5,
		Suggestions:[]testCampaignPlanCommand{{Side:"baseline"},{Side:"current"}}}
	plan := testCampaignPolicyPlan{Entries:[]testCampaignPolicyPlanEntry{entry}}
	scenarios := map[string]testScenario{"checkout":scenario}
	if err := optimizeTestCampaignPlan(&plan,scenarios,"manifest.yaml",testCampaignPlanBudget{Runs:4}); err != nil { t.Fatal(err) }
	if len(plan.Batches) != 1 || plan.Batches[0].Repetitions != 1 ||
		plan.Costs.Estimated.Runs != 4 || plan.Costs.Estimated.Deployments != 4 ||
		plan.Entries[0].Planned.BaselineRemaining != 3 || plan.Entries[0].Planned.CurrentRemaining != 5 {
		t.Fatalf("budget omitted matrix cases or crossed sides: %+v",plan)
	}
}

func TestCampaignCostBudgetsPreserveBlockedAndTruncatedGaps(t *testing.T) {
	// adr: 984 — planning must retain unavailable evidence and respect all budget dimensions.
	for _,budget := range []testCampaignPlanBudget{{Deployments:2},{WorkloadMinutes:4}} {
		entries := []testCampaignPolicyPlanEntry{
			{Context:testFailureGroupKey{Scenario:"checkout",Profile:"warm"},RunsPerRepetition:1,
				CurrentAdditional:1000,Suggestions:[]testCampaignPlanCommand{{Side:"current"}}},
			{Context:testFailureGroupKey{Scenario:"checkout",Profile:"cold"},RunsPerRepetition:1,
				CurrentAdditional:3,Status:"environment_blocked"},
		}
		plan := testCampaignPolicyPlan{Entries:entries}
		scenarios := map[string]testScenario{"checkout":{Timeout:"2m"}}
		if err := optimizeTestCampaignPlan(&plan,scenarios,"m.yaml",budget); err != nil { t.Fatal(err) }
		if len(plan.Batches) != 1 || plan.Batches[0].Repetitions != 2 ||
			plan.Entries[0].Planned.CurrentRemaining != 998 || plan.Entries[1].Planned.CurrentRemaining != 3 {
			t.Fatalf("budget or blocker lost: %+v",plan)
		}
	}
	plan := testCampaignPolicyPlan{Entries:[]testCampaignPolicyPlanEntry{{
		Context:testFailureGroupKey{Scenario:"checkout",Profile:"warm"},RunsPerRepetition:1,
		CurrentAdditional:1000,Suggestions:[]testCampaignPlanCommand{{Side:"current"}},
	}}}
	if err := optimizeTestCampaignPlan(&plan,map[string]testScenario{"checkout":{}},"m.yaml",testCampaignPlanBudget{}); err != nil { t.Fatal(err) }
	if len(plan.Batches) != 20 || plan.Entries[0].Planned.CurrentRemaining != 600 { t.Fatal("output cap lost its residual gap") }
	if err := optimizeTestCampaignPlan(&testCampaignPolicyPlan{},nil,"m",testCampaignPlanBudget{Runs:-1}); err == nil { t.Fatal("negative budget accepted") }
	if _,err := testCampaignCostMultiply(int64(^uint64(0)>>1),2); err == nil { t.Fatal("overflow accepted") }
}
