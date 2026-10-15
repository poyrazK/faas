package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func completionReader(t *testing.T, status int) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || len(r.URL.Query().Get("workspace_id")) != 32 {
			t.Error("verification attempted mutation or unscoped lookup")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL, "test-token")
}

func TestRecoveryCompletionSessionAbsenceDoesNotCompleteMissingTest(t *testing.T) {
	// adr: 984 — resource cleanup cannot fabricate terminal assertion evidence.
	plan, cp, scenarios, options := recoveryAssessmentJournalFixture(t)
	assessment := buildTestCampaignRecoveryAssessment(context.Background(), plan, cp, testCampaignStatusReport{SnapshotStable: true, PolicyPassed: true},
		scenarios, options, nil, nil)
	report := buildTestCampaignRecoveryCompletion(context.Background(), assessment, plan, cp, scenarios, options.Directory, "snapshot", nil,
		completionReader(t, http.StatusNotFound), nil)
	if !report.ResourceEvidenceComplete || !report.KnownSessionsAbsent || report.TestExecutionComplete || report.RecoveryComplete {
		t.Fatalf("resource absence became test completion: %+v", report)
	}
	report = buildTestCampaignRecoveryCompletion(context.Background(), assessment, plan, cp, scenarios, options.Directory, "snapshot", nil,
		completionReader(t, http.StatusServiceUnavailable), nil)
	if report.KnownSessionsAbsent || report.RecoveryComplete {
		t.Fatal("lookup failure became resource absence")
	}
}

