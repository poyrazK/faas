package main

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type testCampaignRecoveryCommand struct {
	Purpose    string   `json:"purpose"`
	Mutates    bool     `json:"mutates"`
	Argv       []string `json:"argv"`
	POSIXShell string   `json:"posix_shell"`
}
type testCampaignRecoveryResource struct {
	Project string `json:"project"`
	RunID   string `json:"run_id"`
	AppID   string `json:"app_id"`
	Live    string `json:"live_status"`
}
type testCampaignRecoveryAssessmentBatch struct {
	ID             string                         `json:"id"`
	Evidence       string                         `json:"cleanup_evidence"`
	EvidenceSHA256 string                         `json:"cleanup_evidence_sha256,omitempty"`
	Resources      []testCampaignRecoveryResource `json:"resources"`
	Blockers       []string                       `json:"blockers"`
	Commands       []testCampaignRecoveryCommand  `json:"commands"`
}
type testCampaignRecoveryAssessment struct {
	SchemaVersion  int                                   `json:"schema_version"`
	Informational  bool                                  `json:"informational"`
	ReadOnly       bool                                  `json:"read_only"`
	ObservedAt     time.Time                             `json:"observed_at"`
	SnapshotStable bool                                  `json:"snapshot_stable"`
	LiveChecked    bool                                  `json:"live_checked"`
	Ownership      string                                `json:"ownership"`
	Recommendation string                                `json:"recommendation"`
	Status         testCampaignStatusReport              `json:"status"`
	Blockers       []string                              `json:"blockers"`
	Batches        []testCampaignRecoveryAssessmentBatch `json:"batches"`
	Commands       []testCampaignRecoveryCommand         `json:"commands"`
	Notes          []string                              `json:"notes"`
	Completion     *testCampaignRecoveryCompletion       `json:"completion,omitempty"`
}
type testCampaignRecoveryAssessmentOptions struct {
	Plan, Policy, Manifest, Directory, Handoff, From, Baseline, OutputDirectory, RecoveryBundle string
}

func newTestCampaignRecoveryCommand(purpose string, mutates bool, args ...string) testCampaignRecoveryCommand {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return testCampaignRecoveryCommand{purpose, mutates, append([]string{}, args...), strings.Join(quoted, " ")}
}
func (o testCampaignRecoveryAssessmentOptions) base(command string) []string {
	args := []string{"gregale", "test", "policy", command, "--plan", o.Plan, "--policy", o.Policy, "--manifest", o.Manifest, "--directory", o.Directory}
	if o.Handoff != "" {
		args = append(args, "--handoff", o.Handoff)
	}
	return args
}

