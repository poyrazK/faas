package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The server permits only scoped session GET/DELETE. Any accidental deployment,
// lease renewal, fixture command or unscoped cleanup fails the acceptance test.
type campaignRecoveryAcceptanceAPI struct {
	mu                     sync.Mutex
	sessions               map[string]string
	reads, deletes         int
	failLookup, failDelete bool
	deleteStarted          chan struct{}
	deleteContinue         chan struct{}
}

func newCampaignRecoveryAcceptanceAPI(t *testing.T) *campaignRecoveryAcceptanceAPI {
	t.Helper()
	stub := &campaignRecoveryAcceptanceAPI{sessions: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runID := r.URL.Query().Get("workspace_id")
		if r.URL.Path != "/v1/dev/sessions/checkout" ||
			(runID != strings.Repeat("a", 32) && runID != strings.Repeat("b", 32)) ||
			r.Header.Get("Authorization") != "Bearer acceptance-token" {
			t.Errorf("unexpected acceptance API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stub.mu.Lock()
		defer stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			stub.reads++
			if stub.failLookup {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("{}"))
				return
			}
			id, ok := stub.sessions[runID]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("{}"))
				return
			}
			var response api.DevSessionResponse
			response.App.ID = id
			_ = json.NewEncoder(w).Encode(response)
		case http.MethodDelete:
			if stub.deleteStarted != nil {
				close(stub.deleteStarted)
				stub.deleteStarted = nil
				<-stub.deleteContinue
			}
			stub.deletes++
			if stub.failDelete {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("{}"))
				return
			}
			delete(stub.sessions, runID)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected mutation during recovery: %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "acceptance-token")
	return stub
}
func (s *campaignRecoveryAcceptanceAPI) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads, s.deletes
}

