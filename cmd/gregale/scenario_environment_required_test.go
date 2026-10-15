package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
)

func requiredEnvironmentTestPolicy(r testRunReceipt) testCampaignPolicy {
	policy := campaignPolicyTestSpec()
	policy.FailNewFailures, policy.MaxFrequencyIncrease = false, nil
	policy.NotComparable, policy.InsufficientSamples = "ignore", "ignore"
	scenario := r.Scenario
	policy.RequiredEnvironments = []testRequiredEnvironment{{
		Name: "checkout-environment", Match: testCampaignPolicySelector{Scenario: &scenario},
		Expect: testEnvironmentExpectation{Workloads: map[string]testWorkloadExpectation{"checkout": {}}},
	}}
	return policy
}

func TestRequiredEnvironmentRejectsMatchingButIncorrectSetup(t *testing.T) {
	// adr: 984 — equal baseline/current environments do not satisfy an incorrect resource setup.
	r := environmentTestReceipt("run", true)
	policy := requiredEnvironmentTestPolicy(r)
	memory := 256
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{RAMMB: &memory}
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{r}, []testRunReceipt{r}, "", "")
	if err != nil || result.Passed || len(result.Violations) != 1 || len(result.RequiredEnvironments) != 1 {
		t.Fatalf("wrong setup passed: %+v %v", result, err)
	}
	if result.Comparisons.Environments[0].Status != "compatible" ||
		result.Violations[0].Rule != "required_environment" || result.Violations[0].EnvironmentRequirement == nil {
		t.Fatal("compatibility was confused with explicit expectations")
	}
	issue := result.RequiredEnvironments[0].Current.Issues[0]
	if issue.Field != "workloads.checkout.ram_mb" || string(issue.Expected) != "256" ||
		string(issue.Observed) != "128" || issue.ReceiptIndex == nil || *issue.ReceiptIndex != 0 {
		t.Fatal("expected/observed evidence or receipt reference was lost")
	}
	body, err := marshalTestCampaignPolicyJUnit(policy, result)
	if err != nil {
		t.Fatal(err)
	}
	var suite testCampaignJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 {
		t.Fatal("JUnit omitted explicit environment failure")
	}
	var detail struct {
		Violation testCampaignPolicyViolation `json:"violation"`
	}
	if err := json.Unmarshal([]byte(suite.Cases[0].Failure.Body), &detail); err != nil ||
		detail.Violation.EnvironmentRequirement == nil ||
		detail.Violation.EnvironmentRequirement.Current.SatisfiedRuns != 0 {
		t.Fatal("JUnit lost requirement details")
	}
}

func TestRequiredEnvironmentHonorsFalseZeroAndEmptyPorts(t *testing.T) {
	// adr: 984 — explicit zero/false expectations are assertions, not omitted settings.
	r := environmentTestReceipt("run", true)
	policy := requiredEnvironmentTestPolicy(r)
	no, zero, ports, runtime := false, 0, []int{}, "go124"
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{
		Runtime: &runtime, Postgres: &no, FailFirst: &zero, MinInstances: &zero, TCPPorts: &ports,
	}
	results := evaluateTestRequiredEnvironments(policy, []testRunReceipt{r}, []testRunReceipt{r})
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("valid explicit false/zero fields failed: %+v", results)
	}
	workload := r.Environment.Workloads["checkout"]
	workload.Postgres = true
	r.Environment.Workloads["checkout"] = workload
	results = evaluateTestRequiredEnvironments(policy, []testRunReceipt{r}, []testRunReceipt{r})
	if results[0].Passed || results[0].Current.Issues[0].Field != "workloads.checkout.postgres" {
		t.Fatal("false expectation was skipped")
	}
}

