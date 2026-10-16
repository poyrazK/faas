package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type testCampaignStatusWatchProgress struct {
	Name             string                         `json:"observation"`
	RecordedAt       *time.Time                     `json:"recorded_at,omitempty"`
	RecordedComplete bool                           `json:"recorded_operation_complete"`
	Faults           []testCampaignFaultObservation `json:"fault_resources"`
	Sessions         []testCampaignCleanupAction    `json:"sessions"`
	Issue            *testCampaignRecoveryIssue     `json:"issue,omitempty"`
}

type testCampaignStatusWatcher struct {
	State            string                           `json:"state"`
	ReceiptPresent   bool                             `json:"receipt_present"`
	EvidenceVerified bool                             `json:"local_evidence_verified"`
	BindingVerified  bool                             `json:"resume_binding_verified"`
	ResumeReady      bool                             `json:"resume_ready"`
	SnapshotStable   bool                             `json:"snapshot_stable"`
	Guard            string                           `json:"guard"`
	GuardPID         int                              `json:"guard_owner_pid,omitempty"`
	Owner            testCampaignProcessStatus        `json:"receipt_owner"`
	PreviousOwners   int                              `json:"previous_owners"`
	Attempts         int                              `json:"cleanup_attempts"`
	RecordedComplete bool                             `json:"recorded_cleanup_complete"`
	Progress         *testCampaignStatusWatchProgress `json:"last_observation,omitempty"`
	Blockers         []testCampaignStatusIssue        `json:"blockers"`
	NextAction       string                           `json:"next_action"`
	NextCommand      []string                         `json:"next_command"`
	Command          string                           `json:"command"`
	Notes            []string                         `json:"notes"`
}

type testCampaignStatusWatchSnapshot struct {
	Watcher   *testCampaignStatusWatcher
	Paths     []string
	Stamp     string
	GuardPath string
	Inventory func() string
}

func (snapshot testCampaignStatusWatchSnapshot) currentStamp() string {
	values := map[string]string{}
	for _, path := range snapshot.Paths {
		values[path] = testCampaignStatusFileStamp(path)
	}
	guard, pid := inspectTestCampaignWatchGuard(snapshot.GuardPath)
	inventory := ""
	if snapshot.Inventory != nil {
		inventory = snapshot.Inventory()
	}
	return testCampaignJSONHash(struct {
		Files     map[string]string
		Guard     string
		PID       int
		Inventory string
	}{values, guard, pid, inventory})
}

func (status *testCampaignStatusWatcher) block(code, message, action string) {
	status.Blockers = append(status.Blockers, testCampaignStatusIssue{Code: code, Message: message, Action: action})
	status.State = "manual_inspection_required"
	status.ResumeReady = false
}

func (status *testCampaignStatusWatcher) command(paths testCampaignWatchPaths, mode string, execute bool) {
	args := []string{"gregale", "test", "policy", mode, "--plan", paths.Plan, "--directory", paths.Directory}
	if mode != "recover-lock" {
		args = append(args, "--policy", paths.Policy, "--manifest", paths.Manifest)
	}
	if paths.Handoff != "" {
		args = append(args, "--handoff", paths.Handoff)
	}
	if mode == "cleanup" {
		args = append(args, "--journal", "--faults", "--fault-history")
	}
	if execute {
		args = append(args, "--execute")
	}
	status.NextCommand = args
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quoteLogCommandArg(arg)
	}
	status.Command = strings.Join(quoted, " ")
}

