package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeMergeTestReport(t *testing.T, path string, rows any) []byte {
	t.Helper()
	body, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestReportMergePreservesEvidenceAndRecordsProvenance(t *testing.T) {
	// adr: 984 — batch aggregation preserves raw evidence and original receipt provenance.
	dir := t.TempDir()
	a, b, output := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), filepath.Join(dir, "merged.json")
	first, second := sourceRevisionTestReceipt("a", true, 50), sourceRevisionTestReceipt("b", true, 50)
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	fields["future_evidence"] = json.RawMessage(`{"counter":9007199254740993,"message":"<raw>&evidence"}`)
	source := writeMergeTestReport(t, a, []map[string]json.RawMessage{fields})
	writeMergeTestReport(t, b, []testRunReceipt{second})
	if code := cmdTestReport([]string{"merge", "--output", output, a, b}); code != 0 {
		t.Fatalf("merge returned %d", code)
	}
	rows, _, err := readTestBaselineReport(output, nil)
	if err != nil || len(rows) != 2 || rows[0].RunID != "a" || rows[1].RunID != "b" {
		t.Fatalf("merged output is not consumable: %+v %v", rows, err)
	}
	digest := sha256.Sum256(source)
	p := rows[0].ReportProvenance
	if len(p) != 1 || p[0].SourceSHA256 != hex.EncodeToString(digest[:]) || p[0].SourceName != "a.json" || p[0].ReceiptIndex != 0 {
		t.Fatal("source provenance was lost")
	}
	body, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	var future struct {
		Counter json.Number `json:"counter"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw[0]["future_evidence"], &future); err != nil || future.Counter.String() != "9007199254740993" || future.Message != "<raw>&evidence" {
		t.Fatal("unknown evidence changed or lost numeric precision")
	}
	policy := requiredCoverageTestPolicy(first)
	policy.RequiredCoverage[0].MinExecutedRuns = 2
	coverage := evaluateTestCampaignRequiredCoverage(policy, rows, rows)
	if len(coverage) != 1 || !coverage[0].Passed || coverage[0].Current.CoveredRuns != 2 {
		t.Fatal("merged distinct runs did not contribute exact coverage counts")
	}
}

func TestReportMergeRejectsNestedDuplicateIDsAndPreservesDestination(t *testing.T) {
	// adr: 984 — repeated leaves must never inflate campaign coverage.
	dir := t.TempDir()
	a, b, output := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), filepath.Join(dir, "merged.json")
	leaf := sourceRevisionTestReceipt("same-run", true, 50)
	parent := testRunReceipt{Scenario: leaf.Scenario, Profile: leaf.Profile, Engine: leaf.Engine, Status: "passed",
		Resilience: &testResilienceEvidence{History: []testRunReceipt{leaf}}}
	writeMergeTestReport(t, a, []testRunReceipt{parent})
	writeMergeTestReport(t, b, []testRunReceipt{leaf})
	if err := os.WriteFile(output, []byte("previous output"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeTestReports([]string{a, b}, output); err == nil {
		t.Fatal("nested duplicate run accepted")
	}
	body, err := os.ReadFile(output)
	if err != nil || string(body) != "previous output" {
		t.Fatal("failed merge changed existing output")
	}
}

func TestReportMergeRejectsAliasesAndDuplicateInputs(t *testing.T) {
	// adr: 984 — source reports survive output aliases and repeated batch selection.
	dir := t.TempDir()
	a, b, alias := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), filepath.Join(dir, "alias.json")
	original := writeMergeTestReport(t, a, []testRunReceipt{sourceRevisionTestReceipt("a", true, 50)})
	writeMergeTestReport(t, b, []testRunReceipt{sourceRevisionTestReceipt("b", true, 50)})
	if err := os.Link(a, alias); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{a, alias} {
		if err := mergeTestReports([]string{a, b}, output); err == nil {
			t.Fatal("input/output alias accepted")
		}
	}
	if err := mergeTestReports([]string{a, alias}, filepath.Join(dir, "merged.json")); err == nil {
		t.Fatal("same input content was merged twice")
	}
	body, err := os.ReadFile(a)
	if err != nil || !bytes.Equal(body, original) {
		t.Fatal("input changed")
	}
}

func TestReportMergeRejectsUnidentifiedRunsAndOverlappingHistory(t *testing.T) {
	// adr: 984 — identity and lineage checks prevent duplicate count inflation.
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json"), filepath.Join(dir, "c.json")
	first := sourceRevisionTestReceipt("a", true, 50)
	first.RunID = ""
	writeMergeTestReport(t, a, []testRunReceipt{first})
	writeMergeTestReport(t, b, []testRunReceipt{sourceRevisionTestReceipt("b", true, 50)})
	if err := mergeTestReports([]string{a, b}, c); err == nil {
		t.Fatal("unidentified executed receipt accepted")
	}
	first.RunID, first.Status = "", "skipped"
	writeMergeTestReport(t, a, []testRunReceipt{first})
	if err := mergeTestReports([]string{a, b}, c); err != nil {
		t.Fatal(err)
	}
	// Re-merging a skipped original must be caught by provenance, even without an ID.
	if err := mergeTestReports([]string{c, a}, filepath.Join(dir, "overlap.json")); err == nil {
		t.Fatal("overlapping skipped source receipt accepted")
	}
	d := filepath.Join(dir, "d.json")
	writeMergeTestReport(t, d, []testRunReceipt{sourceRevisionTestReceipt("d", true, 50)})
	output := filepath.Join(dir, "again.json")
	if err := mergeTestReports([]string{c, d}, output); err != nil {
		t.Fatal(err)
	}
	rows, _, err := readTestBaselineReport(output, nil)
	if err != nil || len(rows[0].ReportProvenance) != 2 {
		t.Fatal("second merge lost original provenance")
	}
}