func TestRequiredEnvironmentNeedsVersionsDependenciesAndExecutedContexts(t *testing.T) {
	// adr: 984 — unavailable metadata and missing scenarios cannot silently satisfy requirements.
	for _, missing := range []string{"platform", "runtime", "dependency", "both-contexts", "skipped"} {
		t.Run(missing, func(t *testing.T) {
			r := environmentTestReceipt("run", true)
			policy := requiredEnvironmentTestPolicy(r)
			switch missing {
			case "platform":
				policy.RequiredEnvironments[0].Expect.RequirePlatformVersion = true
				r.Environment.PlatformVersion = nil
			case "runtime":
				policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{RequireRuntimeVersion: true}
				workload := r.Environment.Workloads["checkout"]
				workload.RuntimeVersion = nil
				r.Environment.Workloads["checkout"] = workload
			case "dependency":
				policy.RequiredEnvironments[0].Expect.Workloads["database"] = testWorkloadExpectation{}
			case "both-contexts":
				scenario := "absent-in-both"
				policy.RequiredEnvironments[0].Match.Scenario = &scenario
			case "skipped":
				r.Status = "skipped"
			}
			result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{r}, []testRunReceipt{r}, "", "")
			if err != nil || result.Passed || len(result.RequiredEnvironments) != 1 || result.RequiredEnvironments[0].Passed {
				t.Fatalf("missing %s passed: %+v %v", missing, result, err)
			}
		})
	}
}

func TestRequiredEnvironmentChecksEveryExecutedRun(t *testing.T) {
	// adr: 984 — one compliant run cannot hide an incompatible repetition.
	good, bad := environmentTestReceipt("good", true), environmentTestReceipt("bad", true)
	policy := requiredEnvironmentTestPolicy(good)
	runtime := "go124"
	policy.RequiredEnvironments[0].Expect.Workloads["checkout"] = testWorkloadExpectation{Runtime: &runtime}
	workload := bad.Environment.Workloads["checkout"]
	workload.Runtime = "python312"
	bad.Environment.Workloads["checkout"] = workload
	rows := evaluateTestRequiredEnvironments(policy, []testRunReceipt{good}, []testRunReceipt{good, bad})
	if len(rows) != 1 || rows[0].Passed || rows[0].Current.ExecutedRuns != 2 || rows[0].Current.SatisfiedRuns != 1 ||
		rows[0].Current.Issues[0].RunID != "bad" {
		t.Fatal("compliant run masked a failing repetition")
	}
}

func TestRequiredEnvironmentStrictPolicyParsing(t *testing.T) {
	// adr: 984 — malformed expectations fail before comparing report evidence.
	prefix := "schema_version: 1\nmin_executed_runs: 1\non_not_comparable: ignore\non_insufficient_samples: ignore\nrequired_environments:\n"
	valid := "  - name: checkout\n    match: {scenario: checkout}\n    expect:\n      workloads:\n        checkout: {runtime: go124, ram_mb: 128, postgres: false, tcp_ports: []}\n"
	path := filepath.Join(t.TempDir(), "policy.yaml")
	invalid := []string{
		"  - name: empty\n    match: {scenario: checkout}\n    expect: {}\n",
		"  - name: phase\n    match: {scenario: checkout, phase: recovery}\n    expect: {require_platform_version: true}\n",
		"  - name: resources\n    match: {scenario: checkout}\n    expect: {workloads: {checkout: {ram_mb: 0}}}\n",
		"  - name: ports\n    match: {scenario: checkout}\n    expect: {workloads: {checkout: {tcp_ports: [5432, 5432]}}}\n",
		"  - name: unknown\n    match: {scenario: checkout}\n    expect: {secrets: forbidden}\n",
		valid + valid,
	}
	for _, body := range invalid {
		if err := os.WriteFile(path, []byte(prefix+body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readTestCampaignPolicy(path); err == nil {
			t.Fatal("invalid environment requirement accepted")
		}
	}
	if err := os.WriteFile(path, []byte(prefix+valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTestCampaignPolicy(path); err != nil {
		t.Fatal(err)
	}
}