func inspectTestCampaignStatusWatcher(paths testCampaignWatchPaths, plan testCampaignPolicyPlan, cp *testCampaignCheckpoint, scenarios map[string]testScenario, resolver *testCampaignBundleResolver) testCampaignStatusWatchSnapshot {
	for _, field := range []*string{&paths.Plan, &paths.Policy, &paths.Manifest, &paths.Directory, &paths.Handoff} {
		if *field != "" {
			if absolute, err := filepath.Abs(*field); err == nil {
				*field = absolute
			}
		}
	}
	status := &testCampaignStatusWatcher{State: "not_started", SnapshotStable: true, Owner: testCampaignProcessStatus{Status: "unverifiable"}, Blockers: []testCampaignStatusIssue{}, Notes: []string{
		"Saved cleanup observations are historical; status makes no API calls and does not prove current resource absence.",
		"Cleanup never certifies test execution. Use the verified batch evidence and policy verdict separately.",
		"Resume readiness is advisory. cleanup-watch rechecks ownership, exclusion and evidence before acting.",
	}}
	guardPath := filepath.Join(paths.Directory, "cleanup-watch.lock")
	snapshot := testCampaignStatusWatchSnapshot{Watcher: status, GuardPath: guardPath, Paths: []string{paths.Plan, paths.Policy, paths.Manifest, filepath.Join(paths.Directory, testCampaignWatchReceiptName), filepath.Join(paths.Directory, "checkpoint.json"), filepath.Join(paths.Directory, "runner.lock"), guardPath}}
	if resolver != nil {
		snapshot.Paths = append(snapshot.Paths, resolver.IndexPath, resolver.Index.ClaimPath, resolver.Index.ClaimPath+".lock")
	}
	// Invalid or absent receipts are diagnostics, never a reason to hide the rest
	// of an otherwise inspectable campaign report.
	var receipt testCampaignWatchReceipt
	err := readTestCampaignJSON(filepath.Join(paths.Directory, testCampaignWatchReceiptName), &receipt)
	status.ReceiptPresent = !os.IsNotExist(err)
	if err == nil && receipt.Progress != nil && filepath.Base(receipt.Progress.Name) == receipt.Progress.Name {
		snapshot.Paths = append(snapshot.Paths, filepath.Join(paths.Directory, receipt.Progress.Name))
	}
	if cp != nil {
		snapshot.Inventory = func() string {
			sources, err := collectTestCampaignFaultHistory(plan, *cp, scenarios, paths.Directory, resolver)
			if err != nil {
				return "unverifiable"
			}
			return testCampaignJSONHash(sources)
		}
		if sources, err := collectTestCampaignFaultHistory(plan, *cp, scenarios, paths.Directory, resolver); err == nil {
			for _, source := range sources {
				snapshot.Paths = append(snapshot.Paths, filepath.Join(source.Directory, "checkpoint.json"), filepath.Join(source.Directory, testCampaignRecoverySealName))
				entries, err := os.ReadDir(testCampaignJournalDirectory(source.Directory, source.BatchID))
				if err != nil {
					continue
				}
				for _, entry := range entries {
					snapshot.Paths = append(snapshot.Paths, filepath.Join(testCampaignJournalDirectory(source.Directory, source.BatchID), entry.Name()))
				}
			}
		}
	}
	snapshot.Stamp = snapshot.currentStamp()
	status.Guard, status.GuardPID = inspectTestCampaignWatchGuard(guardPath)
	status.command(paths, "cleanup", false)
	status.NextAction = "Inspect the saved workload and fault inventory before choosing cleanup."
	if err != nil {
		if !os.IsNotExist(err) {
			status.block("watcher_receipt_unreadable", "Watcher recovery receipt is unreadable or invalid.", "Retain the receipt and inspect the original evidence; do not recreate ownership proof.")
		} else {
			inspectTestCampaignStatusWithoutReceipt(status, paths, plan, cp)
		}
		return snapshot
	}
	if receipt.SchemaVersion != 1 || receipt.Attempts < 0 || len(receipt.PreviousOwners) >= testCampaignWatchOwnerLimit {
		status.block("watcher_receipt_invalid", "Watcher receipt version, attempt count or ownership history is invalid.", "Inspect the retained receipt and original ownership evidence.")
		return snapshot
	}
	status.Owner.PID = receipt.Owner.PID
	status.Owner.Status, status.Owner.Reason = probeTestCampaignProcess(receipt.Owner)
	status.PreviousOwners = len(receipt.PreviousOwners)
	if cp == nil || receipt.CheckpointSHA256 != testCampaignJSONHash(*cp) {
		status.block("watcher_checkpoint_changed", "Checkpoint no longer matches the watcher receipt.", "Restore the original bound checkpoint or inspect cleanup manually.")
		return snapshot
	}
	sources, err := collectTestCampaignFaultHistory(plan, *cp, scenarios, paths.Directory, resolver)
	if err != nil || testCampaignJSONHash(sources) != receipt.InventorySHA256 {
		status.block("watcher_inventory_changed", "Resource inventory or recovery ownership no longer matches the watcher receipt.", "Restore matching journals and handoff evidence before resuming.")
		return snapshot
	}
	local := &testCampaignWatchRecovery{Paths: paths, Receipt: &receipt}
	if receipt.Progress != nil {
		if err := local.verifyProgress(*receipt.Progress); err != nil {
			status.block("watcher_progress_changed", "Referenced cleanup observation is missing or changed.", "Restore the original observation; status cannot replace cleanup proof.")
			return snapshot
		}
		var progress testCampaignFaultHistory
		if err := readTestCampaignJSON(filepath.Join(paths.Directory, receipt.Progress.Name), &progress); err != nil {
			status.block("watcher_progress_changed", "Cleanup observation changed during inspection.", "Rerun status against stable evidence.")
			return snapshot
		}
		status.Progress = &testCampaignStatusWatchProgress{Name: receipt.Progress.Name, RecordedComplete: progress.Complete, Faults: progress.Faults, Sessions: progress.Sessions, Issue: progress.Issue}
		stamp := strings.TrimSuffix(strings.TrimPrefix(receipt.Progress.Name, "fault-history-cleanup-"), ".json")
		if recorded, err := time.Parse("20060102T150405.000000000", stamp); err == nil {
			status.Progress.RecordedAt = &recorded
		}
	}
	status.EvidenceVerified = true
	status.Attempts, status.RecordedComplete = receipt.Attempts, receipt.Complete
	inspectTestCampaignStatusWatchReadiness(status, paths, &receipt)
	return snapshot
}

