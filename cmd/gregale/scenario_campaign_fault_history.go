package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Source paths are resolved through the import index when present. They are
// provenance labels in observations, never instructions to reopen sender paths.
type testCampaignFaultSource struct {
	Directory        string                               `json:"directory"`
	PlanSHA256       string                               `json:"plan_sha256"`
	CheckpointSHA256 string                               `json:"checkpoint_sha256"`
	BatchID          string                               `json:"batch_id"`
	JournalSHA256    string                               `json:"journal_sha256"`
	Journals         []testCampaignRunJournal             `json:"-"`
	Targets          map[string]testCampaignCleanupAction `json:"-"`
}

type testCampaignFaultHistory struct {
	SchemaVersion  int                            `json:"schema_version"`
	PlanSHA256     string                         `json:"plan_sha256"`
	SnapshotSHA256 string                         `json:"snapshot_sha256"`
	Executed       bool                           `json:"executed"`
	Complete       bool                           `json:"operation_complete"`
	Sources        []testCampaignFaultSource      `json:"sources"`
	Faults         []testCampaignFaultObservation `json:"fault_resources"`
	Sessions       []testCampaignCleanupAction    `json:"sessions"`
}

func collectTestCampaignFaultHistory(plan testCampaignPolicyPlan, cp testCampaignCheckpoint, scenarios map[string]testScenario, directory string, resolver *testCampaignBundleResolver) ([]testCampaignFaultSource, error) {
	if cp.PlanSHA256 != testCampaignJSONHash(plan) || !testCampaignCheckpointLineageMatches(cp, plan) {
		return nil, errors.New("fault history checkpoint differs from plan")
	}
	if resolver != nil {
		fresh, err := loadTestCampaignBundleResolver(resolver.IndexPath)
		if err != nil {
			return nil, err
		}
		if testCampaignJSONHash(fresh.Index) != testCampaignJSONHash(resolver.Index) {
			return nil, errors.New("handoff index changed")
		}
		var claim testCampaignBundleClaim
		if err := readTestCampaignJSON(resolver.Index.ClaimPath, &claim); err != nil {
			return nil, err
		}
		hash := testCampaignJSONHash(plan)
		rootWork := hash == resolver.Index.PlanSHA256 && directory == filepath.Join(resolver.Index.Root, "work")
		if (claim.Work[hash] != "" && claim.Work[hash] != directory) || (!rootWork && claim.Work[hash] != directory) || cp.Side != resolver.Index.Side || cp.Revision != resolver.Index.Revision {
			return nil, errors.New("fault history belongs to another recipient work directory")
		}
	}
	rows, complete, _ := testCampaignStatusTrace(plan, resolver)
	if !complete {
		return nil, errors.New("fault history has unverified, legacy or cyclic recovery links")
	}
	if err := validateTestCampaignRecoverySeal(plan, resolver); err != nil {
		return nil, err
	}
	if err := checkTestCampaignBundleWork(directory, resolver, true); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(directory, testCampaignContinuationOwnerName)); !os.IsNotExist(err) {
		return nil, errors.New("fault cleanup belongs to a later continuation")
	}

	dirs := []string{directory}
	for _, row := range rows {
		dirs = append(dirs, row.Directory)
	}
	sources := []testCampaignFaultSource{}
	seenRuns := map[string]bool{}
	totalTargets := 0
	var totalBytes int64
	seenDirs := map[string]bool{}
	for index, dir := range dirs {
		if seenDirs[filepath.Clean(dir)] {
			return nil, errors.New("fault history repeats a directory")
		}
		seenDirs[filepath.Clean(dir)] = true
		var saved testCampaignCheckpoint
		if err := readTestCampaignJSON(filepath.Join(dir, "checkpoint.json"), &saved); err != nil {
			return nil, err
		}
		if saved.SchemaVersion != 1 || saved.LineageVersion != 1 {
			return nil, errors.New("fault history has an unknown checkpoint boundary")
		}
		if index == 0 && testCampaignJSONHash(saved) != testCampaignJSONHash(cp) {
			return nil, errors.New("current checkpoint changed")
		}
		if saved.Recovery != nil && saved.Recovery.Blocked {
			return nil, errors.New("blocked ancestor cannot authorize cleanup")
		}
		if index > 0 {
			if err := checkTestCampaignBundleWork(dir, resolver, false); err != nil {
				return nil, err
			}
			if _, err := os.Lstat(filepath.Join(dir, "runner.lock")); !os.IsNotExist(err) {
				return nil, errors.New("ancestor runner lock prevents fault cleanup")
			}
		}
		if resolver == nil && index > 0 {
			var owner testCampaignContinuationOwner
			childHash := cp.PlanSHA256
			if index > 1 {
				var child testCampaignCheckpoint
				if err := readTestCampaignJSON(filepath.Join(dirs[index-1], "checkpoint.json"), &child); err != nil {
					return nil, err
				}
				childHash = child.PlanSHA256
			}
			expected := testCampaignContinuationOwner{SchemaVersion: 1, PlanSHA256: childHash, Directory: dirs[index-1], HostSHA256: testCampaignBundleHost()}
			if readTestCampaignJSON(filepath.Join(dir, testCampaignContinuationOwnerName), &owner) != nil || owner != expected {
				return nil, errors.New("ancestor continuation owner changed")
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			n := entry.Name()
			if strings.HasPrefix(n, "batch-") && (strings.HasSuffix(n, ".journal") || strings.HasSuffix(n, ".log")) {
				id := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(n, "batch-"), ".journal"), ".log")
				if _, ok := saved.Batches[id]; !ok {
					return nil, errors.New("uncheckpointed fault work requires manual inspection")
				}
			}
		}
		if len(saved.Batches) > 1024 {
			return nil, errors.New("fault history exceeds batch bounds")
		}
		ids := []string{}
		for id := range saved.Batches {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if !testCampaignPortableJournalDirectory.MatchString("batch-" + id + ".journal") {
				return nil, errors.New("invalid history batch ID")
			}
			if len(sources) >= 1024 {
				return nil, errors.New("fault history exceeds source bounds")
			}
			state := saved.Batches[id].Status
			if state != "done" && state != "uncertain" {
				return nil, errors.New("fault history contains unfinished or unknown worker state")
			}
			var binding testCampaignJournalBinding
			if err := readTestCampaignJSON(filepath.Join(testCampaignJournalDirectory(dir, id), "binding.json"), &binding); err != nil {
				return nil, errors.New("fault history lacks a complete journal inventory")
			}
			scenario, ok := scenarios[binding.Scenario]
			if !ok {
				return nil, errors.New("ancestor scenario is absent from the bound manifest")
			}
			batch := testCampaignCostBatch{ID: id, Scenario: binding.Scenario, Profile: binding.Profile, Side: saved.Side}
			portable := resolver != nil && testCampaignBundleImmutableDirectory(dir, resolver)
			expectedPortableHash := ""
			if portable {
				var err error
				if expectedPortableHash, err = validateTestCampaignFaultPortableInventory(testCampaignJournalDirectory(dir, id)); err != nil {
					return nil, err
				}
			}
			targets, hash, err := readTestCampaignJournalTargets(dir, saved.PlanSHA256, saved, batch, scenario, portable)
			if err != nil {
				return nil, err
			}
			if state == "done" {
				if err := validateTestCampaignFaultFinalReport(dir, saved, batch, scenario, targets); err != nil {
					return nil, err
				}
			}
			journals, err := readTestCampaignFaultJournals(dir, id, scenario, portable)
			if err != nil {
				return nil, err
			}
			if testCampaignJSONHash(journals) != hash || (portable && hash != expectedPortableHash) {
				return nil, errors.New("fault journal changed while captured")
			}
			body, err := json.Marshal(journals)
			if err != nil {
				return nil, err
			}
			totalBytes += int64(len(body))
			if totalBytes > testCampaignBundleRawLimit {
				return nil, errors.New("fault history exceeds 64 MiB evidence limit")
			}
			for _, journal := range journals {
				if seenRuns[journal.RunID] {
					return nil, errors.New("fault history repeats a run identity")
				}
				seenRuns[journal.RunID] = true
			}
			totalTargets += len(targets)
			if totalTargets > 1024 || len(seenRuns) > 1024 {
				return nil, errors.New("fault history exceeds 1024 resource identities")
			}
			sources = append(sources, testCampaignFaultSource{dir, saved.PlanSHA256, testCampaignJSONHash(saved), id, hash, journals, targets})
		}
	}
	return sources, nil
}

