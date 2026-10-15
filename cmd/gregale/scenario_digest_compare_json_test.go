package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDigestComparisonJSONPreservesCoverageAndEvidence(t *testing.T) {
	// adr: 984 — consumers receive typed comparisons derived from raw receipts.
	old := digestComparisonReceipt("old", false, 200)
	current := digestComparisonReceipt("new", true, 50)
	body, err := marshalTestDigestComparison([]testRunReceipt{old}, []testRunReceipt{current}, "https://example.test/old.html", "https://example.test/new.html")
	if err != nil {
		t.Fatal(err)
	}
	var report testDigestComparisonReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || !report.Informational || report.Counts["no_longer_observed"] != 1 || len(report.Comparisons) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	row := report.Comparisons[0]
	if row.FrequencyDeltaPP == nil || *row.FrequencyDeltaPP != -100 || row.Baseline.Failures != 1 || row.Current.ExecutedRuns != 1 {
		t.Fatalf("missing typed measurements: %+v", row)
	}
	if row.Baseline.FirstSupportingIndex == nil || *row.Baseline.FirstSupportingIndex != 0 ||
		row.Baseline.EvidenceURL != "https://example.test/old.html#run-evidence-0" ||
		row.Current.FirstSupportingIndex != nil || len(row.Baseline.Occurrences) != 1 || row.Baseline.Occurrences[0].Replay == "" {
		t.Fatal("supporting evidence or replay was lost")
	}
	body, err = marshalTestDigestComparison([]testRunReceipt{old}, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	if report.Comparisons[0].FrequencyDeltaPP != nil || report.Comparisons[0].Reason == "" {
		t.Fatal("missing coverage acquired a numeric delta")
	}
}

func TestDigestComparisonJSONCLIProtectsFiles(t *testing.T) {
	// adr: 984 — both generated outputs must preserve original evidence.
	dir := t.TempDir()
	current, baseline := filepath.Join(dir, "current.json"), filepath.Join(dir, "baseline.json")
	markdown, output := filepath.Join(dir, "digest.md"), filepath.Join(dir, "comparison.json")
	body, err := json.Marshal([]testRunReceipt{digestComparisonReceipt("run", false, 200)})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{current, baseline} {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, destination := range []string{current, baseline, markdown} {
		if code := cmdTestDigest([]string{"--from", current, "--baseline", baseline, "--markdown", markdown, "--comparison-json", destination}); code == 0 {
			t.Fatalf("accepted aliased output %s", destination)
		}
	}
	if code := cmdTestDigest([]string{"--from", current, "--markdown", markdown, "--comparison-json", output}); code == 0 {
		t.Fatal("accepted comparison without a baseline")
	}
	if code := cmdTestDigest([]string{"--from", current, "--baseline", baseline, "--markdown", markdown, "--comparison-json", output, "--limit", "1"}); code != 0 {
		t.Fatalf("comparison returned %d", code)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var report testDigestComparisonReport
	if err := json.Unmarshal(encoded, &report); err != nil || len(report.Comparisons) != 1 {
		t.Fatalf("invalid generated report: %v", err)
	}
	alias := filepath.Join(dir, "alias.json")
	if err := os.Link(output, alias); err != nil {
		t.Fatal(err)
	}
	if err := testDigestDistinctOutputs(output, alias); err == nil {
		t.Fatal("hard-linked outputs accepted")
	}
}
