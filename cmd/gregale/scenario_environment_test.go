package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func environmentTestReceipt(id string, passed bool) testRunReceipt {
	r := digestComparisonReceipt(id, passed, 200)
	platform, runtime := "platform-v1", "go1.24.9"
	r.Environment = &testEnvironmentSnapshot{SchemaVersion: 1, PlatformVersion: &platform,
		Workloads: map[string]testWorkloadEnvironment{"checkout": {
			Type: "function", Runtime: "go124", RuntimeVersion: &runtime,
			RAMMB: 128, VCPU: 1, CPUMillicores: 250, MaxConcurrency: 5, ConcurrencyPerVM: 5, TCPPorts: []int{},
		}}}
	return r
}

func TestEnvironmentCaptureExcludesSecretsAndTransientIDs(t *testing.T) {
	// adr: 984 — environment comparison records only explicit nonsecret resource fields.
	app := api.AppResponse{ID: "transient-id", Slug: "transient-slug", URL: "https://user:password@host.test",
		Type: "function", Runtime: "go124", RAMMB: 128, VCPU: 1, CPUMillicores: 250, MaxConcurrency: 5, ConcurrencyPerVMBound: 5}
	spec := testService{Postgres: true, Fixture: "tcp-relay", TCPPorts: []int{5432, 1234},
		Secrets: map[string]string{"token": "secret-value"}, Upstream: "tcp://private-user:private-password@host.test:5432"}
	observed := testEnvironmentFromApp(app, spec)
	body, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"transient", "password", "secret-value", "host.test", "token"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("captured forbidden metadata %q", forbidden)
		}
	}
	if observed.RAMMB != 128 || !observed.Postgres || observed.RuntimeVersion != nil || observed.TCPPorts[0] != 1234 || spec.TCPPorts[0] != 5432 {
		t.Fatal("resource capture or unknown version semantics changed")
	}
}

func TestEnvironmentCompatibilityDetectsResourcesAndUnknownVersions(t *testing.T) {
	// adr: 984 — observed mismatches and missing version information remain distinguishable.
	old, current := environmentTestReceipt("old", true), environmentTestReceipt("new", true)
	rows := compareTestEnvironments([]testRunReceipt{old}, []testRunReceipt{current})
	if len(rows) != 1 || rows[0].Status != "compatible" {
		t.Fatalf("matching environment not recognized: %+v", rows)
	}
	workload := current.Environment.Workloads["checkout"]
	workload.RAMMB = 256
	current.Environment.Workloads["checkout"] = workload
	rows = compareTestEnvironments([]testRunReceipt{old}, []testRunReceipt{current})
	if rows[0].Status != "incompatible" || len(rows[0].Differences) != 1 || rows[0].Differences[0].Field != "workloads.checkout.ram_mb" {
		t.Fatalf("memory drift was hidden: %+v", rows)
	}
	old.Environment.PlatformVersion, current.Environment.PlatformVersion = nil, nil
	workload.RAMMB = 128
	current.Environment.Workloads["checkout"] = workload
	rows = compareTestEnvironments([]testRunReceipt{old}, []testRunReceipt{current})
	if rows[0].Status != "unknown" || len(rows[0].UnknownFields) == 0 {
		t.Fatal("absent platform versions were invented")
	}
	rows = compareTestEnvironments([]testRunReceipt{old, current}, []testRunReceipt{current})
	if rows[0].Status != "unknown" {
		t.Fatal("equivalent partial snapshots changed compatibility semantics")
	}
	current.Environment = nil
	rows = compareTestEnvironments([]testRunReceipt{old}, []testRunReceipt{current})
	if rows[0].Status != "unknown" || rows[0].Current.UnknownRuns != 1 {
		t.Fatal("legacy report was treated as a proven match")
	}
}