func testCampaignFaultHistoryJournals(sources []testCampaignFaultSource) []testCampaignRunJournal {
	journals := []testCampaignRunJournal{}
	for _, source := range sources {
		journals = append(journals, source.Journals...)
	}
	return journals
}
func cleanupTestCampaignFaultHistory(ctx context.Context, client interface {
	testCampaignFaultClient
	testCampaignCleanupClient
}, plan testCampaignPolicyPlan, cp testCampaignCheckpoint, scenarios map[string]testScenario, directory string, resolver *testCampaignBundleResolver, execute bool) (result testCampaignFaultHistory, err error) {
	result = testCampaignFaultHistory{SchemaVersion: 1, PlanSHA256: testCampaignJSONHash(plan), Executed: execute}
	sources, err := collectTestCampaignFaultHistory(plan, cp, scenarios, directory, resolver)
	if err != nil {
		return result, err
	}
	result.Sources = sources
	result.SnapshotSHA256 = testCampaignJSONHash(sources)
	defer func() {
		path := filepath.Join(directory, "fault-history-cleanup-"+time.Now().UTC().Format("20060102T150405.000000000")+".json")
		if saveErr := persistTestCampaignProcess(path, result); saveErr != nil && err == nil {
			err = saveErr
		}
	}()
	stable := func() error {
		fresh, err := collectTestCampaignFaultHistory(plan, cp, scenarios, directory, resolver)
		if err != nil {
			return err
		}
		if testCampaignJSONHash(fresh) != result.SnapshotSHA256 {
			return errors.New("fault history changed during cleanup")
		}
		return nil
	}
	journals := testCampaignFaultHistoryJournals(sources)
	targets := map[string]testCampaignCleanupAction{}
	for _, source := range sources {
		for key, target := range source.Targets {
			targets[key] = target
		}
	}
	result.Faults, err = inspectTestCampaignFaults(ctx, client, journals, false, false)
	if err != nil {
		return result, err
	}
	if len(targets) > 0 {
		result.Sessions, err = testCampaignRetryCleanupTargets(ctx, client, targets, false)
		if err != nil {
			return result, err
		}
	}
	if err = stable(); err != nil {
		return result, err
	}
	if execute {
		result.Faults, err = inspectTestCampaignFaults(ctx, client, journals, true, false)
		if err != nil {
			return result, err
		}
		if err = stable(); err != nil {
			return result, err
		}
		if len(targets) > 0 {
			result.Sessions, err = testCampaignRetryCleanupTargets(ctx, client, targets, true)
			if err != nil {
				return result, err
			}
		}
		if err = stable(); err != nil {
			return result, err
		}
		result.Faults, err = inspectTestCampaignFaults(ctx, client, journals, true, true)
		if err != nil {
			return result, err
		}
	}
	if err = stable(); err != nil {
		return result, err
	}
	result.Complete = true
	return result, nil
}

