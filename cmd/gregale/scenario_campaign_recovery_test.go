package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func campaignRecoveryTestReceipt(scenario testScenario, id string) testRunReceipt {
	r := sourceRevisionTestReceipt(id, true, 50)
	r.Scenario, r.Profile, r.Engine = "checkout", "warm", "real-vm"
	r.ConfigurationSHA256 = newTestReplayManifest("", "", "", scenario).ConfigurationSHA256
	r.StartedAt = time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	r.FinishedAt = r.StartedAt.Add(time.Second)
	return r
}

func TestCampaignRecoveryAdoptsFinalizedPartialEvidenceWithoutInventingRuns(t *testing.T) {
	// adr: 984 — only verified executed leaves are adopted; skipped work remains a coverage gap.
	scenario := testScenario{Project: "checkout", Command: []string{"true"}, Resilience: &testResilienceSpec{Runs: 2}}
	executed := campaignRecoveryTestReceipt(scenario, "executed")
	skipped := executed
	skipped.Status, skipped.SkipReason, skipped.RunID, skipped.SourceRevision = "skipped", "interrupted before starting", "", nil
	root := testRunReceipt{Scenario: "checkout", Profile: "warm", Engine: "real-vm", Status: "failed",
		StartedAt: executed.StartedAt, FinishedAt: executed.FinishedAt,
		Resilience: &testResilienceEvidence{Runs: 2, History: []testRunReceipt{executed, skipped}}}
	path := filepath.Join(t.TempDir(), "partial.json")
	if err := writeTestCampaignJSON(path, []testRunReceipt{root}); err != nil {
		t.Fatal(err)
	}
	batch := testCampaignCostBatch{ID: "a", Scenario: "checkout", Profile: "warm", Side: "current", Repetitions: 1}
	checkpoint := testCampaignCheckpoint{PlanSHA256: strings.Repeat("c", 64), Side: "current", Revision: strings.Repeat("a", 40)}
	result, adopted := inspectTestCampaignRecoveryReport(context.Background(), path, batch, scenario, checkpoint, testCampaignCheckpointBatch{Status: "uncertain"}, nil)
	if result.Status != "partially_recovered" || result.AdoptedRuns != 1 || result.SkippedRuns != 1 || len(adopted) != 1 {
		t.Fatalf("partial evidence was lost or inflated: %+v", result)
	}
	var recovered testRunReceipt
	if err := json.Unmarshal(adopted[0], &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.RunID != executed.RunID || len(recovered.CampaignRecovery) != 1 ||
		recovered.CampaignRecovery[0].ReceiptPath != "roots/0/history/0" || recovered.SourceRevision == nil {
		t.Fatalf("recovered evidence lost identity or lineage: %+v", recovered)
	}
	// A report with no completion timestamp is not proof that cleanup finished.
	root.FinishedAt = time.Time{}
	if err := writeTestCampaignJSON(path, []testRunReceipt{root}); err != nil {
		t.Fatal(err)
	}
	result, _ = inspectTestCampaignRecoveryReport(context.Background(), path, batch, scenario, checkpoint, testCampaignCheckpointBatch{Status: "running"}, nil)
	if result.Status != "blocked" {
		t.Fatal("unfinished batch permitted continuation")
	}
}

func TestCampaignRecoveryBlocksMissingTamperedAndCleanupFailedEvidence(t *testing.T) {
	// adr: 984 — uncertainty and recorded cleanup failures cannot be silently waived.
	scenario := testScenario{Project: "checkout", Command: []string{"true"}}
	batch := testCampaignCostBatch{ID: "a", Scenario: "checkout", Profile: "warm", Side: "current", Repetitions: 1}
	checkpoint := testCampaignCheckpoint{PlanSHA256: strings.Repeat("c", 64), Side: "current", Revision: strings.Repeat("a", 40)}
	path := filepath.Join(t.TempDir(), "report.json")
	result, _ := inspectTestCampaignRecoveryReport(context.Background(), path, batch, scenario, checkpoint, testCampaignCheckpointBatch{Status: "running"}, nil)
	if result.Status != "blocked" {
		t.Fatal("missing report adopted")
	}
	r := campaignRecoveryTestReceipt(scenario, "run-a")
	if err := writeTestCampaignJSON(path, []testRunReceipt{r}); err != nil {
		t.Fatal(err)
	}
	result, adopted := inspectTestCampaignRecoveryReport(context.Background(), path, batch, scenario, checkpoint, testCampaignCheckpointBatch{Status: "done", ReportSHA256: "wrong"}, nil)
	if result.Status != "blocked" || len(adopted) != 0 {
		t.Fatal("changed completed evidence adopted")
	}
	r.Status, r.CleanupError = "failed", "cleanup did not finish"
	if err := writeTestCampaignJSON(path, []testRunReceipt{r}); err != nil {
		t.Fatal(err)
	}
	result, _ = inspectTestCampaignRecoveryReport(context.Background(), path, batch, scenario, checkpoint, testCampaignCheckpointBatch{Status: "running"}, nil)
	if result.Status != "blocked" || result.Cleanup != "recorded_failure" {
		t.Fatal("cleanup failure waived")
	}
}

func TestCampaignRecoveryLiveInspectionOnlyReadsKnownSessions(t *testing.T) {
	// adr: 984 — live checks never provision, renew or destroy resources.
	scenario := testScenario{Project: "checkout", Command: []string{"true"}, Services: map[string]testService{"database": {Fixture: "postgres"}}}
	r := campaignRecoveryTestReceipt(scenario, strings.Repeat("1", 32))
	for _, status := range []int{http.StatusNotFound, http.StatusOK, http.StatusServiceUnavailable} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			calls++
			if req.Method != http.MethodGet || req.URL.Query().Get("workspace_id") != r.RunID {
				t.Error("unexpected mutating or unscoped cleanup request")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("{}"))
		}))
		observations, clean := testCampaignLiveCleanup(context.Background(), NewClient(server.URL, "test-token"), scenario, []testRunReceipt{r, r})
		server.Close()
		if calls != 2 || len(observations) != 2 || clean != (status == http.StatusNotFound) {
			t.Fatalf("incorrect read-only cleanup result: status=%d calls=%d clean=%v %+v", status, calls, clean, observations)
		}
	}
}