func TestRecoveryCompletionJournalAddsDiagnosticAndUncertainRuns(t *testing.T) {
	// adr: 984 — every known journal identity matters, including runs absent from primary receipts.
	plan, cp, scenarios, options := recoveryAssessmentJournalFixture(t)
	batch := plan.Batches[0]
	scenario := scenarios[batch.Scenario]
	assessment := buildTestCampaignRecoveryAssessment(context.Background(), plan, cp, testCampaignStatusReport{SnapshotStable: true, PolicyPassed: true},
		scenarios, options, nil, nil)
	writer, err := newTestCampaignRunJournal(testCampaignJournalDirectory(options.Directory, batch.ID), batch.Scenario, batch.Profile,
		strings.Repeat("c", 32), scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.update(scenario.Project, "", "creating"); err != nil {
		t.Fatal(err)
	}
	report := buildTestCampaignRecoveryCompletion(context.Background(), assessment, plan, cp, scenarios, options.Directory, "snapshot", nil,
		completionReader(t, http.StatusNotFound), nil)
	if report.ResourceEvidenceComplete || report.RecoveryComplete {
		t.Fatal("uncertain extra journal run was overlooked")
	}
	if err := writer.update(scenario.Project, "diagnostic-app", "provisioned"); err != nil {
		t.Fatal(err)
	}
	report = buildTestCampaignRecoveryCompletion(context.Background(), assessment, plan, cp, scenarios, options.Directory, "snapshot", nil,
		completionReader(t, http.StatusNotFound), nil)
	if len(report.Batches) != 1 || len(report.Batches[0].Resources) != 2 || report.LiveLookups != 2 || !report.KnownSessionsAbsent {
		t.Fatal("diagnostic run was omitted from fresh session checks")
	}
}

func TestCleanupObservationRequiresCheckpointEvidenceAndExactIdentities(t *testing.T) {
	// adr: 984 — provenance linkage must match the actual resource inventory and checkpoint.
	plan, cp, scenarios, options := recoveryAssessmentJournalFixture(t)
	batch := plan.Batches[0]
	targets, hash, err := readTestCampaignJournalTargets(options.Directory, testCampaignJSONHash(plan), cp, batch, scenarios[batch.Scenario])
	if err != nil {
		t.Fatal(err)
	}
	actions := []testCampaignCleanupAction{}
	for _, target := range targets {
		target.Status = "absent"
		actions = append(actions, target)
	}
	observation := testCampaignCleanupObservation{SchemaVersion: 2, PlanSHA256: testCampaignJSONHash(plan), CheckpointSHA256: testCampaignJSONHash(cp),
		BatchID: batch.ID, Side: cp.Side, Revision: cp.Revision, Evidence: "journal", JournalSHA256: "before", JournalAfterSHA256: hash,
		TargetsSHA256: testCampaignCleanupTargetsSHA256(targets), ObservedAt: time.Now().UTC(), OperationComplete: true, Executed: true, Actions: actions}
	path := filepath.Join(t.TempDir(), "cleanup.json")
	evidence := map[string]testCampaignCompletionEvidence{batch.ID: {journalHash: hash, journalTargets: targets}}
	check := func(want string) {
		t.Helper()
		if err := writeTestCampaignJSON(path, observation); err != nil {
			t.Fatal(err)
		}
		result := linkTestCampaignCleanupObservation(path, plan, cp, evidence)
		if result.Status != want {
			t.Fatalf("link=%s want %s", result.Status, want)
		}
	}
	check("linked")
	observation.Actions[0].AppID = "replacement"
	check("identity_mismatch")
	for _, target := range targets {
		observation.Actions[0].AppID = target.AppID
	}
	observation.JournalAfterSHA256 = "stale"
	check("stale_evidence")
	observation.JournalAfterSHA256 = hash
	observation.CheckpointSHA256 = "another-checkpoint"
	check("unrelated_checkpoint")
	observation.CheckpointSHA256 = testCampaignJSONHash(cp)
	observation.OperationComplete = false
	check("linked_partial")
	observation.SchemaVersion = 1
	check("legacy_unbound")
}

func TestRecoveryCompletionKeepsRecordedCleanupFailuresBlocking(t *testing.T) {
	// adr: 984 — absence of workload sessions cannot clear fixture or consumer cleanup errors.
	plan, cp, scenarios, options := recoveryAssessmentJournalFixture(t)
	batch := plan.Batches[0]
	receipt := campaignRecoveryTestReceipt(scenarios[batch.Scenario], strings.Repeat("b", 32))
	receipt.CleanupError = "fixture cleanup unfinished"
	receipt.WorkloadAppIDs = map[string]string{scenarios[batch.Scenario].Project: "recorded"}
	path := filepath.Join(options.Directory, "batch-"+batch.ID+".json")
	if err := writeTestCampaignJSON(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	_, hash, err := readTestBaselineReport(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cp.Batches[batch.ID] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
	status := testCampaignStatusReport{SnapshotStable: true, PolicyPassed: true, Batches: []testCampaignStatusBatch{{ID: batch.ID, Status: "completed"}}}
	assessment := buildTestCampaignRecoveryAssessment(context.Background(), plan, cp, status, scenarios, options, nil, nil)
	report := buildTestCampaignRecoveryCompletion(context.Background(), assessment, plan, cp, scenarios, options.Directory, "snapshot", nil,
		completionReader(t, http.StatusNotFound), nil)
	if !report.KnownSessionsAbsent || !report.Batches[0].RecordedCleanupFailure || report.RecoveryComplete {
		t.Fatal("recorded cleanup failure was waived")
	}
}

func TestRecoveryCompletionStableFullyEvidencedRunAndInvalidation(t *testing.T) {
	// adr: 984 — completion requires complete verified execution, policy, ownership and current absence.
	plan, cp, scenarios, options := recoveryAssessmentJournalFixture(t)
	batch := plan.Batches[0]
	receipt := campaignRecoveryTestReceipt(scenarios[batch.Scenario], strings.Repeat("b", 32))
	receipt.WorkloadAppIDs = map[string]string{scenarios[batch.Scenario].Project: "recorded"}
	path := filepath.Join(options.Directory, "batch-"+batch.ID+".json")
	if err := writeTestCampaignJSON(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	_, hash, err := readTestBaselineReport(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cp.Batches[batch.ID] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
	status := testCampaignStatusReport{SnapshotStable: true, PolicyPassed: true, Batches: []testCampaignStatusBatch{{ID: batch.ID, Status: "completed"}}}
	assessment := buildTestCampaignRecoveryAssessment(context.Background(), plan, cp, status, scenarios, options, nil, nil)
	report := buildTestCampaignRecoveryCompletion(context.Background(), assessment, plan, cp, scenarios, options.Directory, "snapshot", nil,
		completionReader(t, http.StatusNotFound), nil)
	if !report.RecoveryComplete {
		t.Fatalf("complete run not verified: %+v", report)
	}
	assessment.Completion = &report
	invalidateTestCampaignRecoveryAssessment(&assessment)
	if report.RecoveryComplete || report.KnownSessionsAbsent || report.SnapshotStable {
		t.Fatal("unstable snapshot retained a completed claim")
	}
}

func TestRecoveryCompletionCLIRequiresExplicitLiveChecksAndObservationsAreBounded(t *testing.T) {
	// adr: 984 — verification cannot silently authenticate or accept unbounded provenance input.
	if code := cmdTestCampaignRecover([]string{"--inspect", "--verify-completion", "--plan", "p", "--policy", "y", "--directory", "d"}); code == 0 {
		t.Fatal("verification without explicit live checks accepted")
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "cleanup-1.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := collectTestCampaignCleanupObservationPaths(directory, []string{filepath.Join(directory, "cleanup-1.json")})
	if err != nil || len(paths) != 1 {
		t.Fatal("observation paths were not deduplicated")
	}
	var values testCampaignCleanupObservationPaths
	if err := values.Set(""); err == nil {
		t.Fatal("empty observation path accepted")
	}
}