func validateTestCampaignFaultPortableInventory(directory string) (string, error) {
	var inventory testCampaignPortableJournalInventory
	if err := readTestCampaignJSON(filepath.Join(directory, "inventory.json"), &inventory); err != nil {
		return "", err
	}
	if inventory.SchemaVersion != 1 || len(inventory.Files) == 0 || len(inventory.Files) > 1025 {
		return "", errors.New("invalid ancestor journal inventory")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	if len(entries) != len(inventory.Files)+1 {
		return "", errors.New("ancestor journal membership differs from portable inventory")
	}
	seen := map[string]bool{}
	snapshots := map[string]testCampaignRunJournal{}
	names := []string{}
	for _, file := range inventory.Files {
		if seen[file.Path] || (file.Path != "binding.json" && !testCampaignPortableJournalRun.MatchString(file.Path)) {
			return "", errors.New("unsafe ancestor journal inventory")
		}
		seen[file.Path] = true
		body, err := readTestCampaignBundleBytes(filepath.Join(directory, file.Path), 8*1024*1024)
		if err != nil || testCampaignBundleHash(body) != file.SHA256 {
			return "", errors.New("ancestor journal differs from portable inventory")
		}
		if file.Path != "binding.json" {
			var journal testCampaignRunJournal
			if err := decodeTestCampaignBundleJSON(body, &journal); err != nil {
				return "", err
			}
			snapshots[file.Path] = journal
			names = append(names, file.Path)
		}
	}
	sort.Strings(names)
	journals := []testCampaignRunJournal{}
	for _, name := range names {
		journals = append(journals, snapshots[name])
	}
	return testCampaignJSONHash(journals), nil
}

func validateTestCampaignFaultFinalReport(directory string, cp testCampaignCheckpoint, batch testCampaignCostBatch, scenario testScenario, targets map[string]testCampaignCleanupAction) error {
	roots, hash, err := readTestBaselineReport(filepath.Join(directory, "batch-"+batch.ID+".json"), nil)
	if err != nil || hash != cp.Batches[batch.ID].ReportSHA256 {
		return errors.New("finalized ancestor report is missing or changed")
	}
	leaves := []testRunReceipt{}
	for _, root := range roots {
		collectTestFailureLeaves(root, "", &leaves)
	}
	for _, leaf := range leaves {
		if err := testCampaignRecoverableLeaf(leaf, batch, scenario, cp.Revision); err != nil {
			return err
		}
	}
	receiptTargets, err := testCampaignReceiptCleanupTargets(scenario, leaves)
	if err != nil {
		return err
	}
	if len(targets) != len(receiptTargets) {
		return errors.New("finalized report and fault inventory disagree")
	}
	for key, target := range targets {
		if receiptTargets[key].AppID != target.AppID {
			return errors.New("finalized report and fault identities disagree")
		}
	}
	return nil
}

func testCampaignFaultAncestorStampPaths(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, entry := range entries {
		if !testCampaignPortableJournalDirectory.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(directory, entry.Name())
		if err := testCampaignPrivateDirectory(dir); err != nil {
			return nil, err
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		if len(files) > 1026 {
			return nil, errors.New("ancestor journal exceeds snapshot bounds")
		}
		paths = append(paths, dir)
		for _, file := range files {
			if !file.Type().IsRegular() {
				return nil, errors.New("ancestor journal contains nonregular evidence")
			}
			paths = append(paths, filepath.Join(dir, file.Name()))
		}
		if len(paths) > 8192 {
			return nil, errors.New("ancestor evidence exceeds snapshot bounds")
		}
	}
	return paths, nil
}
