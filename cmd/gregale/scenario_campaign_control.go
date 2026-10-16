package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const testCampaignCancelName = "campaign-cancel.json"

type testCampaignCancellation struct {
	SchemaVersion int       `json:"schema_version"`
	PlanSHA256    string    `json:"plan_sha256"`
	RequestedAt   time.Time `json:"requested_at"`
}

func testCampaignCancellationRequested(directory string, plan testCampaignPolicyPlan) error {
	path := filepath.Join(directory, testCampaignCancelName)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	var request testCampaignCancellation
	if err := readTestCampaignJSON(path, &request); err != nil {
		return errors.New("invalid cancellation request; scheduling stopped")
	}
	if request.SchemaVersion != 1 || request.PlanSHA256 != testCampaignJSONHash(plan) || request.RequestedAt.IsZero() {
		return errors.New("cancellation binding changed; scheduling stopped")
	}
	return errors.New("campaign cancellation requested; active batches drained; reconcile before continuing")
}

type testCampaignCleanupClient interface {
	testCampaignCleanupReader
	DestroyDevSession(context.Context, string, ...string) error
}

type testCampaignCleanupAction struct {
	Issue   *testCampaignRecoveryIssue `json:"issue,omitempty"`
	Project string                     `json:"project"`
	RunID   string                     `json:"run_id"`
	AppID   string                     `json:"app_id"`
	Status  string                     `json:"status"`
}

// Only complete, configuration-bound receipt identities can authorize cleanup.
// No fixture commands, API consumer revocations, or scenario registrations are retried.
func testCampaignRetryCleanup(ctx context.Context, client testCampaignCleanupClient, scenario testScenario,
	leaves []testRunReceipt, execute bool) ([]testCampaignCleanupAction, error) {
	targets, err := testCampaignReceiptCleanupTargets(scenario, leaves)
	if err != nil {
		return nil, err
	}
	return testCampaignRetryCleanupTargets(ctx, client, targets, execute)
}

func testCampaignReceiptCleanupTargets(scenario testScenario, leaves []testRunReceipt) (map[string]testCampaignCleanupAction, error) {
	targets := map[string]testCampaignCleanupAction{}
	for _, leaf := range leaves {
		decoded, err := hex.DecodeString(leaf.RunID)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != leaf.RunID {
			return nil, errors.New("cleanup requires a canonical native run ID")
		}
		names := []string{scenario.Project}
		for name := range scenario.Services {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			id := leaf.WorkloadAppIDs[name]
			if id == "" {
				return nil, errors.New("cleanup requires recorded app IDs for every workload; inspect incomplete runs manually")
			}
			project := scenario.Project
			if name != scenario.Project {
				project += "-" + name
			}
			key := project + "/" + leaf.RunID
			target := testCampaignCleanupAction{Project: project, RunID: leaf.RunID, AppID: id}
			if old, ok := targets[key]; ok && old.AppID != id {
				return nil, errors.New("conflicting cleanup app identities")
			}
			targets[key] = target
		}
	}
	return targets, nil
}

