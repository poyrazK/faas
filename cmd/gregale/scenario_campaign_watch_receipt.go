package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const testCampaignWatchReceiptName = "cleanup-watch-receipt.json"
const testCampaignWatchOwnerLimit = 32

type testCampaignWatchPaths struct {
	Plan, Policy, Manifest, Directory, Handoff string
}

type testCampaignWatchProgress struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type testCampaignWatchReceipt struct {
	SchemaVersion    int                           `json:"schema_version"`
	BindingSHA256    string                        `json:"binding_sha256"`
	CheckpointSHA256 string                        `json:"checkpoint_sha256"`
	InventorySHA256  string                        `json:"inventory_sha256"`
	Owner            testCampaignProcessIdentity   `json:"owner"`
	PreviousOwners   []testCampaignProcessIdentity `json:"previous_owners"`
	Progress         *testCampaignWatchProgress    `json:"progress,omitempty"`
	Attempts         int                           `json:"cleanup_attempts"`
	Complete         bool                          `json:"operation_complete"`
}

type testCampaignWatchRecovery struct {
	Paths          testCampaignWatchPaths
	EndpointSHA256 string
	BindingSHA256  string
	Receipt        *testCampaignWatchReceipt
}

func (w *testCampaignWatchRecovery) inputs() (testCampaignPolicyPlan, testCampaignCheckpoint, map[string]testScenario, *testCampaignBundleResolver, string, error) {
	var plan testCampaignPolicyPlan
	var cp testCampaignCheckpoint
	fail := func(err error) (testCampaignPolicyPlan, testCampaignCheckpoint, map[string]testScenario, *testCampaignBundleResolver, string, error) {
		return plan, cp, nil, nil, "", err
	}
	if err := testCampaignPrivateDirectory(w.Paths.Directory); err != nil {
		return fail(err)
	}
	client, err := authedClient()
	if err != nil {
		return fail(err)
	}
	if testCampaignJSONHash(client.BaseURL()) != w.EndpointSHA256 {
		return fail(errors.New("watch API endpoint changed"))
	}
	if err := readTestCampaignJSON(w.Paths.Plan, &plan); err != nil {
		return fail(err)
	}
	policy, err := readTestCampaignPolicy(w.Paths.Policy)
	if err != nil {
		return fail(err)
	}
	manifest, _, err := readTestManifestDocument(w.Paths.Manifest, func(_ string, s testScenario) []string { return testHTTPDataFields(s) })
	if err != nil {
		return fail(err)
	}
	if err := validateTestCampaignRunnable(plan, manifest.Scenarios, policy); err != nil {
		return fail(err)
	}
	resolver, err := loadTestCampaignBundleResolver(w.Paths.Handoff)
	if err != nil {
		return fail(err)
	}
	if err := resolver.requireActive(); err != nil {
		return fail(err)
	}
	if err := enforceTestCampaignPlanHandoff(w.Paths.Plan, resolver); err != nil {
		return fail(err)
	}
	if err := checkTestCampaignBundleWork(w.Paths.Directory, resolver, true); err != nil {
		return fail(err)
	}
	if err := readTestCampaignJSON(filepath.Join(w.Paths.Directory, "checkpoint.json"), &cp); err != nil {
		return fail(err)
	}
	if cp.SchemaVersion != 1 || cp.PlanSHA256 != testCampaignJSONHash(plan) || !testCampaignCheckpointLineageMatches(cp, plan) {
		return fail(errors.New("watch checkpoint binding changed"))
	}
	if err := validateTestCampaignAssessmentRevision(cp); err != nil {
		return fail(err)
	}
	indexHash := ""
	if resolver != nil {
		indexHash = testCampaignJSONHash(resolver.Index)
	}
	binding := testCampaignJSONHash(struct {
		Paths                                   testCampaignWatchPaths
		Plan, Policy, Manifest, Index, Endpoint string
	}{w.Paths, testCampaignJSONHash(plan), testCampaignJSONHash(policy), testCampaignJSONHash(manifest.Scenarios), indexHash, w.EndpointSHA256})
	return plan, cp, manifest.Scenarios, resolver, binding, nil
}

func (w *testCampaignWatchRecovery) snapshot() (string, string, error) {
	plan, cp, scenarios, resolver, binding, err := w.inputs()
	if err != nil {
		return "", "", err
	}
	if binding != w.BindingSHA256 {
		return "", "", errors.New("watch inputs or handoff changed")
	}
	if !cp.Halted {
		return "", "", errors.New("watch recovery requires a halted checkpoint")
	}
	for _, batch := range cp.Batches {
		if batch.Status == "running" {
			return "", "", errors.New("watch recovery cannot adopt running work")
		}
	}
	sources, err := collectTestCampaignFaultHistory(plan, cp, scenarios, w.Paths.Directory, resolver)
	if err != nil {
		return "", "", err
	}
	return testCampaignJSONHash(cp), testCampaignJSONHash(sources), nil
}

func (w *testCampaignWatchRecovery) load() error {
	var receipt testCampaignWatchReceipt
	err := readTestCampaignJSON(filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName), &receipt)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	w.Receipt = &receipt
	return w.validate(true)
}

