package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCampaignPolicyJUnitEscapesAndPreservesEvidence(t *testing.T) {
	// adr: 984 — policy failures retain typed measurements and replay evidence in CI.
	old := digestComparisonReceipt("old", true, 50)
	current := digestComparisonReceipt("new<&", false, 200)
	policy := campaignPolicyTestSpec()
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current},
		"https://example.test/old.html", "https://example.test/new.html")
	if err != nil {
		t.Fatal(err)
	}
	result.Violations[0].Reason = "regression <threshold> & \"budget\""
	body, err := marshalTestCampaignPolicyJUnit(policy, result)
	if err != nil {
		t.Fatal(err)
	}
	var suite testCampaignJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Tests != 2 || suite.Failures != 2 || len(suite.Cases) != 2 ||
		suite.Cases[0].Failure == nil || suite.Cases[0].Failure.Message != result.Violations[0].Reason {
		t.Fatalf("invalid JUnit failure mapping: %+v", suite)
	}
	var detail struct {
		MinExecutedRuns int                         `json:"min_executed_runs"`
		Violation       testCampaignPolicyViolation `json:"violation"`
	}
	if err := json.Unmarshal([]byte(suite.Cases[0].Failure.Body), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.MinExecutedRuns != 1 || detail.Violation.Comparison == nil ||
		detail.Violation.Comparison.Current.Failures != 1 ||
		detail.Violation.Comparison.Current.Occurrences[0].RunID != "new<&" ||
		detail.Violation.Comparison.Current.Occurrences[0].Replay == "" ||
		!strings.Contains(detail.Violation.Comparison.Current.EvidenceURL, "#run-evidence-0") {
		t.Fatal("JUnit lost policy measurements, evidence or replay commands")
	}
	if suite.Cases[0].Name == suite.Cases[1].Name {
		t.Fatal("distinct violations share a testcase name")
	}
}

func TestCampaignPolicyJUnitPassingAndCoverageResults(t *testing.T) {
	// adr: 984 — passing gates and missing coverage remain distinct from application verdicts.
	for _, violations := range [][]testCampaignPolicyViolation{
		nil,
		{{Rule: "insufficient_samples", Reason: "minimum samples unavailable", BeforeRuns: 1, AfterRuns: 2}},
	} {
		body, err := marshalTestCampaignPolicyJUnit(campaignPolicyTestSpec(), testCampaignPolicyResult{
			Passed: len(violations) == 0, Violations: violations,
		})
		if err != nil {
			t.Fatal(err)
		}
		var suite testCampaignJUnitSuite
		if err := xml.Unmarshal(body, &suite); err != nil {
			t.Fatal(err)
		}
		if suite.Tests != 1 || suite.Failures != len(violations) {
			t.Fatalf("wrong JUnit totals: %+v", suite)
		}
		if len(violations) == 0 && (suite.Cases[0].Failure != nil || suite.Cases[0].Output == "") {
			t.Fatal("accepted policy was emitted as an application failure")
		}
	}
}

func TestCampaignPolicyJUnitCLIAndInputAliases(t *testing.T) {
	// adr: 984 — export preserves input evidence and policy gate exit semantics.
	dir := t.TempDir()
	oldPath, newPath := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	policyPath, output := filepath.Join(dir, "policy.yaml"), filepath.Join(dir, "policy.xml")
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
	args := []string{"--from", newPath, "--baseline", oldPath, "--policy", policyPath}
	for _, path := range []string{oldPath, newPath, policyPath} {
		if code := cmdTestCompare(append(append([]string{}, args...), "--junit", path)); code != 1 {
			t.Fatalf("input alias returned %d", code)
		}
	}
	alias := filepath.Join(dir, "linked.xml")
	if err := os.Link(oldPath, alias); err != nil {
		t.Fatal(err)
	}
	if code := cmdTestCompare(append(append([]string{}, args...), "--junit", alias)); code != 1 {
		t.Fatal("hard-linked input alias was accepted")
	}
	if code := cmdTestCompare(append(append([]string{}, args...), "--junit", output)); code != 2 {
		t.Fatalf("policy violation exit changed to %d", code)
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var suite testCampaignJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 {
		t.Fatalf("invalid CLI JUnit output: %v, %+v", err, suite)
	}
	if code := cmdTestCompare(append(append([]string{}, args...), "--junit", filepath.Join(dir, "absent", "policy.xml"))); code != 1 {
		t.Fatalf("write failure returned %d", code)
	}
}