func TestCampaignRecoveryContinuationHasHardZeroBudgetAndSealBinding(t *testing.T) {
	// adr: 984 — recovery cannot reset exhausted budgets or detach from its parent.
	parent, scenarios, policy := campaignRunnerTestPlan(t, 2)
	parentDir := t.TempDir()
	checkpoint := testCampaignCheckpoint{SchemaVersion: 1, PlanSHA256: testCampaignJSONHash(parent), Side: "current",
		Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{"a": {Status: "running"}}}
	unit := parent.Batches[0].Estimated
	reconciliation := testCampaignReconciliation{SchemaVersion: 1, Ready: true, Side: "current", Revision: checkpoint.Revision,
		ParentPlanSHA256: checkpoint.PlanSHA256, CheckpointSHA256: testCampaignJSONHash(checkpoint),
		Allocation: unit, Reserved: unit, Remaining: testCampaignPlanBudget{}}
	plan, err := buildTestCampaignContinuation(parent, checkpoint, reconciliation, policy, scenarios, "manifest.yaml", parentDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Batches) != 0 || plan.Costs.Estimated.Runs != 0 {
		t.Fatal("zero remaining budget became unlimited")
	}
	if err := validateTestCampaignRecoverySeal(plan); err == nil {
		t.Fatal("unpublished continuation allowed execution")
	}
	if err := writeTestCampaignJSON(filepath.Join(parentDir, "checkpoint.json"), checkpoint); err != nil {
		t.Fatal(err)
	}
	seal := testCampaignRecoverySeal{SchemaVersion: 1, ParentPlanSHA256: checkpoint.PlanSHA256, CheckpointSHA256: testCampaignJSONHash(checkpoint),
		ReconciliationSHA256: testCampaignJSONHash(reconciliation), ContinuationSHA256: testCampaignJSONHash(plan)}
	if err := writeTestCampaignJSON(filepath.Join(parentDir, testCampaignRecoverySealName), seal); err != nil {
		t.Fatal(err)
	}
	if err := validateTestCampaignRecoverySeal(plan); err != nil {
		t.Fatal(err)
	}
	plan.Batches = []testCampaignCostBatch{parent.Batches[0]}
	plan.Costs.Estimated = unit
	if err := validateTestCampaignRunnable(plan, scenarios, policy); err == nil {
		t.Fatal("hard zero budget bypassed")
	}
	if err := validateTestCampaignRecoverySeal(plan); err == nil {
		t.Fatal("modified sealed plan accepted")
	}
}