// This fingerprint covers local evidence read by the assessment, including
// journal membership, so a concurrent worker cannot leave actionable advice.
func testCampaignRecoveryAssessmentStamp(plan testCampaignPolicyPlan, o testCampaignRecoveryAssessmentOptions, resolver *testCampaignBundleResolver, extras ...string) (string, error) {
	paths := []string{o.Plan, o.Policy, o.Manifest, o.From, o.Baseline, filepath.Join(o.Directory, "checkpoint.json"),
		filepath.Join(o.Directory, testCampaignRecoverySealName), filepath.Join(o.Directory, testCampaignHandoffName),
		filepath.Join(o.Directory, testCampaignCancelName), filepath.Join(o.Directory, "runner.lock"),
		filepath.Join(o.Directory, testCampaignImportName), filepath.Join(o.Directory, testCampaignContinuationOwnerName)}
	if resolver != nil {
		paths = append(paths, resolver.IndexPath, resolver.Index.ClaimPath, resolver.Index.ClaimPath+".lock")
	}
	link := plan.Recovery
	seenParents := map[string]bool{}
	for depth := 0; link != nil; depth++ {
		if depth >= 32 || seenParents[link.ParentDirectory] {
			return "", errors.New("recovery ancestry is cyclic or too deep")
		}
		seenParents[link.ParentDirectory] = true
		physical, err := resolveTestCampaignDirectory(link.ParentDirectory, resolver)
		if err != nil {
			return "", err
		}
		paths = append(paths, filepath.Join(physical, "checkpoint.json"), filepath.Join(physical, testCampaignRecoverySealName),
			filepath.Join(physical, testCampaignHandoffName), filepath.Join(physical, testCampaignImportName))
		paths = append(paths, filepath.Join(physical, "runner.lock"), filepath.Join(physical, testCampaignContinuationOwnerName))
		ancestorPaths, err := testCampaignFaultAncestorStampPaths(physical)
		if err != nil {
			return "", err
		}
		paths = append(paths, ancestorPaths...)
		var parent testCampaignCheckpoint
		if readTestCampaignJSON(filepath.Join(physical, "checkpoint.json"), &parent) != nil || parent.LineageVersion != 1 {
			break
		}
		link = parent.Recovery
	}
	for _, batch := range plan.Batches {
		paths = append(paths, filepath.Join(o.Directory, "batch-"+batch.ID+".json"), filepath.Join(o.Directory, "batch-"+batch.ID+".log"))
		directory := testCampaignJournalDirectory(o.Directory, batch.ID)
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			paths = append(paths, directory)
			continue
		}
		if err != nil {
			return "", err
		}
		if err := testCampaignPrivateDirectory(directory); err != nil {
			return "", err
		}
		if len(entries) > 1025 {
			return "", errors.New("journal snapshot count exceeds assessment limit")
		}
		paths = append(paths, directory)
		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				return "", errors.New("journal contains a non-regular entry")
			}
			paths = append(paths, filepath.Join(directory, entry.Name()))
		}
	}
	if o.RecoveryBundle != "" {
		entries, err := os.ReadDir(o.RecoveryBundle)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				paths = append(paths, filepath.Join(o.RecoveryBundle, entry.Name()))
			}
		}
	}
	paths = append(paths, extras...)
	if len(paths) > 8192 {
		return "", errors.New("assessment exceeds artifact count limit")
	}
	stamps := map[string]string{}
	var total int64
	for _, name := range paths {
		if name == "" {
			continue
		}
		info, err := os.Lstat(name)
		if os.IsNotExist(err) {
			stamps[name] = "absent"
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("assessment artifact must not be a symlink")
		}
		if info.IsDir() {
			stamps[name] = "directory"
			continue
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("assessment artifact must be regular")
		}
		// Logs are never read or emitted. Their existence and metadata reserve work.
		if filepath.Dir(name) == o.Directory && testCampaignBundleBatchName.MatchString(strings.TrimSuffix(filepath.Base(name), ".log")+".json") && strings.HasSuffix(name, ".log") {
			stamps[name] = info.ModTime().UTC().String() + "/" + strconv.FormatInt(info.Size(), 10)
			continue
		}
		if info.Size() > 128*1024*1024-total {
			return "", errors.New("assessment exceeds 128 MiB evidence limit")
		}
		body, err := readTestCampaignBundleBytes(name, 128*1024*1024-total)
		if err != nil {
			return "", err
		}
		total += int64(len(body))
		stamps[name] = testCampaignBundleHash(body)
	}
	return testCampaignJSONHash(stamps), nil
}

func testCampaignAssessBatchCleanup(plan testCampaignPolicyPlan, cp testCampaignCheckpoint, batch testCampaignCostBatch, scenario testScenario, directory string) (string, string, map[string]testCampaignCleanupAction, []string) {
	saved := cp.Batches[batch.ID]
	path := filepath.Join(directory, "batch-"+batch.ID+".json")
	roots, hash, err := readTestBaselineReport(path, nil)
	if err == nil {
		if saved.ReportSHA256 != "" && hash != saved.ReportSHA256 {
			return "invalid", "", nil, []string{"Report hash differs from checkpoint; journal fallback cannot bypass finalized evidence."}
		}
		var leaves []testRunReceipt
		seen := map[string]bool{}
		problems := []string{}
		for _, root := range roots {
			if testCampaignReceiptCleanupFailed(root, 0) {
				problems = append(problems, "Recorded cleanup errors remain blocking after workload deletion; fixture and consumer cleanup require manual inspection.")
			}
			collectTestFailureLeaves(root, "", &leaves)
		}
		for _, leaf := range leaves {
			if err := testCampaignRecoverableLeaf(leaf, batch, scenario, cp.Revision); err != nil {
				return "invalid", hash, nil, []string{"Receipt context, configuration, completion or source identity is unverified."}
			}
			if seen[leaf.RunID] {
				return "invalid", hash, nil, []string{"Receipt repeats a native run identity."}
			}
			seen[leaf.RunID] = true
		}
		targets, err := testCampaignReceiptCleanupTargets(scenario, leaves)
		if err == nil && len(targets) > 0 && len(targets) <= 1024 {
			return "receipt", hash, targets, problems
		}
	}
	if saved.Status == "done" || saved.ReportSHA256 != "" {
		return "invalid", hash, nil, []string{"Finalized report is missing, incomplete or lacks verified workload identities; inspect manually."}
	}
	targets, journalHash, journalErr := readTestCampaignJournalTargets(directory, testCampaignJSONHash(plan), cp, batch, scenario)
	if journalErr != nil {
		return "unresolved", "", nil, []string{"No usable cleanup identities: journal may be missing, malformed or contain an uncertain provisioning intent. Inspect the batch report and journal manually."}
	}
	if len(targets) == 0 || len(targets) > 1024 {
		return "unresolved", journalHash, nil, []string{"Journal contains no usable returned app identities or exceeds cleanup limits."}
	}
	return "journal", journalHash, targets, []string{"Journal identities support resource cleanup only; missing execution evidence remains blocking."}
}