func TestEnvironmentPolicyRejectsAndMarksNotComparable(t *testing.T) {
	// adr: 984 — environment incompatibility cannot be misreported as an application regression.
	old, current := environmentTestReceipt("old", true), environmentTestReceipt("new", false)
	workload := current.Environment.Workloads["checkout"]
	workload.RAMMB = 256
	current.Environment.Workloads["checkout"] = workload
	policy := campaignPolicyTestSpec()
	policy.Environment = &testCampaignEnvironmentPolicy{OnMismatch: "fail", OnUnknown: "ignore"}
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || result.Passed || len(result.Violations) != 1 || result.Violations[0].Rule != "environment_mismatch" ||
		result.Violations[0].Environment == nil || result.Comparisons.Comparisons[0].Status != "not_comparable" {
		t.Fatalf("environment mismatch was not isolated: %+v %v", result, err)
	}
	body, err := marshalTestCampaignPolicyJUnit(policy, result)
	if err != nil {
		t.Fatal(err)
	}
	var suite testCampaignJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil || suite.Failures != 1 || !strings.Contains(suite.Cases[0].Failure.Body, "ram_mb") {
		t.Fatal("JUnit lost environment differences")
	}
	policy.Environment.OnMismatch, policy.NotComparable = "not_comparable", "ignore"
	result, err = evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || !result.Passed || result.Comparisons.Counts["not_comparable"] != 1 ||
		result.Comparisons.Comparisons[0].FrequencyDeltaPP != nil {
		t.Fatal("explicit incomparable handling used invalid frequency comparisons")
	}
}

func TestEnvironmentPolicyChecksPassingContextsAndScopedRules(t *testing.T) {
	// adr: 984 — passing campaigns still require the selected environment policy.
	old, current := environmentTestReceipt("old", true), environmentTestReceipt("new", true)
	current.Environment = nil
	policy := campaignPolicyTestSpec()
	policy.Environment = &testCampaignEnvironmentPolicy{OnMismatch: "fail", OnUnknown: "fail"}
	result, err := evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || result.Passed || len(result.Violations) != 1 || result.Violations[0].Rule != "environment_unknown" {
		t.Fatal("passing context bypassed environment checks")
	}
	scenario := old.Scenario
	policy.Rules = []testCampaignScopedRule{{Name: "legacy", Match: testCampaignPolicySelector{Scenario: &scenario},
		Environment: &testCampaignEnvironmentPolicy{OnMismatch: "fail", OnUnknown: "ignore"}}}
	result, err = evaluateTestCampaignPolicy(policy, []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || !result.Passed || result.EnvironmentDecisions[0].PolicyRule != "legacy" {
		t.Fatal("scoped environment handling was ignored")
	}
}

func TestEnvironmentPolicyStrictParsingAndLegacyDefaults(t *testing.T) {
	// adr: 984 — compatibility policies are explicit and old policies retain their behavior.
	if action := testCampaignEnvironmentAction(campaignPolicyTestSpec(), nil); action != "ignore" {
		t.Fatal("old policies acquired an implicit environment gate")
	}
	path := filepath.Join(t.TempDir(), "policy.yaml")
	prefix := "schema_version: 1\nmin_executed_runs: 1\non_not_comparable: fail\non_insufficient_samples: fail\n"
	for _, block := range []string{
		"environment: {on_mismatch: fail}\n",
		"environment: {on_mismatch: maybe, on_unknown: ignore}\n",
		"environment: {on_mismatch: fail, on_unknown: ignore, extra: true}\n",
	} {
		if err := os.WriteFile(path, []byte(prefix+block), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readTestCampaignPolicy(path); err == nil {
			t.Fatal("ambiguous environment policy accepted")
		}
	}
	if err := os.WriteFile(path, []byte(prefix+"environment: {on_mismatch: not_comparable, on_unknown: ignore}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTestCampaignPolicy(path); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentMixedObservedValuesRemainIncompatible(t *testing.T) {
	// adr: 984 — unknown extra receipts cannot conceal an observed resource mismatch.
	old, current := environmentTestReceipt("old", true), environmentTestReceipt("new", true)
	changed := environmentTestReceipt("changed", true)
	workload := changed.Environment.Workloads["checkout"]
	workload.CPUMillicores = 500
	changed.Environment.Workloads["checkout"] = workload
	unknown := environmentTestReceipt("unknown", true)
	unknown.Environment = nil
	rows := compareTestEnvironments([]testRunReceipt{old}, []testRunReceipt{current, changed, unknown})
	if len(rows) != 1 || rows[0].Status != "incompatible" || rows[0].Current.UnknownRuns != 1 || len(rows[0].Current.Variants) != 2 {
		t.Fatalf("observed drift was concealed: %+v", rows)
	}
}
