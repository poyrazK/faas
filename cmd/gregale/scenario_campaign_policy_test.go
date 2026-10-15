package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func campaignPolicyTestSpec() testCampaignPolicy {
	limit := 10.0
	return testCampaignPolicy{SchemaVersion: 1, FailNewFailures: true, MaxFrequencyIncrease: &limit,
		MinExecutedRuns: 1, NotComparable: "fail", InsufficientSamples: "fail"}
}

func TestCampaignPolicyRejectsNewFailuresAndFrequencyIncrease(t *testing.T) {
	// adr: 984 — explicit policies gate comparable campaign regressions.
	old := digestComparisonReceipt("old", true, 50)
	current := digestComparisonReceipt("new", false, 200)
	result, err := evaluateTestCampaignPolicy(campaignPolicyTestSpec(), []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || len(result.Violations) != 2 || result.Violations[0].Rule != "new_failure_group" ||
		result.Violations[1].Rule != "frequency_increase" || result.Violations[0].Comparison == nil {
		t.Fatalf("regression was not explained: %+v", result)
	}
	policy := campaignPolicyTestSpec()
	policy.FailNewFailures = false
	limit := 100.0
	policy.MaxFrequencyIncrease = &limit
	result, err = evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || !result.Passed {
		t.Fatal("inclusive frequency boundary was rejected")
	}
}

func TestCampaignPolicyRequiresPassingConfigurationCoverage(t *testing.T) {
	// adr: 984 — absent passing contexts must not disappear from required coverage.
	old := digestComparisonReceipt("old", true, 50)
	current := digestComparisonReceipt("new", true, 50)
	current.Replay.ConfigurationSHA256 = strings.Repeat("b", 64)
	result, err := evaluateTestCampaignPolicy(campaignPolicyTestSpec(), []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || result.Passed || len(result.Violations) != 2 {
		t.Fatalf("changed passing configurations escaped coverage: %+v, %v", result, err)
	}
	for _, violation := range result.Violations {
		if violation.Rule != "required_coverage" {
			t.Fatal("missing configuration was treated as a frequency regression")
		}
	}
	policy := campaignPolicyTestSpec()
	policy.NotComparable = "ignore"
	result, err = evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || !result.Passed {
		t.Fatal("explicit coverage ignore was not honored")
	}
}

func TestCampaignPolicySampleHandling(t *testing.T) {
	// adr: 984 — inadequate samples are explicit and cannot drive frequency decisions.
	policy := campaignPolicyTestSpec()
	policy.MinExecutedRuns = 3
	old := digestComparisonReceipt("old", true, 50)
	current := digestComparisonReceipt("new", false, 200)
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || result.Passed || len(result.Violations) != 1 || result.Violations[0].Rule != "insufficient_samples" {
		t.Fatalf("unexpected sample decision: %+v, %v", result, err)
	}
	policy.InsufficientSamples = "ignore"
	result, err = evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || !result.Passed {
		t.Fatal("ignored low-sample comparison triggered a regression")
	}
}

func TestCampaignPolicyStrictDecoding(t *testing.T) {
	// adr: 984 — misspelled, ambiguous and unsupported policies fail before evaluation.
	valid := "schema_version: 1\nfail_new_failures: true\nmin_executed_runs: 3\non_not_comparable: fail\non_insufficient_samples: fail\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	for _, body := range []string{
		valid + "unknown: true\n",
		valid + "---\n{}\n",
		strings.Replace(valid, "schema_version: 1", "schema_version: 2", 1),
		strings.Replace(valid, "min_executed_runs: 3", "min_executed_runs: 0", 1),
		strings.Replace(valid, "on_not_comparable: fail", "on_not_comparable: maybe", 1),
		valid + "max_frequency_increase_percentage_points: .nan\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readTestCampaignPolicy(path); err == nil {
			t.Fatalf("accepted invalid policy: %s", body)
		}
	}
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTestCampaignPolicy(path); err != nil {
		t.Fatal(err)
	}
	if code := cmdTestCompare([]string{"--policy", path, "--from", "missing.json", "--baseline", "missing.json", "--budget", "legacy.yaml"}); code != 1 {
		t.Fatal("policy mode accepted legacy budget flags")
	}
}