// Inject a worker crash at the native launcher seam after provisioning intent
// and returned identity persistence, but before any final report is published.
// This exercises the real scheduler/checkpoint path without starting a VM.
func interruptedCampaignRecoveryAcceptanceFixture(t *testing.T, stub *campaignRecoveryAcceptanceAPI, uncertain bool, baseline ...bool) campaignStatusTestFiles {
	t.Helper()
	f := newCampaignStatusTestFiles(t)
	if len(baseline) > 0 && baseline[0] {
		var receipts []testRunReceipt
		for _, id := range []string{strings.Repeat("c", 32), strings.Repeat("d", 32)} {
			receipts = append(receipts, campaignRecoveryTestReceipt(f.scenarios["checkout"], id))
		}
		path := filepath.Join(f.root, "baseline.json")
		if err := writeTestCampaignJSON(path, receipts); err != nil {
			t.Fatal(err)
		}
		_, hash, err := readTestBaselineReport(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.plan.Execution.BaselineSHA256 = hash
		if err := writeTestCampaignJSON(f.planPath, f.plan); err != nil {
			t.Fatal(err)
		}
		f.checkpoint.PlanSHA256 = testCampaignJSONHash(f.plan)
	}
	receipt := campaignRecoveryTestReceipt(f.scenarios["checkout"], strings.Repeat("a", 32))
	receipt.WorkloadAppIDs = map[string]string{"checkout": "app-a"}
	if err := writeTestCampaignJSON(filepath.Join(f.directory, "batch-a.json"), []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	_, hash, err := readTestBaselineReport(filepath.Join(f.directory, "batch-a.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.checkpoint.Batches["a"] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
	if err := writeTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), f.checkpoint); err != nil {
		t.Fatal(err)
	}
	launches := 0
	err = runTestCampaignBatches(context.Background(), f.plan, f.scenarios, f.directory, &f.checkpoint, 1,
		func(testCampaignCostBatch) error { return nil },
		func(_ context.Context, batch testCampaignCostBatch, directory string) error {
			launches++
			if batch.ID != "b" {
				return errors.New("completed batch was relaunched")
			}
			if err := os.WriteFile(filepath.Join(directory, "batch-b.log"), []byte("Injected worker exit before final report.\n"), 0o600); err != nil {
				return err
			}
			journal, err := prepareTestCampaignJournal(directory, batch)
			if err != nil {
				return err
			}
			writer, err := newTestCampaignRunJournal(journal, batch.Scenario, batch.Profile, strings.Repeat("b", 32), f.scenarios[batch.Scenario])
			if err != nil {
				return err
			}
			if err := writer.update("checkout", "", "creating"); err != nil {
				return err
			}
			stub.mu.Lock()
			stub.sessions[strings.Repeat("b", 32)] = "app-b"
			stub.mu.Unlock()
			if !uncertain {
				if err := writer.update("checkout", "app-b", "provisioned"); err != nil {
					return err
				}
			}
			return errors.New("injected worker crash before final report")
		})
	if err == nil || launches != 1 || !f.checkpoint.Halted || f.checkpoint.Batches["b"].Status != "uncertain" {
		t.Fatalf("scheduler failed to retain interrupted work: launches=%d checkpoint=%+v error=%v", launches, f.checkpoint, err)
	}
	if _, err := os.Stat(filepath.Join(f.directory, "batch-b.json")); !os.IsNotExist(err) {
		t.Fatal("crash fixture manufactured a final report")
	}
	return f
}

type campaignRecoveryAcceptanceRecipient struct {
	root, work, index, plan, policy string
}

func importCampaignRecoveryAcceptance(t *testing.T, from, root, claims string, active bool) campaignRecoveryAcceptanceRecipient {
	t.Helper()
	args := []string{"import", "--from", from, "--directory", root}
	if active {
		args = append(args, "--activate", "--claim-directory", claims)
	}
	if code := cmdTestCampaignBundle(args); code != 0 {
		t.Fatalf("bundle import failed: %d", code)
	}
	return campaignRecoveryAcceptanceRecipient{root, filepath.Join(root, "work"), filepath.Join(root, "bundle-index.json"),
		filepath.Join(root, "artifacts", "plan.json"), filepath.Join(root, "artifacts", "policy.yaml")}
}
func exportCampaignRecoveryAcceptance(t *testing.T, plan, policy, manifest, directory, handoff, output string, extra ...string) {
	t.Helper()
	args := []string{"--plan", plan, "--policy", policy, "--manifest", manifest, "--directory", directory, "--output", output}
	if handoff != "" {
		args = append(args, "--handoff", handoff)
	}
	args = append(args, extra...)
	if code := cmdTestCampaignBundleExport(args); code != 0 {
		t.Fatalf("bundle export failed: %d", code)
	}
}
func (r campaignRecoveryAcceptanceRecipient) args(manifest string) []string {
	return []string{"--plan", r.plan, "--policy", r.policy, "--manifest", manifest, "--directory", r.work, "--handoff", r.index}
}
func verifyCampaignRecoveryAcceptance(t *testing.T, r campaignRecoveryAcceptanceRecipient, manifest, output string, extra ...string) testCampaignRecoveryAssessment {
	t.Helper()
	args := append([]string{"--inspect", "--verify-completion", "--check-cleanup"}, r.args(manifest)...)
	args = append(args, "--json", output)
	if path := filepath.Join(r.root, "artifacts", "baseline.json"); acceptanceFileExists(path) {
		args = append(args, "--baseline", path)
	}
	args = append(args, extra...)
	if code := cmdTestCampaignRecover(args); code != 0 {
		t.Fatalf("completion assessment failed: %d", code)
	}
	var result testCampaignRecoveryAssessment
	if err := readTestCampaignJSON(output, &result); err != nil {
		t.Fatal(err)
	}
	if result.Completion == nil || !result.SnapshotStable || !result.Completion.SnapshotStable {
		t.Fatal("completion assessment missing or unstable")
	}
	return result
}

func TestCampaignRecoveryAcceptanceCrashHandoffCleanupAndReconciliation(t *testing.T) {
	// adr: 984 — transport, ownership, actual session cleanup and completion must agree without fabricating execution evidence.
	stub := newCampaignRecoveryAcceptanceAPI(t)
	f := interruptedCampaignRecoveryAcceptanceFixture(t, stub, false)
	sourceReceipt, err := os.ReadFile(filepath.Join(f.directory, "batch-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	firstBundle := filepath.Join(f.root, "first.json")
	exportCampaignRecoveryAcceptance(t, f.planPath, f.policyPath, f.manifestPath, f.directory, "", firstBundle)
	launches := 0
	if err := runTestCampaignBatches(context.Background(), f.plan, f.scenarios, f.directory, &f.checkpoint, 1,
		func(testCampaignCostBatch) error { return nil }, func(context.Context, testCampaignCostBatch, string) error { launches++; return nil }); err == nil || launches != 0 {
		t.Fatal("frozen source resumed scheduling")
	}
	claims := filepath.Join(f.root, "claims")
	if err := os.Mkdir(claims, 0o700); err != nil {
		t.Fatal(err)
	}
	inspection := importCampaignRecoveryAcceptance(t, firstBundle, filepath.Join(f.root, "inspection"), "", false)
	cleanup := append(inspection.args(f.manifestPath), "--batch", "b", "--journal", "--execute")
	if code := cmdTestCampaignControl("cleanup", cleanup); code == 0 {
		t.Fatal("inspection-only recipient deleted sessions")
	}
	if _, deletes := stub.counts(); deletes != 0 {
		t.Fatal("inspection-only recipient reached deletion API")
	}
	recipient := importCampaignRecoveryAcceptance(t, firstBundle, filepath.Join(f.root, "recipient"), claims, true)
	if code := cmdTestCampaignBundle([]string{"import", "--from", firstBundle, "--directory", filepath.Join(f.root, "duplicate"),
		"--activate", "--claim-directory", claims}); code == 0 {
		t.Fatal("duplicate activated recipient acquired the same transfer")
	}
	if err := os.Rename(f.directory, f.directory+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	originalJournal := filepath.Join(recipient.root, "artifacts", "nodes", "0", "batch-b.journal", strings.Repeat("b", 32)+".json")
	immutableBefore, err := os.ReadFile(originalJournal)
	if err != nil {
		t.Fatal(err)
	}
	cleanup = append(recipient.args(f.manifestPath), "--batch", "b", "--journal", "--execute")
	if code := cmdTestCampaignControl("cleanup", cleanup); code != 0 {
		t.Fatalf("verified journal cleanup failed: %d", code)
	}
	if _, deletes := stub.counts(); deletes != 1 {
		t.Fatalf("expected exactly one scoped deletion, got %d", deletes)
	}
	immutableAfter, err := os.ReadFile(originalJournal)
	if err != nil || !bytes.Equal(immutableBefore, immutableAfter) {
		t.Fatal("cleanup rewrote immutable imported evidence")
	}
	observations, err := collectTestCampaignCleanupObservationPaths(recipient.work, nil)
	if err != nil || len(observations) != 1 {
		t.Fatal("bound cleanup observation missing")
	}
	var observation testCampaignCleanupObservation
	if err := readTestCampaignJSON(observations[0], &observation); err != nil {
		t.Fatal(err)
	}
	if observation.SchemaVersion != 2 || !observation.OperationComplete || observation.BatchID != "b" ||
		observation.JournalSHA256 == observation.JournalAfterSHA256 {
		t.Fatal("cleanup did not bind original and updated journal evidence")
	}
	summary := verifyCampaignRecoveryAcceptance(t, recipient, f.manifestPath, filepath.Join(f.root, "summary.json"))
	if !summary.Completion.ResourceEvidenceComplete || !summary.Completion.KnownSessionsAbsent ||
		summary.Completion.LinkedObservations != 1 || summary.Completion.TestExecutionComplete || summary.Completion.RecoveryComplete {
		t.Fatalf("resource recovery was confused with test completion: %+v", summary.Completion)
	}
	foundDestination := false
	for _, command := range summary.Commands {
		for i, arg := range command.Argv {
			if arg == "--output-directory" && i+1 < len(command.Argv) && command.Argv[i+1] == recipient.root+"-recovery" {
				foundDestination = true
			}
		}
	}
	if !foundDestination {
		t.Fatal("imported recovery default destination overlaps immutable evidence or is missing")
	}
	// Transfer the observation explicitly: campaign bundles deliberately exclude
	// local cleanup files, while checkpoint and journal bytes remain hash-bound.
	observationBytes, err := os.ReadFile(observations[0])
	if err != nil {
		t.Fatal(err)
	}
	transferredObservation := filepath.Join(f.root, "transferred-observation.json")
	if err := os.WriteFile(transferredObservation, observationBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	secondBundle := filepath.Join(f.root, "second.json")
	exportCampaignRecoveryAcceptance(t, recipient.plan, recipient.policy, f.manifestPath, recipient.work, recipient.index, secondBundle)
	cleanup = append(recipient.args(f.manifestPath), "--batch", "b", "--journal", "--execute")
	if code := cmdTestCampaignControl("cleanup", cleanup); code == 0 {
		t.Fatal("transferred recipient retained cleanup authority")
	}
	next := importCampaignRecoveryAcceptance(t, secondBundle, filepath.Join(f.root, "next"), claims, true)
	nextSummary := verifyCampaignRecoveryAcceptance(t, next, f.manifestPath, filepath.Join(f.root, "next-summary.json"),
		"--cleanup-observation", transferredObservation)
	if !nextSummary.Completion.KnownSessionsAbsent || nextSummary.Completion.LinkedObservations != 1 ||
		nextSummary.Completion.RecoveryComplete {
		t.Fatal("second recipient lost provenance or invented test completion")
	}
	checkpointPath := filepath.Join(next.work, "checkpoint.json")
	checkpointBefore, err := os.ReadFile(checkpointPath)
	if err != nil {
		t.Fatal(err)
	}
	recovery := filepath.Join(f.root, "reconciliation")
	reconcile := append(next.args(f.manifestPath), "--output-directory", recovery, "--check-cleanup")
	if code := cmdTestCampaignReconcile(reconcile); code != 2 {
		t.Fatalf("missing final report must block reconciliation, got %d", code)
	}
	var result testCampaignReconciliation
	if err := readTestCampaignJSON(filepath.Join(recovery, "reconciliation.json"), &result); err != nil {
		t.Fatal(err)
	}
	if result.Ready {
		t.Fatal("journal cleanup manufactured a ready continuation")
	}
	checkpointAfter, err := os.ReadFile(checkpointPath)
	if err != nil || !bytes.Equal(checkpointBefore, checkpointAfter) {
		t.Fatal("reconciliation changed the original checkpoint")
	}
	receiptAfter, err := os.ReadFile(filepath.Join(next.work, "batch-a.json"))
	if err != nil || !bytes.Equal(sourceReceipt, receiptAfter) {
		t.Fatal("reconciliation rewrote source assertion evidence")
	}
	if _, deletes := stub.counts(); deletes != 1 {
		t.Fatal("assessment or reconciliation performed another deletion")
	}
}

func TestCampaignRecoveryAcceptanceBlockingFailures(t *testing.T) {
	// adr: 984 — replacement apps, failed cleanup, unknown provisioning and stale ownership stay blocking through the public CLI.
	for _, mode := range []string{"replacement_app", "lookup_failure", "delete_failure", "unknown_provisioning", "stale_claim", "recorded_fixture_failure"} {
		t.Run(mode, func(t *testing.T) {
			stub := newCampaignRecoveryAcceptanceAPI(t)
			f := interruptedCampaignRecoveryAcceptanceFixture(t, stub, mode == "unknown_provisioning")
			if mode == "recorded_fixture_failure" {
				path := filepath.Join(f.directory, "batch-a.json")
				receipts, _, err := readTestBaselineReport(path, nil)
				if err != nil {
					t.Fatal(err)
				}
				receipts[0].CleanupError = "fixture cleanup unfinished"
				receipts[0].Status = "failed"
				if err := writeTestCampaignJSON(path, receipts); err != nil {
					t.Fatal(err)
				}
				_, hash, err := readTestBaselineReport(path, nil)
				if err != nil {
					t.Fatal(err)
				}
				f.checkpoint.Batches["a"] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
				if err := writeTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), f.checkpoint); err != nil {
					t.Fatal(err)
				}
			}
			transfer := filepath.Join(f.root, "transfer.json")
			exportCampaignRecoveryAcceptance(t, f.planPath, f.policyPath, f.manifestPath, f.directory, "", transfer)
			claims := filepath.Join(f.root, "claims")
			if err := os.Mkdir(claims, 0o700); err != nil {
				t.Fatal(err)
			}
			recipient := importCampaignRecoveryAcceptance(t, transfer, filepath.Join(f.root, "recipient"), claims, true)
			stub.mu.Lock()
			switch mode {
			case "replacement_app":
				stub.sessions[strings.Repeat("b", 32)] = "replacement"
			case "lookup_failure":
				stub.failLookup = true
			case "delete_failure":
				stub.failDelete = true
			}
			stub.mu.Unlock()
			if mode == "stale_claim" {
				resolver, err := loadTestCampaignBundleResolver(recipient.index)
				if err != nil {
					t.Fatal(err)
				}
				var claim testCampaignBundleClaim
				if err := readTestCampaignJSON(resolver.Index.ClaimPath, &claim); err != nil {
					t.Fatal(err)
				}
				claim.Nonce = "stale-owner"
				if err := writeTestCampaignJSON(resolver.Index.ClaimPath, claim); err != nil {
					t.Fatal(err)
				}
			}
			cleanup := append(recipient.args(f.manifestPath), "--batch", "b", "--journal", "--execute")
			code := cmdTestCampaignControl("cleanup", cleanup)
			if mode == "recorded_fixture_failure" {
				if code != 0 {
					t.Fatalf("known b workload cleanup failed: %d", code)
				}
			} else if code == 0 {
				t.Fatal("unsafe or failed cleanup returned success")
			}
			_, deletes := stub.counts()
			expectedDeletes := 0
			if mode == "delete_failure" || mode == "recorded_fixture_failure" {
				expectedDeletes = 1
			}
			if deletes != expectedDeletes {
				t.Fatalf("unexpected deletion attempts: got %d want %d", deletes, expectedDeletes)
			}
			summary := verifyCampaignRecoveryAcceptance(t, recipient, f.manifestPath, filepath.Join(f.root, "blocked-summary.json"))
			if summary.Completion.RecoveryComplete || summary.Completion.TestExecutionComplete {
				t.Fatal("blocked campaign was marked complete")
			}
			if mode == "unknown_provisioning" && summary.Completion.ResourceEvidenceComplete {
				t.Fatal("unknown provisioning outcome was waived")
			}
			if mode == "stale_claim" {
				if summary.Ownership == "active_recipient" || len(summary.Commands) != 0 {
					t.Fatal("stale claim retained recovery authority")
				}
				for _, batch := range summary.Batches {
					if len(batch.Commands) != 0 {
						t.Fatal("stale claim produced actionable cleanup advice")
					}
				}
			}
			if mode == "delete_failure" {
				partial := false
				for _, link := range summary.Completion.Observations {
					if link.Status == "linked_partial" {
						partial = true
					}
				}
				if !partial {
					t.Fatal("failed deletion was not retained as partial bound provenance")
				}
			}
			if mode == "replacement_app" || mode == "lookup_failure" || mode == "delete_failure" {
				if summary.Completion.KnownSessionsAbsent {
					t.Fatal("present, replaced or unavailable session was reported absent")
				}
			}
			if mode == "recorded_fixture_failure" {
				found := false
				for _, batch := range summary.Completion.Batches {
					if batch.ID == "a" {
						found = batch.RecordedCleanupFailure
					}
				}
				if !found || !summary.Completion.KnownSessionsAbsent {
					t.Fatal("fixture failure was lost or successful resource cleanup was not distinguished")
				}
			}
			if _, after := stub.counts(); after != deletes {
				t.Fatal("completion verification mutated sessions")
			}
		})
	}
}

func TestCampaignRecoveryAcceptanceCompletedEvidenceCanPass(t *testing.T) {
	// adr: 984 — actual terminal receipts plus policy and fresh absence can complete recovery; journals alone cannot.
	stub := newCampaignRecoveryAcceptanceAPI(t)
	f := interruptedCampaignRecoveryAcceptanceFixture(t, stub, false, true)
	receipt := campaignRecoveryTestReceipt(f.scenarios["checkout"], strings.Repeat("b", 32))
	receipt.WorkloadAppIDs = map[string]string{"checkout": "app-b"}
	path := filepath.Join(f.directory, "batch-b.json")
	if err := writeTestCampaignJSON(path, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	_, hash, err := readTestBaselineReport(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.checkpoint.Batches["b"] = testCampaignCheckpointBatch{Status: "done", ReportSHA256: hash}
	f.checkpoint.Halted = false
	if err := writeTestCampaignJSON(filepath.Join(f.directory, "checkpoint.json"), f.checkpoint); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	delete(stub.sessions, strings.Repeat("b", 32))
	stub.mu.Unlock()
	transfer := filepath.Join(f.root, "complete.json")
	exportCampaignRecoveryAcceptance(t, f.planPath, f.policyPath, f.manifestPath, f.directory, "", transfer, "--baseline", filepath.Join(f.root, "baseline.json"))
	claims := filepath.Join(f.root, "claims")
	if err := os.Mkdir(claims, 0o700); err != nil {
		t.Fatal(err)
	}
	recipient := importCampaignRecoveryAcceptance(t, transfer, filepath.Join(f.root, "recipient"), claims, true)
	summary := verifyCampaignRecoveryAcceptance(t, recipient, f.manifestPath, filepath.Join(f.root, "complete-summary.json"))
	if !summary.Completion.RecoveryComplete || !summary.Completion.PolicyPassed || !summary.Completion.TestExecutionComplete ||
		!summary.Completion.KnownSessionsAbsent {
		t.Fatalf("fully evidenced recovery did not complete: %+v", summary.Completion)
	}
	if _, deletes := stub.counts(); deletes != 0 {
		t.Fatal("read-only completion verification deleted a session")
	}
}

func acceptanceFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