func testCampaignRetryCleanupTargets(ctx context.Context, client testCampaignCleanupClient, targets map[string]testCampaignCleanupAction, execute bool) ([]testCampaignCleanupAction, error) {
	ctx, cancel := context.WithTimeout(ctx, testCampaignRecoveryBudget)
	defer cancel()
	actions := []testCampaignCleanupAction{}
	if len(targets) == 0 || len(targets) > 1024 {
		return nil, errors.New("cleanup requires 1..1024 verified workload identities")
	}
	keys := make([]string, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		target := targets[key]
		target.Status = "unverified"
		target.Issue = nil
		actions = append(actions, target)
	}
	// Preflight every identity before the first mutation.
	for index := range actions {
		target := &actions[index]
		session, err := readTestCampaignRecoverySession(ctx, client, target.Project, target.RunID)
		if isNotFound(err) {
			target.Status = "absent"
		} else if err != nil {
			target.Issue = testCampaignRecoveryIssueFor("session", "lookup", err)
			return actions, fmt.Errorf("live cleanup lookup failed: %w", err)
		} else if session.App.ID != target.AppID {
			target.Issue = testCampaignRecoveryIssueFor("session", "verify_identity", errors.New("identity changed"))
			return actions, errors.New("live session app ID differs from receipt; refusing cleanup")
		} else {
			target.Status = "present"
		}
	}
	for i := range actions {
		target := &actions[i]
		if !execute || target.Status == "absent" {
			continue
		}
		// Recheck immediately before deletion; never use the shared default workspace.
		session, err := readTestCampaignRecoverySession(ctx, client, target.Project, target.RunID)
		if isNotFound(err) {
			target.Status = "absent"
			continue
		}
		if err != nil {
			target.Status = "unverified"
			target.Issue = testCampaignRecoveryIssueFor("session", "recheck", err)
			return actions, fmt.Errorf("cleanup identity could not be reverified: %w", err)
		}
		if session.App.ID != target.AppID {
			target.Status = "identity_changed"
			target.Issue = testCampaignRecoveryIssueFor("session", "verify_identity", errors.New("identity changed"))
			return actions, errors.New("cleanup identity could not be reverified")
		}
		if err := deleteTestCampaignRecoverySession(ctx, client, target.Project, target.RunID); err != nil && !isNotFound(err) {
			target.Status = "delete_failed"
			target.Issue = testCampaignRecoveryIssueFor("session", "delete", err)
			return actions, fmt.Errorf("session cleanup failed: %w", err)
		}
		_, err = readTestCampaignRecoverySession(ctx, client, target.Project, target.RunID)
		if !isNotFound(err) {
			target.Status = "unconfirmed"
			if err == nil {
				err = errors.New("session remains present")
			}
			target.Issue = testCampaignRecoveryIssueFor("session", "confirm_absence", err)
			return actions, errors.New("session absence could not be confirmed")
		}
		target.Status = "deleted"
	}
	return actions, nil
}