func buildTestCampaignRecoveryAssessment(ctx context.Context, plan testCampaignPolicyPlan, cp testCampaignCheckpoint, status testCampaignStatusReport,
	scenarios map[string]testScenario, o testCampaignRecoveryAssessmentOptions, resolver *testCampaignBundleResolver, reader testCampaignCleanupReader) testCampaignRecoveryAssessment {
	result := testCampaignRecoveryAssessment{SchemaVersion: 1, Informational: true, ReadOnly: true, ObservedAt: time.Now().UTC(), SnapshotStable: status.SnapshotStable,
		LiveChecked: reader != nil, Ownership: "local", Recommendation: "inspect", Status: status, Blockers: []string{},
		Batches: []testCampaignRecoveryAssessmentBatch{}, Commands: []testCampaignRecoveryCommand{}, Notes: []string{
			"Assessment never deploys, executes commands, deletes resources, acquires locks or rewrites campaign state.",
			"Recorded identities are cleanup candidates; only live matching app IDs verify resources. Cleanup rechecks ownership and identities when invoked.",
			"Lock presence does not prove liveness. Use test policy recover-lock to inspect recorded coordinator and worker identities; unverifiable legacy locks still require manual investigation.",
			"POSIX shell commands are displayed only; argv arrays are the authoritative machine-readable form.",
			"Missing reports, fixture/consumer cleanup failures and uncertain provisioning outcomes cannot be waived by this assessment.",
		}}
	permitted := true
	if _, err := os.Lstat(filepath.Join(o.Directory, testCampaignCancelName)); err == nil {
		result.Notes = append(result.Notes, "A durable cancellation request is present. Wait for active work to drain; do not reset the request or checkpoint to resume.")
	}
	if status.LockPresent {
		permitted = false
		result.Recommendation = "wait"
		result.Blockers = append(result.Blockers, "Runner lock is present. Inspect it with test policy recover-lock; all recorded processes must be verified terminated before explicit lock recovery.")
	}
	if resolver != nil {
		result.Ownership = "inspection_only"
		if err := resolver.requireActive(); err != nil {
			permitted = false
			result.Blockers = append(result.Blockers, "Import lacks an active matching recipient claim; inspect the claim before recovery.")
		} else {
			result.Ownership = "active_recipient"
		}
		if _, err := os.Lstat(resolver.Index.ClaimPath + ".lock"); !os.IsNotExist(err) {
			permitted = false
			result.Blockers = append(result.Blockers, "Shared handoff claim is locked or unavailable.")
		}
	}
	if err := checkTestCampaignBundleWork(o.Directory, resolver, true); err != nil {
		permitted = false
		result.Ownership = "unavailable"
		result.Blockers = append(result.Blockers, "Campaign ownership is frozen, immutable, or belongs to another import.")
	}
	if plan.Recovery != nil {
		if err := validateTestCampaignRecoverySeal(plan, resolver); err != nil {
			permitted = false
			result.Blockers = append(result.Blockers, "Parent continuation seal does not authorize this plan.")
		}
	}
	for _, parent := range status.Lineage {
		if !parent.Verified {
			permitted = false
			result.Blockers = append(result.Blockers, "Recovery ancestry includes an unverified parent; inspect its retained evidence before continuing.")
		}
	}
	sealed := status.Sealed
	refreshBlocked := false
	if sealed {
		var seal testCampaignRecoverySeal
		if readTestCampaignJSON(filepath.Join(o.Directory, testCampaignRecoverySealName), &seal) == nil && seal.SchemaVersion == 1 &&
			seal.Blocked && seal.ParentPlanSHA256 == testCampaignJSONHash(plan) && seal.CheckpointSHA256 == testCampaignJSONHash(cp) {
			refreshBlocked = true
		}
	}
	reconcilePermitted := permitted && (!sealed || refreshBlocked)
	if sealed {
		permitted = false
		result.Recommendation = "sealed"
		result.Blockers = append(result.Blockers, "Reconciliation seal freezes this directory; use its verified continuation or inspect its blocked recovery bundle.")
	}
	if _, err := os.Lstat(filepath.Join(o.Directory, testCampaignContinuationOwnerName)); !os.IsNotExist(err) {
		permitted = false
		reconcilePermitted = false
		result.Blockers = append(result.Blockers, "A continuation ownership record exists or is unavailable; use the continuation owner.")
	}
	for _, issue := range status.Issues {
		if issue.Code == "evidence_conflict" || issue.Code == "snapshot_changed" {
			permitted = false
			reconcilePermitted = false
			result.Blockers = append(result.Blockers, "Conflicting or changing evidence prevents actionable recovery commands.")
		}
	}
	reads := 0
	for _, batch := range plan.Batches {
		if batch.Side != cp.Side {
			continue
		}
		saved, attempted := cp.Batches[batch.ID]
		if !attempted {
			continue
		}
		row := testCampaignRecoveryAssessmentBatch{ID: batch.ID, Resources: []testCampaignRecoveryResource{}, Blockers: []string{}, Commands: []testCampaignRecoveryCommand{}}
		evidence, hash, targets, problems := testCampaignAssessBatchCleanup(plan, cp, batch, scenarios[batch.Scenario], o.Directory)
		row.Evidence, row.EvidenceSHA256, row.Blockers = evidence, hash, append(row.Blockers, problems...)
		if permitted && (evidence == "invalid" || evidence == "unresolved") {
			result.Recommendation = "manual_inspection"
		}
		keys := make([]string, 0, len(targets))
		for key := range targets {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		liveValid := reader != nil
		anyPresent := false
		for _, key := range keys {
			target := targets[key]
			resource := testCampaignRecoveryResource{target.Project, target.RunID, target.AppID, "not_checked"}
			if reader != nil {
				if reads >= 1024 {
					resource.Live = "limit_reached"
					liveValid = false
				} else {
					reads++
					session, err := reader.GetDevSession(ctx, target.Project, target.RunID)
					if isNotFound(err) {
						resource.Live = "absent"
					} else if err != nil {
						resource.Live = "unknown"
						liveValid = false
					} else if session.App.ID != target.AppID {
						resource.Live = "identity_changed"
						liveValid = false
					} else {
						resource.Live = "present"
						anyPresent = true
					}
				}
			}
			row.Resources = append(row.Resources, resource)
		}
		if reader != nil && !liveValid {
			row.Blockers = append(row.Blockers, "Live inspection failed, reached its limit, or found a replacement app; no deletion command is suggested.")
		}
		if permitted && len(targets) > 0 {
			if reader == nil && result.Recommendation == "inspect" {
				result.Recommendation = "inspect_cleanup"
			}
			args := append(o.base("cleanup"), "--batch", batch.ID)
			if evidence == "journal" {
				args = append(args, "--journal")
			}
			row.Commands = append(row.Commands, newTestCampaignRecoveryCommand("Inspect workload sessions and save observations", true, args...))
			if liveValid && anyPresent {
				executeArgs := append(append([]string{}, args...), "--execute")
				row.Commands = append(row.Commands, newTestCampaignRecoveryCommand("Retry verified workload session cleanup", true, executeArgs...))
				result.Recommendation = "cleanup"
			}
		}
		if saved.Status == "running" || saved.Status == "uncertain" {
			row.Blockers = append(row.Blockers, "Checkpoint status does not prove process completion; confirm the worker is inactive.")
		}
		result.Batches = append(result.Batches, row)
	}
	if reconcilePermitted {
		args := o.base("reconcile")
		if o.From != "" {
			args = append(args, "--from", o.From)
		}
		if o.Baseline != "" {
			args = append(args, "--baseline", o.Baseline)
		}
		if _, err := os.Lstat(o.OutputDirectory); os.IsNotExist(err) {
			args = append(args, "--output-directory", o.OutputDirectory, "--check-cleanup")
			result.Commands = append(result.Commands, newTestCampaignRecoveryCommand("Reconcile after the worker is inactive and cleanup is resolved; creates a new recovery bundle", true, args...))
			if refreshBlocked {
				result.Recommendation = "refresh_blocked_reconciliation"
			} else if result.Recommendation == "inspect" {
				result.Recommendation = "reconcile"
			}
		} else {
			result.Blockers = append(result.Blockers, "Suggested reconciliation directory already exists or is unavailable; supply a new --output-directory.")
		}
	} else if result.Recommendation == "inspect" {
		result.Recommendation = "manual_inspection"
	}
	if !result.SnapshotStable {
		invalidateTestCampaignRecoveryAssessment(&result)
	}
	return result
}

func validateTestCampaignAssessmentRevision(cp testCampaignCheckpoint) error {
	raw, err := hex.DecodeString(cp.Revision)
	if err != nil || (len(raw) != 20 && len(raw) != 32) {
		return errors.New("checkpoint revision is invalid")
	}
	return nil
}
