package main

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceRevisionTestReceipt(id string, passed bool, observed float64) testRunReceipt {
	r := digestComparisonReceipt(id, passed, observed)
	dirty := false
	r.SourceRevision = &testSourceRevision{SchemaVersion: 1, ExpectedWorkloads: []string{"checkout"},
		Workloads: map[string]testWorkloadSourceRevision{"checkout": {
			GitCommit: strings.Repeat("a", 40), TrackedChanges: &dirty, SourceSHA256: strings.Repeat("b", 64),
			DeploymentID: "deployment-" + id, Runtime: "go", Shape: "function", Handler: "main",
		}}}
	return r
}

func TestSourceRevisionIdentityUsesSourceAndBuildInputs(t *testing.T) {
	// adr: 984 — build instance IDs do not change identity; deployed inputs do.
	a, b := sourceRevisionTestReceipt("a", true, 50), sourceRevisionTestReceipt("b", true, 50)
	first, err := testSourceRevisionIdentity(a.SourceRevision)
	if err != nil {
		t.Fatal(err)
	}
	second, err := testSourceRevisionIdentity(b.SourceRevision)
	if err != nil || first != second {
		t.Fatal("different deployment IDs changed source identity")
	}
	for _, field := range []string{"git", "archive", "handler", "runtime", "dirty", "service"} {
		r := sourceRevisionTestReceipt("change", true, 50)
		workload := r.SourceRevision.Workloads["checkout"]
		switch field {
		case "git":
			workload.GitCommit = strings.Repeat("c", 40)
		case "archive":
			workload.SourceSHA256 = strings.Repeat("c", 64)
		case "handler":
			workload.Handler = "other"
		case "runtime":
			workload.Runtime = "node"
		case "dirty":
			dirty := true
			workload.TrackedChanges = &dirty
		case "service":
			r.SourceRevision.ExpectedWorkloads = append(r.SourceRevision.ExpectedWorkloads, "dependency")
			r.SourceRevision.Workloads["dependency"] = workload
		}
		r.SourceRevision.Workloads["checkout"] = workload
		hash, err := testSourceRevisionIdentity(r.SourceRevision)
		if err != nil || hash == first {
			t.Fatalf("%s did not change identity: %v", field, err)
		}
	}
	b.SourceRevision.ExpectedWorkloads = append(b.SourceRevision.ExpectedWorkloads, "missing")
	if _, err := testSourceRevisionIdentity(b.SourceRevision); err == nil {
		t.Fatal("incomplete workload metadata accepted")
	}
}

func TestReportMergeRejectsMixedRevisionsAndRecordsExplicitOverride(t *testing.T) {
	// adr: 984 — independent batches must not silently mix source revisions.
	dir := t.TempDir()
	aPath, bPath, output := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), filepath.Join(dir, "merged.json")
	a, b := sourceRevisionTestReceipt("a", true, 50), sourceRevisionTestReceipt("b", true, 50)
	workload := b.SourceRevision.Workloads["checkout"]
	workload.SourceSHA256 = strings.Repeat("c", 64)
	b.SourceRevision.Workloads["checkout"] = workload
	writeMergeTestReport(t, aPath, []testRunReceipt{a})
	writeMergeTestReport(t, bPath, []testRunReceipt{b})
	if err := os.WriteFile(output, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeTestReports([]string{aPath, bPath}, output); err == nil {
		t.Fatal("mixed source archives accepted")
	}
	body, err := os.ReadFile(output)
	if err != nil || string(body) != "previous" {
		t.Fatal("rejected merge changed destination")
	}
	if code := cmdTestReport([]string{"merge", "--allow-mixed-revisions", "--output", output, aPath, bPath}); code != 0 {
		t.Fatalf("explicit override returned %d", code)
	}
	rows, _, err := readTestBaselineReport(output, nil)
	if err != nil || !rows[0].ReportMergeRevisionOverride || !rows[1].ReportMergeRevisionOverride {
		t.Fatal("mixed merge was not explicitly marked")
	}
	summary := summarizeTestReportSources(rows)
	if !summary.MergeOverride || len(summary.Identities) != 2 || summary.UnknownRuns != 0 {
		t.Fatal("source summary concealed mixed identities")
	}
	// The override cannot bypass duplicate IDs.
	b.RunID = a.RunID
	writeMergeTestReport(t, bPath, []testRunReceipt{b})
	if err := mergeTestReports([]string{aPath, bPath}, output, true); err == nil {
		t.Fatal("source override bypassed duplicate run protection")
	}
}

func TestReportMergeUnknownSourceRequiresOverride(t *testing.T) {
	// adr: 984 — old evidence stays readable without being treated as proven identity.
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	writeMergeTestReport(t, a, []testRunReceipt{digestComparisonReceipt("a", true, 50)})
	writeMergeTestReport(t, b, []testRunReceipt{sourceRevisionTestReceipt("b", true, 50)})
	output := filepath.Join(dir, "merged.json")
	if err := mergeTestReports([]string{a, b}, output); err == nil {
		t.Fatal("unknown source identity passed strict merge")
	}
	if err := mergeTestReports([]string{a, b}, output, true); err != nil {
		t.Fatal(err)
	}
	rows, _, err := readTestBaselineReport(output, nil)
	if err != nil || summarizeTestReportSources(rows).UnknownRuns != 1 {
		t.Fatal("unknown identity was fabricated")
	}
}

func TestPolicyComparisonKeepsBaselineAndCurrentSourceRevisionsSeparate(t *testing.T) {
	// adr: 984 — source changes across comparison sides are intentional and visible.
	old, current := sourceRevisionTestReceipt("old", true, 50), sourceRevisionTestReceipt("current", false, 200)
	workload := current.SourceRevision.Workloads["checkout"]
	workload.GitCommit = strings.Repeat("c", 40)
	current.SourceRevision.Workloads["checkout"] = workload
	result, err := evaluateTestCampaignPolicy(campaignPolicyTestSpec(), []testRunReceipt{old}, []testRunReceipt{current}, "", "")
	if err != nil || len(result.Comparisons.BaselineSources.Identities) != 1 || len(result.Comparisons.CurrentSources.Identities) != 1 {
		t.Fatalf("source metadata missing from comparison: %v", err)
	}
	if result.Comparisons.BaselineSources.Identities[0].IdentitySHA256 == result.Comparisons.CurrentSources.Identities[0].IdentitySHA256 {
		t.Fatal("baseline source was blended with current source")
	}
	body, err := marshalTestCampaignPolicyJUnit(campaignPolicyTestSpec(), result)
	if err != nil {
		t.Fatal(err)
	}
	var suite testCampaignJUnitSuite
	if err := xml.Unmarshal(body, &suite); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		Before testReportSourceSummary `json:"baseline_sources"`
		After  testReportSourceSummary `json:"current_sources"`
	}
	if err := json.Unmarshal([]byte(suite.Cases[0].Failure.Body), &detail); err != nil ||
		len(detail.Before.Identities) != 1 || len(detail.After.Identities) != 1 {
		t.Fatal("JUnit did not retain separate revision provenance")
	}
}