// A private, versioned receipt binds immutable inventory, not cached absence.
// Every resumed cleanup still reads the authenticated API before any mutation.
func (w *testCampaignWatchRecovery) validate(requireTerminated bool) error {
	r := w.Receipt
	if r == nil || r.SchemaVersion != 1 || r.BindingSHA256 != w.BindingSHA256 || len(r.PreviousOwners) >= testCampaignWatchOwnerLimit || r.Attempts < 0 {
		return errors.New("invalid watcher recovery receipt")
	}
	checkpoint, inventory, err := w.snapshot()
	if err != nil {
		return err
	}
	if checkpoint != r.CheckpointSHA256 || inventory != r.InventorySHA256 {
		return errors.New("watch checkpoint or resource inventory changed")
	}
	seen := map[testCampaignProcessIdentity]bool{}
	for i, owner := range append(append([]testCampaignProcessIdentity{}, r.PreviousOwners...), r.Owner) {
		if seen[owner] {
			return errors.New("duplicate watcher ownership identity")
		}
		seen[owner] = true
		status, _ := probeTestCampaignProcess(owner)
		if i < len(r.PreviousOwners) || requireTerminated {
			if status != "terminated" {
				return errors.New("prior watcher termination is not verified")
			}
		} else if owner.PID != os.Getpid() || status != "alive" {
			return errors.New("watch receipt is owned by another process")
		}
	}
	if r.Progress != nil {
		if err := w.verifyProgress(*r.Progress); err != nil {
			return err
		}
	}
	var saved testCampaignWatchReceipt
	if err := readTestCampaignJSON(filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName), &saved); err != nil {
		return err
	}
	if testCampaignJSONHash(saved) != testCampaignJSONHash(r) {
		return errors.New("watch receipt changed during recovery")
	}
	return nil
}

func (w *testCampaignWatchRecovery) persist() error {
	return persistTestCampaignProcess(filepath.Join(w.Paths.Directory, testCampaignWatchReceiptName), w.Receipt)
}

func (w *testCampaignWatchRecovery) create() error {
	checkpoint, inventory, err := w.snapshot()
	if err != nil {
		return err
	}
	owner, err := readTestCampaignProcessIdentity(os.Getpid())
	if err != nil {
		return err
	}
	w.Receipt = &testCampaignWatchReceipt{SchemaVersion: 1, BindingSHA256: w.BindingSHA256, CheckpointSHA256: checkpoint, InventorySHA256: inventory, Owner: owner, PreviousOwners: []testCampaignProcessIdentity{}}
	return w.persist()
}

func (w *testCampaignWatchRecovery) transfer() error {
	if err := w.validate(true); err != nil {
		return err
	}
	if len(w.Receipt.PreviousOwners)+1 >= testCampaignWatchOwnerLimit {
		return errors.New("watch ownership recovery history exhausted; inspect manually")
	}
	owner, err := readTestCampaignProcessIdentity(os.Getpid())
	if err != nil {
		return err
	}
	w.Receipt.PreviousOwners = append(w.Receipt.PreviousOwners, w.Receipt.Owner)
	w.Receipt.Owner = owner
	return w.persist()
}

func (w *testCampaignWatchRecovery) verifyProgress(progress testCampaignWatchProgress) error {
	if filepath.Base(progress.Name) != progress.Name || !strings.HasPrefix(progress.Name, "fault-history-cleanup-") || !strings.HasSuffix(progress.Name, ".json") {
		return errors.New("invalid watcher progress reference")
	}
	var result testCampaignFaultHistory
	if err := readTestCampaignJSON(filepath.Join(w.Paths.Directory, progress.Name), &result); err != nil {
		return err
	}
	if result.SchemaVersion != 1 || !result.Executed || result.PlanSHA256 != w.currentPlanHash() || result.SnapshotSHA256 != w.Receipt.InventorySHA256 || testCampaignJSONHash(result) != progress.SHA256 {
		return errors.New("watch cleanup progress changed")
	}
	return nil
}

func (w *testCampaignWatchRecovery) saveProgress(complete bool) error {
	if err := w.validate(false); err != nil {
		return err
	}
	entries, err := os.ReadDir(w.Paths.Directory)
	if err != nil {
		return err
	}
	if len(entries) > 8192 {
		return errors.New("watch work directory exceeds recovery bounds")
	}
	for i := len(entries) - 1; i >= 0; i-- {
		name := entries[i].Name()
		if !strings.HasPrefix(name, "fault-history-cleanup-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		var result testCampaignFaultHistory
		if err := readTestCampaignJSON(filepath.Join(w.Paths.Directory, name), &result); err != nil {
			return err
		}
		if result.SnapshotSHA256 != w.Receipt.InventorySHA256 {
			return errors.New("watch cleanup produced changed inventory")
		}
		progress := testCampaignWatchProgress{Name: name, SHA256: testCampaignJSONHash(result)}
		if err := w.verifyProgress(progress); err != nil {
			return err
		}
		w.Receipt.Progress = &progress
		break
	}
	w.Receipt.Attempts++
	w.Receipt.Complete = complete
	return w.persist()
}

func newTestCampaignWatchRecovery(paths testCampaignWatchPaths) (*testCampaignWatchRecovery, error) {
	fields := []*string{&paths.Plan, &paths.Policy, &paths.Manifest, &paths.Directory, &paths.Handoff}
	for _, field := range fields {
		if *field != "" {
			absolute, err := filepath.Abs(*field)
			if err != nil {
				return nil, err
			}
			*field = absolute
		}
	}
	client, err := authedClient()
	if err != nil {
		return nil, err
	}
	w := &testCampaignWatchRecovery{Paths: paths, EndpointSHA256: testCampaignJSONHash(client.BaseURL())}
	_, _, _, _, binding, err := w.inputs()
	if err != nil {
		return nil, err
	}
	w.BindingSHA256 = binding
	return w, nil
}