func TestCampaignReconcilePublishesEvidenceAndSealsOriginalScheduler(t *testing.T) {
	// adr: 984 — adoption links exact original bytes and prevents two schedulers spending the same allocation.
	root := t.TempDir()
	manifestPath, policyPath := filepath.Join(root, "manifest.yaml"), filepath.Join(root, "policy.yaml")
	manifestText := "version: 1\nscenarios:\n  checkout:\n    project: checkout\n    source: .\n    command: [must-never-run]\n"
	policyText := "schema_version: 1\nmin_executed_runs: 1\non_not_comparable: ignore\non_insufficient_samples: ignore\nrequired_coverage:\n  - name: checkout\n    match: {scenario: checkout, profile: warm}\n    min_executed_runs: 3\n"
	if err := os.WriteFile(manifestPath, []byte(manifestText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, []byte(policyText), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readTestManifestDocument(manifestPath, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil {
		t.Fatal(err)
	}
	policy, err := readTestCampaignPolicy(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	parent, _, _ := campaignRunnerTestPlan(t, 3)
	parent.Execution.ManifestSHA256, parent.Execution.PolicySHA256 = testCampaignJSONHash(manifest.Scenarios), testCampaignJSONHash(policy)
	originalDir := filepath.Join(root, "campaign")
	if err := os.Mkdir(originalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	checkpoint := testCampaignCheckpoint{SchemaVersion: 1, PlanSHA256: testCampaignJSONHash(parent), Side: "current", Revision: strings.Repeat("a", 40),
		Batches: map[string]testCampaignCheckpointBatch{"a": {Status: "running"}}}
	planPath := filepath.Join(root, "plan.json")
	if err := writeTestCampaignJSON(planPath, parent); err != nil {
		t.Fatal(err)
	}
	checkpointPath := filepath.Join(originalDir, "checkpoint.json")
	if err := writeTestCampaignJSON(checkpointPath, checkpoint); err != nil {
		t.Fatal(err)
	}
	originalCheckpoint, _ := os.ReadFile(checkpointPath)
	r := campaignRecoveryTestReceipt(manifest.Scenarios["checkout"], "original-run")
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	fields["future_counter"] = json.RawMessage("9007199254740993")
	reportPath := filepath.Join(originalDir, "batch-a.json")
	if err := writeTestCampaignJSON(reportPath, []map[string]json.RawMessage{fields}); err != nil {
		t.Fatal(err)
	}
	originalReport, _ := os.ReadFile(reportPath)
	output := filepath.Join(root, "recovery")
	args := []string{"--plan", planPath, "--policy", policyPath, "--manifest", manifestPath, "--directory", originalDir, "--output-directory", output}
	if code := cmdTestCampaignReconcile(args); code != 0 {
		t.Fatalf("reconciliation returned %d", code)
	}
	var reconciliation testCampaignReconciliation
	if err := readTestCampaignJSON(filepath.Join(output, "reconciliation.json"), &reconciliation); err != nil {
		t.Fatal(err)
	}
	if !reconciliation.Ready || reconciliation.Reserved.Runs != 1 || reconciliation.Remaining.Runs != 2 ||
		len(reconciliation.Batches) != 3 || reconciliation.Batches[0].AdoptedRuns != 1 {
		t.Fatalf("reservation or adoption incorrect: %+v", reconciliation)
	}
	var continuation testCampaignPolicyPlan
	if err := readTestCampaignJSON(filepath.Join(output, "continuation-plan.json"), &continuation); err != nil {
		t.Fatal(err)
	}
	if err := validateTestCampaignRunnable(continuation, manifest.Scenarios, policy); err != nil {
		t.Fatal(err)
	}
	if err := validateTestCampaignRecoverySeal(continuation); err != nil {
		t.Fatal(err)
	}
	if continuation.Costs.Estimated.Runs > 2 {
		t.Fatal("continuation exceeded unspent original allocation")
	}
	for _, batch := range continuation.Batches {
		if batch.Side != "current" {
			t.Fatal("continuation scheduled the other revision")
		}
	}
	recoveredBody, err := os.ReadFile(filepath.Join(output, "current.json"))
	if err != nil || !bytes.Contains(recoveredBody, []byte("9007199254740993")) {
		t.Fatal("unknown raw evidence was changed or lost")
	}
	checkpointAfter, _ := os.ReadFile(checkpointPath)
	reportAfter, _ := os.ReadFile(reportPath)
	if !bytes.Equal(originalCheckpoint, checkpointAfter) || !bytes.Equal(originalReport, reportAfter) {
		t.Fatal("original checkpoint or evidence was rewritten")
	}
	err = runTestCampaignBatches(context.Background(), parent, manifest.Scenarios, originalDir, &checkpoint, 1, func(testCampaignCostBatch) error { return nil },
		func(context.Context, testCampaignCostBatch, string) error {
			t.Fatal("sealed original scheduled work")
			return nil
		})
	if err == nil {
		t.Fatal("original scheduler ignored reconciliation seal")
	}
}

func TestCampaignRecoveryKeepsCompleteCampaignHierarchy(t *testing.T) {
	// adr: 984 — adoption preserves aggregate verdicts when the full report is verifiable.
	scenario := testScenario{Project: "checkout", Command: []string{"true"}, Resilience: &testResilienceSpec{Runs: 2}}
	first, second := campaignRecoveryTestReceipt(scenario, "one"), campaignRecoveryTestReceipt(scenario, "two")
	root := testRunReceipt{Scenario: "checkout", Profile: "warm", Engine: "real-vm", Status: "passed",
		StartedAt: first.StartedAt, FinishedAt: first.FinishedAt,
		Resilience: &testResilienceEvidence{Runs: 2, Passed: 2, History: []testRunReceipt{first, second}}}
	path := filepath.Join(t.TempDir(), "full.json")
	if err := writeTestCampaignJSON(path, []testRunReceipt{root}); err != nil {
		t.Fatal(err)
	}
	batch := testCampaignCostBatch{ID: "a", Scenario: "checkout", Profile: "warm", Side: "current", Repetitions: 1}
	checkpoint := testCampaignCheckpoint{PlanSHA256: strings.Repeat("c", 64), Side: "current", Revision: strings.Repeat("a", 40)}
	result, adopted := inspectTestCampaignRecoveryReport(context.Background(), path, batch, scenario, checkpoint, testCampaignCheckpointBatch{Status: "running"}, nil)
	if result.Status != "recovered" || result.AdoptedRuns != 2 || len(adopted) != 1 {
		t.Fatalf("complete hierarchy flattened: %+v", result)
	}
	var recovered testRunReceipt
	if err := json.Unmarshal(adopted[0], &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Resilience == nil || len(recovered.Resilience.History) != 2 || recovered.Status != "passed" || len(recovered.CampaignRecovery) != 1 {
		t.Fatal("aggregate evidence was changed")
	}
}