func inspectTestCampaignStatusWithoutReceipt(status *testCampaignStatusWatcher, paths testCampaignWatchPaths, plan testCampaignPolicyPlan, cp *testCampaignCheckpoint) {
	if status.Guard == "held" {
		status.State = "active"
		status.NextAction = "A watcher holds the kernel guard; allow it to finish or inspect its logs."
		status.NextCommand = nil
		status.Command = ""
		return
	}
	if status.Guard == "unverifiable" {
		status.block("watcher_guard_unverifiable", "Watcher exclusion cannot be verified.", "Inspect the guard and use a supported Linux process domain.")
		return
	}
	if cp != nil {
		result, err := inspectTestCampaignRunnerLock(paths.Directory, plan, *cp)
		if err == nil && result.Recoverable {
			status.State = "ready_to_start"
			status.NextAction = "Start the opt-in watcher; it will verify termination through the grace period."
			status.command(paths, "cleanup-watch", true)
			return
		}
		if _, err := os.Lstat(filepath.Join(paths.Directory, "runner.lock")); err == nil {
			status.State = "waiting_for_run"
			status.NextAction = "Inspect the coordinator and tracked workers; active or unknown processes block cleanup."
			status.command(paths, "recover-lock", false)
			return
		}
	}
	if cp == nil || !cp.Halted {
		uncertain := false
		if cp != nil {
			for _, batch := range cp.Batches {
				uncertain = uncertain || batch.Status == "uncertain" || batch.Status == "running"
			}
		}
		if !uncertain {
			status.NextAction = "No watcher ownership has been recorded; use batch evidence to assess test execution."
			status.NextCommand = nil
			status.Command = ""
			return
		}
	}
	status.block("watcher_receipt_missing", "No durable watcher receipt or verifiable original runner lock is available.", "Inspect ordinary cleanup explicitly; elapsed time cannot supply ownership proof.")
}

func inspectTestCampaignStatusWatchReadiness(status *testCampaignStatusWatcher, paths testCampaignWatchPaths, receipt *testCampaignWatchReceipt) {
	seen := map[testCampaignProcessIdentity]bool{}
	for _, owner := range append(append([]testCampaignProcessIdentity{}, receipt.PreviousOwners...), receipt.Owner) {
		if seen[owner] {
			status.block("watcher_owner_invalid", "Watcher ownership history repeats an identity.", "Inspect the original receipt before choosing recovery.")
			return
		}
		seen[owner] = true
	}
	if status.Guard == "unverifiable" {
		status.block("watcher_guard_unverifiable", "Watcher exclusion cannot be verified.", "Inspect the guard and use a supported Linux process domain.")
		return
	}
	if status.Guard == "held" || status.Owner.Status == "alive" || status.Owner.Status == "stopped" {
		status.State = "active"
		status.NextAction = "A watcher or its receipt owner remains active; allow it to finish and rerun status."
		status.NextCommand = nil
		status.Command = ""
		return
	}
	w, err := newTestCampaignWatchRecovery(paths)
	if err != nil || w.BindingSHA256 != receipt.BindingSHA256 {
		status.block("watcher_binding_unverified", "Resume inputs or configured API profile do not match the watcher receipt.", "Use the original input paths, handoff and API profile; rerun status to verify readiness.")
		return
	}
	status.BindingVerified = true
	w.Receipt = receipt
	if _, err := w.resumeStamp(true); err != nil {
		status.block("watcher_resume_blocked", "Prior owner termination, stale exclusion or receipt evidence cannot authorize resumption.", "Inspect original ownership records and cleanup inventory; do not remove locks by hand.")
		return
	}
	if len(receipt.PreviousOwners)+1 >= testCampaignWatchOwnerLimit {
		status.block("watcher_owner_limit", "Watcher ownership recovery history is exhausted.", "Retain the receipt and inspect explicit cleanup manually.")
		return
	}
	status.ResumeReady = true
	status.State = "ready_to_resume"
	if receipt.Complete {
		status.State = "cleanup_recorded_complete"
	}
	status.NextAction = "Restart the same watcher command to revalidate ownership and current API outcomes."
	status.command(paths, "cleanup-watch", true)
}

func applyTestCampaignStatusWatcherSnapshot(report *testCampaignStatusReport, snapshot testCampaignStatusWatchSnapshot) {
	report.Watcher = snapshot.Watcher
	if !report.SnapshotStable || snapshot.currentStamp() != snapshot.Stamp {
		report.SnapshotStable = false
		report.Watcher.SnapshotStable = false
		report.Watcher.Progress = nil
		report.Watcher.EvidenceVerified = false
		report.Watcher.RecordedComplete = false
		report.Watcher.BindingVerified = false
		report.Watcher.NextCommand = nil
		report.Watcher.Command = ""
		report.Watcher.NextAction = "Rerun status for a stable snapshot before choosing recovery."
		report.Watcher.block("watcher_snapshot_changed", "Watcher receipt, progress or exclusion changed while status was read.", "Rerun status for a stable snapshot before choosing a next action.")
	}
}
