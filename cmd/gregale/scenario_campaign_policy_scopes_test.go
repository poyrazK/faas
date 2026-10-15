package main

import (
	"encoding/json"
	"encoding/xml"
	"testing"
)

func TestCampaignScopedPolicyPrecedenceAndInheritance(t *testing.T) {
	// adr: 984 — first matching exact selector wins without merging later rules.
	policy := campaignPolicyTestSpec()
	phase, assertion := "fault", "p95_ms"
	minimum, disabled := 9, false
	policy.Rules = []testCampaignScopedRule{
		{Name: "latency", Match: testCampaignPolicySelector{Assertion: &assertion}, MinExecutedRuns: &minimum, FailNewFailures: &disabled},
		{Name: "faults", Match: testCampaignPolicySelector{Phase: &phase}, DisableFrequencyCheck: true},
	}
	selected, name := selectTestCampaignPolicy(policy, testFailureGroupKey{Phase: phase, Assertion: assertion}, "", false)
	if name != "latency" || selected.MinExecutedRuns != 9 || selected.FailNewFailures || selected.MaxFrequencyIncrease == nil {
		t.Fatal("first-match inheritance changed")
	}
	selected, name = selectTestCampaignPolicy(policy, testFailureGroupKey{Phase: phase}, "", false)
	if name != "faults" || selected.MaxFrequencyIncrease != nil {
		t.Fatal("explicit frequency disabling was ignored")
	}
	_, name = selectTestCampaignPolicy(policy, testFailureGroupKey{Phase: phase, Assertion: assertion}, "", true)
	if name != "default" {
		t.Fatal("assertion selector waived context coverage")
	}
}

func TestCampaignScopedPolicyJSONAndJUnitUseMatchedRule(t *testing.T) {
	// adr: 984 — policy decisions and CI evidence retain effective scoped thresholds.
	old := digestComparisonReceipt("old", true, 50)
	current := digestComparisonReceipt("new", false, 200)
	assertion := "p95_ms"
	limit := 25.0
	policy := campaignPolicyTestSpec()
	policy.Rules = []testCampaignScopedRule{{Name: "latency", Match: testCampaignPolicySelector{Assertion: &assertion}, MaxFrequencyIncrease: &limit}}
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decisions) != 1 || result.Decisions[0].PolicyRule != "latency" || len(result.Violations) != 2 {
		t.Fatalf("missing scoped decisions: %+v", result)
	}
	for _, v := range result.Violations {
		if v.PolicyRule != "latency" || v.EffectivePolicy == nil || *v.EffectivePolicy.MaxFrequencyIncrease != 25 {
			t.Fatal("violation lost effective settings")
		}
	}
	body, err := marshalTestCampaignPolicyJUnit(policy, result)
	if err != nil {
		t.Fatal(err)
	}
	var suite testCampaignJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Limit     float64                     `json:"max_frequency_increase_percentage_points"`
		Violation testCampaignPolicyViolation `json:"violation"`
	}
	if err := json.Unmarshal([]byte(suite.Cases[0].Failure.Body), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Limit != 25 || detail.Violation.PolicyRule != "latency" {
		t.Fatal("JUnit reported global instead of scoped policy")
	}
}

func TestCampaignScopedSampleAndContextCoverage(t *testing.T) {
	// adr: 984 — scoped sample checks do not remove global context requirements.
	old := digestComparisonReceipt("old", true, 50)
	current := digestComparisonReceipt("new", false, 200)
	assertion, minimum := "p95_ms", 5
	policy := campaignPolicyTestSpec()
	policy.Rules = []testCampaignScopedRule{{Name: "more-samples", Match: testCampaignPolicySelector{Assertion: &assertion}, MinExecutedRuns: &minimum}}
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || len(result.Violations) != 1 || result.Violations[0].Rule != "insufficient_samples" || result.Violations[0].PolicyRule != "more-samples" {
		t.Fatalf("scoped samples not enforced: %+v %v", result, err)
	}
	scenario, ignore := old.Scenario, "ignore"
	policy.Rules = []testCampaignScopedRule{{Name: "optional-context", Match: testCampaignPolicySelector{Scenario: &scenario}, NotComparable: &ignore}}
	result, err = evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, nil, "", "")
	if err != nil || !result.Passed {
		t.Fatal("context-level coverage exception was ignored")
	}
}

func TestCampaignScopedPolicyValidation(t *testing.T) {
	// adr: 984 — ambiguous rule names and invalid overrides are rejected.
	value, zero, action := "checkout", 0, "maybe"
	limit := 10.0
	valid := testCampaignScopedRule{Name: "checkout", Match: testCampaignPolicySelector{Scenario: &value}, MaxFrequencyIncrease: &limit}
	if err := validateTestCampaignScopedRules([]testCampaignScopedRule{valid}); err != nil {
		t.Fatal(err)
	}
	for _, rules := range [][]testCampaignScopedRule{
		{valid, valid},
		{{Name: "empty", Match: valid.Match}},
		{{Name: "default", Match: valid.Match, MaxFrequencyIncrease: &limit}},
		{{Name: "all", MaxFrequencyIncrease: &limit}},
		{{Name: "zero", Match: valid.Match, MinExecutedRuns: &zero}},
		{{Name: "bad-action", Match: valid.Match, NotComparable: &action}},
		{{Name: "conflict", Match: valid.Match, MaxFrequencyIncrease: &limit, DisableFrequencyCheck: true}},
	} {
		if err := validateTestCampaignScopedRules(rules); err == nil {
			t.Fatal("invalid scoped policy accepted")
		}
	}
}