func cmdTestCampaignControl(mode string, args []string) int {
	fs := newFlagSet("test policy "+mode, flag.ContinueOnError)
	planPath := fs.String("plan", "", "saved campaign plan")
	policyPath := fs.String("policy", "", "bound campaign policy (cleanup)")
	manifestPath := fs.String("manifest", "gregale-test.yaml", "bound manifest (cleanup)")
	directory := fs.String("directory", "", "private campaign directory")
	handoff := fs.String("handoff", "", "active portable import index")
	execute := fs.Bool("execute", false, "retry verified workload session deletion (cleanup only)")
	faultHistory := fs.Bool("fault-history", false, "inspect or clean fault resources across the verified recovery chain; requires --journal --faults; omit --batch")
	faultMode := fs.Bool("faults", false, "also revoke journaled consumers and conditionally remove fault registrations; requires --journal")
	journalMode := fs.Bool("journal", false, "inspect or clean saved run identities when no finalized report exists")
	batchID := fs.String("batch", "", "single recorded batch to inspect or clean")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if (*faultHistory && (!*faultMode || *batchID != "")) || (*faultMode && (!*journalMode || mode != "cleanup")) || rejectUnexpectedFlagArgs(fs) || fs.NArg() != 0 || *planPath == "" || *directory == "" ||
		(mode == "cleanup" && (*policyPath == "" || (*batchID == "" && !*faultHistory))) || (mode == "cancel" && (*execute || *batchID != "" || *journalMode)) {
		return printErr("Invalid campaign control options", errors.New("--plan and --directory required; cleanup requires --policy and either --batch or --fault-history (--journal --faults)"))
	}
	resolver, err := loadTestCampaignBundleResolver(*handoff)
	if err != nil {
		return printErr("Invalid handoff", err)
	}
	if err := resolver.requireActive(); err != nil {
		return printErr("Inactive handoff", err)
	}
	if err := enforceTestCampaignPlanHandoff(*planPath, resolver); err != nil {
		return printErr("Invalid imported plan", err)
	}
	var plan testCampaignPolicyPlan
	if err := readTestCampaignJSON(*planPath, &plan); err != nil {
		return printErr("Invalid plan", err)
	}
	*directory, err = filepath.Abs(*directory)
	if err != nil {
		return printErr("Invalid directory", err)
	}
	if err := testCampaignPrivateDirectory(*directory); err != nil {
		return printErr("Invalid directory", err)
	}
	if err := checkTestCampaignBundleWork(*directory, resolver, true); err != nil {
		return printErr("Invalid campaign ownership", err)
	}
	// Cancellation only publishes a plan-bound request. It does not acquire the
	// runner's lock or rewrite a checkpoint while the scheduler owns it.
	if mode == "cancel" {
		var cp testCampaignCheckpoint
		if err := readTestCampaignJSON(filepath.Join(*directory, "checkpoint.json"), &cp); err != nil {
			return printErr("Invalid checkpoint", err)
		}
		if cp.SchemaVersion != 1 || cp.PlanSHA256 != testCampaignJSONHash(plan) || !testCampaignCheckpointLineageMatches(cp, plan) {
			return printErr("Checkpoint changed", errors.New("plan mismatch"))
		}
		for _, name := range []string{testCampaignRecoverySealName, testCampaignHandoffName} {
			if _, err := os.Lstat(filepath.Join(*directory, name)); !os.IsNotExist(err) {
				return printErr("Campaign already sealed", errors.New("cancel the active continuation instead"))
			}
		}
		request := testCampaignCancellation{SchemaVersion: 1, PlanSHA256: cp.PlanSHA256, RequestedAt: time.Now().UTC()}
		body, _ := json.Marshal(request)
		path := filepath.Join(*directory, testCampaignCancelName)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return printErr("Cancellation request exists or cannot be created", err)
		}
		_, writeErr := file.Write(body)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return printErr("Could not persist cancellation", errors.New("inspect cancellation artifact"))
		}
		return 0
	}
	release, err := acquireTestCampaignHandoffFor(resolver, *directory, testCampaignJSONHash(plan), "cleanup", false)
	if err != nil {
		return printErr("Handoff unavailable", err)
	}
	defer release()
	releaseRunner, err := acquireTestCampaignRunnerLock(*directory, testCampaignJSONHash(plan))
	if err != nil {
		return printErr("Campaign must be inactive before cleanup", err)
	}
	defer releaseRunner()
	for _, name := range []string{testCampaignRecoverySealName, testCampaignHandoffName} {
		if _, err := os.Lstat(filepath.Join(*directory, name)); !os.IsNotExist(err) {
			return printErr("Campaign already sealed", errors.New("cleanup must precede reconciliation or handoff"))
		}
	}
	policy, err := readTestCampaignPolicy(*policyPath)
	if err != nil {
		return printErr("Invalid policy", err)
	}
	manifest, _, err := readTestManifestDocument(*manifestPath, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil {
		return printErr("Invalid manifest", err)
	}
	if err := validateTestCampaignRunnable(plan, manifest.Scenarios, policy); err != nil {
		return printErr("Campaign binding changed", err)
	}
	if err := validateTestCampaignRecoverySeal(plan, resolver); err != nil {
		return printErr("Invalid recovery lineage", err)
	}
	var cp testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(*directory, "checkpoint.json"), &cp); err != nil {
		return printErr("Invalid checkpoint", err)
	}
	if cp.SchemaVersion != 1 || cp.PlanSHA256 != testCampaignJSONHash(plan) || !testCampaignCheckpointLineageMatches(cp, plan) {
		return printErr("Checkpoint changed", errors.New("plan mismatch"))
	}
	if err := registerTestCampaignHandoffWork(resolver, testCampaignJSONHash(plan), *directory, cp.Side, cp.Revision); err != nil {
		return printErr("Invalid handoff work directory", err)
	}
	if *faultHistory {
		client, err := authedClient()
		if err != nil {
			return printErr("Cleanup authentication failed", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		result, err := cleanupTestCampaignFaultHistory(ctx, client, plan, cp, manifest.Scenarios, *directory, resolver, *execute)
		if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
			return printErr("Could not write history observations", encodeErr)
		}
		if err != nil {
			return printErr("Fault history remains unresolved", err)
		}
		return 0
	}
	var selected *testCampaignCostBatch
	for i := range plan.Batches {
		b := &plan.Batches[i]
		if b.ID == *batchID && b.Side == cp.Side {
			selected = b
		}
	}
	saved, ok := cp.Batches[*batchID]
	if selected == nil || !ok || (saved.Status != "done" && saved.Status != "running" && saved.Status != "uncertain") {
		return printErr("Unknown batch", errors.New("only recorded attempted batches may be inspected"))
	}
	path := filepath.Join(*directory, "batch-"+selected.ID+".json")
	scenario := manifest.Scenarios[selected.Scenario]
	var leaves []testRunReceipt
	hash, journalHash := "", ""
	var journalTargets map[string]testCampaignCleanupAction
	if *journalMode {
		if saved.Status == "done" || saved.ReportSHA256 != "" {
			return printErr("Final report already bound", errors.New("use receipt cleanup; journal mode cannot bypass finalized evidence"))
		}
		journalTargets, journalHash, err = readTestCampaignJournalTargets(*directory, testCampaignJSONHash(plan), cp, *selected, scenario)
		if err != nil {
			return printErr("Incomplete journal evidence", err)
		}
	} else {
		var roots []testRunReceipt
		roots, hash, err = readTestBaselineReport(path, nil)
		if err != nil {
			return printErr("Incomplete cleanup evidence; use --journal for saved provisioning identities", err)
		}
		if saved.ReportSHA256 != "" && hash != saved.ReportSHA256 {
			return printErr("Changed batch evidence", errors.New("report hash mismatch"))
		}
		for _, root := range roots {
			collectTestFailureLeaves(root, "", &leaves)
		}
		seen := map[string]bool{}
		for _, leaf := range leaves {
			if err := testCampaignRecoverableLeaf(leaf, *selected, scenario, cp.Revision); err != nil {
				return printErr("Unverified cleanup evidence", err)
			}
			if seen[leaf.RunID] {
				return printErr("Duplicate run identity", errors.New("ambiguous cleanup evidence"))
			}
			seen[leaf.RunID] = true
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Cleanup authentication failed", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var faultJournals []testCampaignRunJournal
	if *faultMode {
		faultJournals, err = readTestCampaignFaultJournals(*directory, selected.ID, scenario)
		if err != nil {
			return printErr("Invalid fault inventory", err)
		}
		observations, faultErr := inspectTestCampaignFaults(ctx, client, faultJournals, false, false)
		if faultErr == nil && *execute {
			observations, faultErr = inspectTestCampaignFaults(ctx, client, faultJournals, true, false)
		}
		if saveErr := persistTestCampaignFaultObservation(*directory, observations); saveErr != nil {
			return printErr("Could not persist fault observations", saveErr)
		}
		if faultErr != nil {
			return printErr("Fault cleanup blocked", faultErr)
		}
	}
	var actions []testCampaignCleanupAction
	var cleanupErr error
	if *journalMode {
		actions, cleanupErr = testCampaignRetryCleanupTargets(ctx, client, journalTargets, *execute)
	} else {
		actions, cleanupErr = testCampaignRetryCleanup(ctx, client, scenario, leaves, *execute)
	}
	if *journalMode && *execute {
		if err := recordTestCampaignJournalCleanup(*directory, selected.ID, actions); err != nil && cleanupErr == nil {
			cleanupErr = errors.New("could not persist journal cleanup progress")
		}
	}
	if *faultMode && cleanupErr == nil {
		observations, faultErr := inspectTestCampaignFaults(ctx, client, faultJournals, *execute, true)
		if saveErr := persistTestCampaignFaultObservation(*directory, observations); saveErr != nil {
			cleanupErr = saveErr
		} else if faultErr != nil {
			cleanupErr = faultErr
		}
	}
	// Observations are separate from immutable receipts and cannot waive their
	// recorded fixture/consumer cleanup failures or refund attempted budget.
	targets := journalTargets
	if !*journalMode {
		targets, err = testCampaignReceiptCleanupTargets(scenario, leaves)
		if err != nil && cleanupErr == nil {
			cleanupErr = err
		}
	}
	result := testCampaignCleanupObservation{SchemaVersion: 2, PlanSHA256: cp.PlanSHA256, CheckpointSHA256: testCampaignJSONHash(cp),
		BatchID: selected.ID, Side: cp.Side, Revision: cp.Revision, Evidence: "receipt", ReportSHA256: hash,
		JournalSHA256: journalHash, TargetsSHA256: testCampaignCleanupTargetsSHA256(targets), ObservedAt: time.Now().UTC(),
		Executed: *execute, Actions: actions}
	if *journalMode {
		result.Evidence = "journal"
		_, result.JournalAfterSHA256, err = readTestCampaignJournalTargets(*directory, testCampaignJSONHash(plan), cp, *selected, scenario)
		if err != nil && cleanupErr == nil {
			cleanupErr = errors.New("could not verify journal cleanup progress")
		}
	}
	result.OperationComplete = cleanupErr == nil
	observationPath := filepath.Join(*directory, fmt.Sprintf("cleanup-%d.json", time.Now().UTC().UnixNano()))
	if err := writeTestCampaignJSON(observationPath, result); err != nil {
		return printErr("Could not save cleanup observations", err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return printErr("Could not write cleanup observations", err)
	}
	if cleanupErr != nil {
		return printErr("Cleanup remains unresolved", cleanupErr)
	}
	fmt.Fprintln(os.Stderr, "Original receipts retained; reconcile with --check-cleanup. Recorded cleanup failures remain blocking.")
	return 0
}
