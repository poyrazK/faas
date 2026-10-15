package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCampaignPolicyLintFindsShadowingWithExactEmptySelectors(t *testing.T) {
	// adr: 984 — lint explains first-match rule precedence without changing decisions.
	scenario, assertion, empty, other := "checkout", "p95_ms", "", "search"
	flag := false
	policy := campaignPolicyTestSpec()
	policy.Rules = []testCampaignScopedRule{
		{Name: "broad", Match: testCampaignPolicySelector{Scenario: &scenario}, FailNewFailures: &flag},
		{Name: "specific", Match: testCampaignPolicySelector{Scenario: &scenario, Assertion: &assertion}, FailNewFailures: &flag},
		{Name: "other", Match: testCampaignPolicySelector{Scenario: &other}, FailNewFailures: &flag},
	}
	inspection := inspectTestCampaignPolicy(policy, nil, nil, false)
	if inspection.ObservationsSupplied || len(inspection.Diagnostics) != 1 ||
		inspection.Diagnostics[0].Code != "shadowed_rule" || inspection.Diagnostics[0].Rule != "specific" ||
		inspection.Diagnostics[0].EarlierRule != "broad" {
		t.Fatalf("wrong static diagnostics: %+v", inspection)
	}
	if testCampaignSelectorContains(testCampaignPolicySelector{Case: &empty}, testCampaignPolicySelector{}) {
		t.Fatal("explicit empty selector treated as wildcard")
	}
	if !testCampaignSelectorContains(testCampaignPolicySelector{}, testCampaignPolicySelector{Case: &empty}) {
		t.Fatal("omitted selector failed to contain empty value")
	}
}

func TestCampaignPolicyLintObservedMatchesAndCoverage(t *testing.T) {
	// adr: 984 — report matching follows evaluator eligibility for whole contexts.
	current := digestComparisonReceipt("pass", true, 50)
	scenario, assertion, absent, flag := current.Scenario, "p95_ms", "absent", false
	policy := campaignPolicyTestSpec()
	policy.Rules = []testCampaignScopedRule{
		{Name: "context", Match: testCampaignPolicySelector{Scenario: &scenario}, FailNewFailures: &flag},
		{Name: "assertion", Match: testCampaignPolicySelector{Assertion: &assertion}, FailNewFailures: &flag},
		{Name: "missing", Match: testCampaignPolicySelector{Scenario: &absent}, FailNewFailures: &flag},
	}
	inspection := inspectTestCampaignPolicy(policy, nil, []testRunReceipt{current}, true)
	if !inspection.ObservationsSupplied || inspection.Usage[0].Matches != 1 || inspection.Usage[0].Selected != 1 ||
		inspection.Usage[1].Matches != 0 || inspection.Usage[2].Matches != 0 {
		t.Fatalf("passing context invented assertion matches: %+v", inspection)
	}
	current = digestComparisonReceipt("fail", false, 200)
	inspection = inspectTestCampaignPolicy(policy, nil, []testRunReceipt{current}, true)
	if inspection.Usage[1].Matches != 1 || inspection.Usage[1].Selected != 0 {
		t.Fatal("first-failure match did not retain earlier-rule selection")
	}
	found := false
	for _, diagnostic := range inspection.Diagnostics {
		found = found || (diagnostic.Rule == "assertion" && diagnostic.Code == "never_selected")
	}
	if !found {
		t.Fatal("missing observed precedence warning")
	}
}

func TestCampaignPolicyPreviewIsInformational(t *testing.T) {
	// adr: 984 — preview predicts gate failures but never acts as a regression gate.
	dir := t.TempDir()
	oldPath, newPath, policyPath := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json"), filepath.Join(dir, "policy.yaml")
	for path, passed := range map[string]bool{oldPath: true, newPath: false} {
		body, err := json.Marshal([]testRunReceipt{digestComparisonReceipt(path, passed, 200)})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := "schema_version: 1\nfail_new_failures: true\nmin_executed_runs: 1\non_not_comparable: fail\non_insufficient_samples: fail\n"
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTestPolicy([]string{"preview", "--policy", policyPath, "--from", newPath, "--baseline", oldPath}); code != 0 {
		t.Fatalf("preview acted as a gate: %d", code)
	}
	if code := cmdTestPolicy([]string{"preview", "--policy", policyPath, "--from", newPath}); code != 1 {
		t.Fatal("preview accepted missing baseline")
	}
	if code := cmdTestPolicy([]string{"lint", "--policy", policyPath}); code != 0 {
		t.Fatal("static lint required reports")
	}
	if code := cmdTestPolicy([]string{"lint", "--policy", policyPath, "--baseline", oldPath}); code != 1 {
		t.Fatal("lint accepted baseline without current report")
	}
}
