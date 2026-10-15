package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requiredCoverageTestPolicy(r testRunReceipt) testCampaignPolicy {
	policy := campaignPolicyTestSpec()
	policy.FailNewFailures, policy.MaxFrequencyIncrease = false, nil
	policy.NotComparable, policy.InsufficientSamples = "ignore", "ignore"
	scenario := r.Scenario
	policy.RequiredCoverage = []testCampaignCoverageRequirement{{
		Name: "critical", Match: testCampaignPolicySelector{Scenario: &scenario}, MinExecutedRuns: 1,
	}}
	return policy
}

func TestRequiredCoverageMissingFromBothReportsCannotPass(t *testing.T) {
	// adr: 984 — explicit requirements exist independently of observed report contexts.
	r := digestComparisonReceipt("passing", true, 50)
	policy := requiredCoverageTestPolicy(r)
	missing := "not-in-either-report"
	policy.RequiredCoverage[0].Match.Scenario = &missing
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{r}, []testRunReceipt{r}, "", "")
	if err != nil || result.Passed || len(result.RequiredCoverage) != 1 || len(result.Violations) != 1 {
		t.Fatalf("missing mandatory scenario passed: %+v, %v", result, err)
	}
	coverage := result.RequiredCoverage[0]
	if coverage.Baseline.CoveredRuns != 0 || coverage.Current.CoveredRuns != 0 ||
		result.Violations[0].Requirement == nil || result.Violations[0].Rule != "explicit_required_coverage" {
		t.Fatal("missing requirement lost counts or diagnostic identity")
	}
	body, err := marshalTestCampaignPolicyJUnit(policy, result)
	if err != nil {
		t.Fatal(err)
	}
	var suite testCampaignJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 ||
		!strings.Contains(suite.Cases[0].Failure.Body, "required_coverage/critical") {
		t.Fatalf("JUnit omitted mandatory coverage: %v", err)
	}
}

func TestRequiredCoverageUsesRawPassingAndFailedAssertions(t *testing.T) {
	// adr: 984 — exercised evidence counts independently of first-failure grouping.
	old := digestComparisonReceipt("old", true, 50)
	current := digestComparisonReceipt("new", false, 200)
	policy := requiredCoverageTestPolicy(old)
	phase, kind, assertion := old.Load.ChaosPhases[0].Name, "threshold", "p95_ms"
	policy.RequiredCoverage[0].Match.Phase = &phase
	policy.RequiredCoverage[0].Match.Kind = &kind
	policy.RequiredCoverage[0].Match.Assertion = &assertion
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || !result.Passed || len(result.RequiredCoverage) != 1 {
		t.Fatalf("raw assertion evidence was ignored: %+v %v", result, err)
	}
	coverage := result.RequiredCoverage[0]
	if !coverage.Passed || coverage.Baseline.CoveredRuns != 1 || coverage.Current.CoveredRuns != 1 ||
		len(coverage.Current.SupportingReceipts) != 1 || coverage.Current.SupportingReceipts[0] != 0 {
		t.Fatal("coverage counts or evidence indexes lost")
	}
}

func TestRequiredCoverageRejectsIncompleteEvidence(t *testing.T) {
	// adr: 984 — skipped runs and unexercised or ambiguous phases cannot satisfy requirements.
	old := digestComparisonReceipt("old", true, 50)
	for _, missing := range []string{"phase", "duration", "requests", "fault", "assertion", "duplicate", "skipped", "fingerprint"} {
		t.Run(missing, func(t *testing.T) {
			current := digestComparisonReceipt("new", true, 50)
			policy := requiredCoverageTestPolicy(old)
			phase, kind, assertion := old.Load.ChaosPhases[0].Name, "threshold", "p95_ms"
			policy.RequiredCoverage[0].Match.Phase = &phase
			policy.RequiredCoverage[0].Match.Kind = &kind
			policy.RequiredCoverage[0].Match.Assertion = &assertion
			switch missing {
			case "phase":
				current.Load.ChaosPhases = nil
			case "duration":
				current.Load.ChaosPhases[0].ActualDurationMS = 0
			case "requests":
				current.Load.ChaosPhases[0].Requests = 0
			case "fault":
				current.Load.ChaosPhases[0].Chaos.Matches[0].Matches = 0
			case "assertion":
				current.Load.ChaosPhases[0].Thresholds = nil
			case "duplicate":
				current.Load.ChaosPhases = append(current.Load.ChaosPhases, current.Load.ChaosPhases[0])
			case "skipped":
				current.Status = "skipped"
			case "fingerprint":
				current.Replay = nil
			}
			results := evaluateTestCampaignRequiredCoverage(policy, []testRunReceipt{old}, []testRunReceipt{current})
			for _, result := range results {
				if result.Passed {
					t.Fatal("incomplete evidence satisfied the requirement")
				}
			}
		})
	}
}

func TestRequiredCoverageNeverPoolsConfigurationsForSamples(t *testing.T) {
	// adr: 984 — each exact configuration/context satisfies its own evidence minimum.
	a := digestComparisonReceipt("a", true, 50)
	b := digestComparisonReceipt("b", true, 50)
	b.Replay.ConfigurationSHA256 = strings.Repeat("b", 64)
	policy := requiredCoverageTestPolicy(a)
	policy.RequiredCoverage[0].MinExecutedRuns = 2
	results := evaluateTestCampaignRequiredCoverage(policy, []testRunReceipt{a, b}, []testRunReceipt{a, b})
	if len(results) != 2 {
		t.Fatalf("got %d configuration results", len(results))
	}
	for _, result := range results {
		if result.Passed || result.Baseline.CoveredRuns != 1 || result.Current.CoveredRuns != 1 {
			t.Fatal("pooled distinct seeds to satisfy the minimum")
		}
	}
	results = evaluateTestCampaignRequiredCoverage(policy, []testRunReceipt{a, a}, []testRunReceipt{a, a})
	if len(results) != 1 || !results[0].Passed {
		t.Fatal("sufficient repetitions failed mandatory coverage")
	}
}

func TestRequiredCoverageStrictPolicyParsing(t *testing.T) {
	// adr: 984 — malformed explicit requirements fail before policy evaluation.
	prefix := "schema_version: 1\nmin_executed_runs: 1\non_not_comparable: ignore\non_insufficient_samples: ignore\nrequired_coverage:\n"
	valid := "  - name: critical\n    match: {scenario: checkout, phase: recovery, kind: threshold, assertion: p95_ms}\n    min_executed_runs: 3\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	for _, body := range []string{
		strings.Replace(valid, "min_executed_runs: 3", "min_executed_runs: 0", 1),
		strings.Replace(valid, "scenario: checkout, ", "", 1),
		strings.Replace(valid, "kind: threshold, ", "", 1),
		strings.Replace(valid, "kind: threshold", "kind: mystery", 1),
		strings.Replace(valid, "scenario: checkout", "scenario: checkout, configuration_sha256: abc", 1),
		valid + valid,
	} {
		if err := os.WriteFile(path, []byte(prefix+body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readTestCampaignPolicy(path); err == nil {
			t.Fatal("invalid mandatory coverage accepted")
		}
	}
	if err := os.WriteFile(path, []byte(prefix+valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTestCampaignPolicy(path); err != nil {
		t.Fatal(err)
	}
}
