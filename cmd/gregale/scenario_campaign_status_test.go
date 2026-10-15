package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCampaignStatusCountsOnlyVerifiedCompletedEvidence(t *testing.T) {
	// adr: 984 — checkpoint claims and uncheckpointed reports cannot inflate actual coverage.
	plan, scenarios, policy := campaignRunnerTestPlan(t, 3)
	policy.RequiredCoverage[0].MinExecutedRuns = 2
	scenarioName := "checkout"
	policy.RequiredCoverage[0].Match.Scenario = &scenarioName
	directory := t.TempDir()
	first := campaignRecoveryTestReceipt(scenarios["checkout"], "first")
	second := campaignRecoveryTestReceipt(scenarios["checkout"], "second")
	firstPath := filepath.Join(directory, "batch-a.json")
	if err := writeTestCampaignJSON(firstPath, []testRunReceipt{first}); err != nil {
		t.Fatal(err)
	}
	_, hash, err := readTestBaselineReport(firstPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, PlanSHA256: testCampaignJSONHash(plan), Side: "current", Revision: strings.Repeat("a", 40),
		Batches: map[string]testCampaignCheckpointBatch{"a": {Status: "done", ReportSHA256: hash}, "b": {Status: "running"}}}
	if err := os.WriteFile(filepath.Join(directory, "runner.lock"), []byte("claimed lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := buildTestCampaignStatus(plan, &checkpoint, directory, "", policy, scenarios, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts["completed"] != 1 || report.Counts["running"] != 1 || report.Counts["pending"] != 1 ||
		report.Reserved.Runs != 2 || report.Remaining.Runs != 1 || report.Batches[1].Evidence != "checkpoint_only" {
		t.Fatalf("checkpoint claims were misclassified: %+v", report)
	}
	if err := writeTestCampaignJSON(filepath.Join(directory, "batch-b.json"), []testRunReceipt{second}); err != nil {
		t.Fatal(err)
	}
	report, err = buildTestCampaignStatus(plan, &checkpoint, directory, "", policy, scenarios, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Counts["awaiting_reconciliation"] != 1 || report.Batches[1].ExecutedRuns != 0 {
		t.Fatal("uncheckpointed report counted as completed")
	}
	found := false
	for _, gap := range report.CoverageGaps {
		if gap.Context.Profile == "warm" {
			found = true
			if gap.CurrentRemaining != 1 {
				t.Fatal("coverage includes unadopted execution evidence")
			}
		}
	}
	if !found {
		t.Fatal("required coverage gap disappeared")
	}
	checkpoint.Batches["a"] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: "changed"}
	report, err = buildTestCampaignStatus(plan, &checkpoint, directory, "", policy, scenarios, nil, nil)
	if err != nil || report.Counts["completed"] != 0 || report.Batches[0].Evidence == "verified_receipts" {
		t.Fatal("changed done report was trusted")
	}
}

func TestCampaignStatusHTMLIsStandaloneAndEscapesEvidence(t *testing.T) {
	// adr: 984 — offline reports cannot execute untrusted scenario or diagnostic text.
	report := testCampaignStatusReport{Counts: newTestCampaignStatusCounts(),
		Batches: []testCampaignStatusBatch{{ID: "a", Scenario: "<script>alert(1)</script>", Action: "<img src=x onerror=alert(2)>"}},
		Issues:  []testCampaignStatusIssue{{Message: "<svg onload=alert(3)>"}}}
	body, err := renderTestCampaignStatusHTML(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("<script>")) || bytes.Contains(body, []byte("<img src=x")) || bytes.Contains(body, []byte("<svg onload")) ||
		!bytes.Contains(body, []byte("&lt;script&gt;")) || !bytes.Contains(body, []byte("default-src 'none'")) {
		t.Fatal("HTML contains active evidence text or lacks offline restrictions")
	}
}

func TestCampaignStatusProtectsInputsAndCampaignDirectories(t *testing.T) {
	// adr: 984 — reporting cannot replace execution artifacts, even through aliases.
	root := t.TempDir()
	directory := filepath.Join(root, "campaign")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "plan.json")
	if err := os.WriteFile(input, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{input, filepath.Join(directory, "checkpoint.json")} {
		if err := protectTestCampaignStatusOutputs([]string{output}, []string{input}, []string{directory}); err == nil {
			t.Fatal("protected output accepted")
		}
	}
	alias := filepath.Join(root, "alias.json")
	if err := os.Link(input, alias); err != nil {
		t.Fatal(err)
	}
	if err := protectTestCampaignStatusOutputs([]string{alias}, []string{input}, nil); err == nil {
		t.Fatal("hard-linked input accepted")
	}
}

type campaignStatusTestFiles struct {
	root, directory, manifestPath, policyPath, planPath string
	plan                                                testCampaignPolicyPlan
	scenarios                                           map[string]testScenario
	policy                                              testCampaignPolicy
	checkpoint                                          testCampaignCheckpoint
}

func newCampaignStatusTestFiles(t *testing.T) campaignStatusTestFiles {
	t.Helper()
	f := campaignStatusTestFiles{root: t.TempDir()}
	f.directory = filepath.Join(f.root, "campaign")
	if err := os.Mkdir(f.directory, 0o700); err != nil {
		t.Fatal(err)
	}
	f.manifestPath, f.policyPath, f.planPath = filepath.Join(f.root, "manifest.yaml"), filepath.Join(f.root, "policy.yaml"), filepath.Join(f.root, "plan.json")
	manifest := "version: 1\nscenarios:\n  checkout:\n    project: checkout\n    source: .\n    command: [must-never-run]\n"
	policy := "schema_version: 1\nmin_executed_runs: 1\non_not_comparable: ignore\non_insufficient_samples: ignore\nrequired_coverage:\n  - name: checkout\n    match: {scenario: checkout, profile: warm}\n    min_executed_runs: 2\n"
	if err := os.WriteFile(f.manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.policyPath, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	document, _, err := readTestManifestDocument(f.manifestPath, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil {
		t.Fatal(err)
	}
	f.scenarios = document.Scenarios
	f.policy, err = readTestCampaignPolicy(f.policyPath)
	if err != nil {
		t.Fatal(err)
	}
	f.plan, _, _ = campaignRunnerTestPlan(t, 2)
	f.plan.Execution.ManifestSHA256, f.plan.Execution.PolicySHA256 = testCampaignJSONHash(f.scenarios), testCampaignJSONHash(f.policy)
	if err := writeTestCampaignJSON(f.planPath, f.plan); err != nil {
		t.Fatal(err)
	}
	r := campaignRecoveryTestReceipt(f.scenarios["checkout"], "completed-a")
	path := filepath.Join(f.directory, "batch-a.json")
	if err := writeTestCampaignJSON(path, []testRunReceipt{r}); err != nil {
		t.Fatal(err)
	}
	_, hash, err := readTestBaselineReport(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.checkpoint = testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, PlanSHA256: testCampaignJSONHash(f.plan), Side: "current", Revision: strings.Repeat("a", 40),
		Batches: map[string]testCampaignCheckpointBatch{"a": {Status: "done", ReportSHA256: hash}}}
	if err := writeTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), f.checkpoint); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCampaignStatusCLIExportsWithoutMutatingCampaign(t *testing.T) {
	// adr: 984 — unified status is an offline snapshot, not execution or reconciliation.
	f := newCampaignStatusTestFiles(t)
	checkpointPath, reportPath := filepath.Join(f.directory, "checkpoint.json"), filepath.Join(f.directory, "batch-a.json")
	checkpointBefore, _ := os.ReadFile(checkpointPath)
	reportBefore, _ := os.ReadFile(reportPath)
	jsonPath, htmlPath := filepath.Join(f.root, "status.json"), filepath.Join(f.root, "status.html")
	if code := cmdTestCampaignStatus([]string{"--plan", f.planPath, "--policy", f.policyPath, "--manifest", f.manifestPath, "--directory", f.directory, "--json", jsonPath, "--html", htmlPath}); code != 0 {
		t.Fatalf("status returned %d", code)
	}
	var report testCampaignStatusReport
	if err := readTestCampaignJSON(jsonPath, &report); err != nil {
		t.Fatal(err)
	}
	if !report.ReadOnly || !report.Informational || !report.SnapshotStable || report.Counts["completed"] != 1 || report.Counts["pending"] != 1 ||
		report.Reserved.Runs != 1 || report.Remaining.Runs != 1 {
		t.Fatalf("unexpected snapshot: %+v", report)
	}
	checkpointAfter, _ := os.ReadFile(checkpointPath)
	reportAfter, _ := os.ReadFile(reportPath)
	if !bytes.Equal(checkpointBefore, checkpointAfter) || !bytes.Equal(reportBefore, reportAfter) {
		t.Fatal("status rewrote campaign evidence")
	}
	if _, err := os.Stat(filepath.Join(f.directory, "runner.lock")); !os.IsNotExist(err) {
		t.Fatal("status acquired or left an execution lock")
	}
	if body, err := os.ReadFile(htmlPath); err != nil || !bytes.Contains(body, []byte("Gregale campaign status")) {
		t.Fatal("HTML export missing")
	}
}

func TestCampaignStatusVerifiesRecoveryBundleAndEarlierLinks(t *testing.T) {
	// adr: 984 — adoption counts require exact bundle/seal binding, and history is traced through retained checkpoints.
	f := newCampaignStatusTestFiles(t)
	bundle := filepath.Join(f.root, "recovery")
	if code := cmdTestCampaignReconcile([]string{"--plan", f.planPath, "--policy", f.policyPath, "--manifest", f.manifestPath, "--directory", f.directory, "--output-directory", bundle}); code != 0 {
		t.Fatalf("reconciliation setup returned %d", code)
	}
	report, err := buildTestCampaignStatus(f.plan, &f.checkpoint, f.directory, "", f.policy, f.scenarios, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyTestCampaignStatusRecovery(&report, f.plan, f.checkpoint, bundle, f.policy, f.scenarios); err != nil {
		t.Fatal(err)
	}
	if report.Counts["recovered"] != 1 || report.Reserved.Runs != 1 || report.Remaining.Runs != 1 {
		t.Fatalf("recovery evidence not reflected: %+v", report)
	}
	var continuation testCampaignPolicyPlan
	if err := readTestCampaignJSON(filepath.Join(bundle, "continuation-plan.json"), &continuation); err != nil {
		t.Fatal(err)
	}
	lineage, complete, issues := testCampaignStatusTrace(continuation)
	if !complete || len(lineage) != 1 || !lineage[0].Verified || len(issues) != 0 {
		t.Fatalf("history not traced: %+v %+v", lineage, issues)
	}
	path := filepath.Join(bundle, "current.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var roots []json.RawMessage
	if err := json.Unmarshal(original, &roots); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyTestCampaignStatusRecovery(&report, f.plan, f.checkpoint, bundle, f.policy, f.scenarios); err == nil {
		t.Fatal("modified recovered report trusted")
	}
}

func TestCampaignStatusTracesMultipleRecoveryParentsAndMarksLegacyBoundary(t *testing.T) {
	// adr: 984 — history follows bound checkpoint links without inventing missing ancestry.
	for _, version := range []int{0, 1} {
		rootPlan, _, _ := campaignRunnerTestPlan(t, 1)
		rootDir, parentDir := t.TempDir(), t.TempDir()
		rootCheckpoint := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: version, PlanSHA256: testCampaignJSONHash(rootPlan),
			Side: "current", Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{}}
		if err := writeTestCampaignJSON(filepath.Join(rootDir, "checkpoint.json"), rootCheckpoint); err != nil {
			t.Fatal(err)
		}
		parentPlan := rootPlan
		parentPlan.Recovery = &testCampaignRecoveryLink{ParentDirectory: rootDir, ParentPlanSHA256: rootCheckpoint.PlanSHA256,
			CheckpointSHA256: testCampaignJSONHash(rootCheckpoint), ReconciliationSHA256: strings.Repeat("b", 64),
			Side: "current", Revision: rootCheckpoint.Revision}
		rootSeal := testCampaignRecoverySeal{SchemaVersion: 1, ParentPlanSHA256: rootCheckpoint.PlanSHA256,
			CheckpointSHA256: testCampaignJSONHash(rootCheckpoint), ReconciliationSHA256: parentPlan.Recovery.ReconciliationSHA256,
			ContinuationSHA256: testCampaignJSONHash(parentPlan)}
		if err := writeTestCampaignJSON(filepath.Join(rootDir, testCampaignRecoverySealName), rootSeal); err != nil {
			t.Fatal(err)
		}
		parentCheckpoint := testCampaignCheckpoint{SchemaVersion: 1, LineageVersion: 1, Recovery: parentPlan.Recovery,
			PlanSHA256: testCampaignJSONHash(parentPlan), Side: "current", Revision: rootCheckpoint.Revision, Batches: map[string]testCampaignCheckpointBatch{}}
		if err := writeTestCampaignJSON(filepath.Join(parentDir, "checkpoint.json"), parentCheckpoint); err != nil {
			t.Fatal(err)
		}
		childPlan := parentPlan
		childPlan.Recovery = &testCampaignRecoveryLink{ParentDirectory: parentDir, ParentPlanSHA256: parentCheckpoint.PlanSHA256,
			CheckpointSHA256: testCampaignJSONHash(parentCheckpoint), ReconciliationSHA256: strings.Repeat("c", 64),
			Side: "current", Revision: parentCheckpoint.Revision}
		parentSeal := testCampaignRecoverySeal{SchemaVersion: 1, ParentPlanSHA256: parentCheckpoint.PlanSHA256,
			CheckpointSHA256: testCampaignJSONHash(parentCheckpoint), ReconciliationSHA256: childPlan.Recovery.ReconciliationSHA256,
			ContinuationSHA256: testCampaignJSONHash(childPlan)}
		if err := writeTestCampaignJSON(filepath.Join(parentDir, testCampaignRecoverySealName), parentSeal); err != nil {
			t.Fatal(err)
		}
		rows, complete, issues := testCampaignStatusTrace(childPlan)
		if len(rows) != 2 || !rows[0].Verified || !rows[1].Verified || complete != (version == 1) {
			t.Fatalf("wrong ancestry: version=%d rows=%+v issues=%+v", version, rows, issues)
		}
		if version == 0 && (len(issues) != 1 || issues[0].Code != "legacy_lineage") {
			t.Fatal("older checkpoint was reported as a known root")
		}
	}
}
