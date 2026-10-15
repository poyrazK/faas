package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exportCampaignBundleTestFixture(t *testing.T, recovery bool) (campaignStatusTestFiles, testCampaignBundle, string) {
	t.Helper()
	f := newCampaignStatusTestFiles(t)
	recoveryPath := ""
	if recovery {
		recoveryPath = filepath.Join(f.root, "recovery")
		if code := cmdTestCampaignReconcile([]string{"--plan", f.planPath, "--policy", f.policyPath, "--manifest", f.manifestPath, "--directory", f.directory, "--output-directory", recoveryPath}); code != 0 {
			t.Fatalf("recovery setup failed: %d", code)
		}
	}
	output := filepath.Join(f.root, "handoff.json")
	args := []string{"--plan", f.planPath, "--policy", f.policyPath, "--manifest", f.manifestPath, "--directory", f.directory, "--output", output}
	if recoveryPath != "" {
		args = append(args, "--recovery-bundle", recoveryPath)
	}
	if code := cmdTestCampaignBundleExport(args); code != 0 {
		t.Fatalf("bundle export failed: %d", code)
	}
	bundle, err := readTestCampaignBundle(output)
	if err != nil {
		t.Fatal(err)
	}
	return f, bundle, output
}

func TestCampaignBundleFreezesSourceAndPreservesReceiptBytes(t *testing.T) {
	// adr: 984 — handoff transfers immutable evidence without granting a second source scheduler.
	f, bundle, _ := exportCampaignBundleTestFixture(t, false)
	original, err := os.ReadFile(filepath.Join(f.directory, "batch-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range bundle.Files {
		if strings.Contains(file.Path, "manifest") || strings.HasSuffix(file.Path, ".log") {
			t.Fatal("source configuration or logs were packaged")
		}
		if file.Path == "nodes/0/batch-a.json" {
			found = true
			if !bytes.Equal(file.Data, original) {
				t.Fatal("receipt bytes changed during export")
			}
		}
		if len(file.Data) == 0 {
			t.Fatal("bundle identity computation erased artifact data")
		}
	}
	if !found {
		t.Fatal("completed receipt missing")
	}
	if err := checkTestCampaignBundleWork(f.directory, nil, true); err == nil {
		t.Fatal("source ownership was not frozen")
	}
	err = runTestCampaignBatches(context.Background(), f.plan, f.scenarios, f.directory, &f.checkpoint, 1, func(testCampaignCostBatch) error { return nil },
		func(context.Context, testCampaignCostBatch, string) error {
			t.Fatal("handed-off source scheduled work")
			return nil
		})
	if err == nil {
		t.Fatal("source scheduler ignored handoff seal")
	}
}

func TestCampaignBundleRejectsTamperingTraversalAndMissingEvidence(t *testing.T) {
	// adr: 984 — inventory hashes and graph completeness precede materialization.
	_, bundle, _ := exportCampaignBundleTestFixture(t, false)
	copyBundle := func() testCampaignBundle {
		value := bundle
		value.Files = append([]testCampaignBundleFile{}, bundle.Files...)
		return value
	}
	changed := copyBundle()
	changed.Files[0].Data = append([]byte("changed"), changed.Files[0].Data...)
	if err := validateTestCampaignBundle(changed); err == nil {
		t.Fatal("changed bytes accepted")
	}
	changed = copyBundle()
	changed.Files[0].Path = "../outside.json"
	if err := validateTestCampaignBundle(changed); err == nil {
		t.Fatal("traversal path accepted")
	}
	changed = copyBundle()
	filtered := []testCampaignBundleFile{}
	for _, file := range changed.Files {
		if file.Path != "nodes/0/batch-a.json" {
			filtered = append(filtered, file)
		}
	}
	changed.Files = filtered
	changed.ID = testCampaignBundleID(changed)
	if err := validateTestCampaignBundle(changed); err == nil {
		t.Fatal("missing completed evidence accepted")
	}
	if err := checkTestCampaignBundleSensitive([]byte("{\"secrets\":{\"DATABASE_PASSWORD\":\"sensitive\"}}"), nil); err == nil {
		t.Fatal("credential configuration accepted")
	}
	if err := checkTestCampaignBundleSensitive([]byte("{\"body\":\"literal-sensitive-value\"}"), []string{"literal-sensitive-value"}); err == nil {
		t.Fatal("configured secret accepted")
	}
}

func TestCampaignBundleInspectionAndClaimedImportHaveSeparateAuthority(t *testing.T) {
	// adr: 984 — inspection is read-only; a shared exclusive claim owns one recipient and work directory.
	f, bundle, _ := exportCampaignBundleTestFixture(t, false)
	inspection, err := importTestCampaignBundle(bundle, filepath.Join(f.root, "inspect"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := loadTestCampaignBundleResolver(filepath.Join(inspection.Root, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.requireActive(); err == nil {
		t.Fatal("inspection import gained execution authority")
	}
	claims := filepath.Join(f.root, "claims")
	if err := os.Mkdir(claims, 0o700); err != nil {
		t.Fatal(err)
	}
	active, err := importTestCampaignBundle(bundle, filepath.Join(f.root, "recipient"), true, claims)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := loadTestCampaignBundleResolver(filepath.Join(active.Root, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.requireActive(); err != nil {
		t.Fatal(err)
	}
	if _, err := importTestCampaignBundle(bundle, filepath.Join(f.root, "second-recipient"), true, claims); err == nil {
		t.Fatal("duplicate recipient claim accepted")
	}
	release, err := acquireTestCampaignHandoff(owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireTestCampaignHandoff(owner); err == nil {
		t.Fatal("concurrent handoff ownership accepted")
	}
	work := filepath.Join(active.Root, "work")
	if err := registerTestCampaignHandoffWork(owner, bundle.PlanSHA256, work, bundle.Side, bundle.Revision); err != nil {
		t.Fatal(err)
	}
	if err := registerTestCampaignHandoffWork(owner, bundle.PlanSHA256, filepath.Join(f.root, "reset"), bundle.Side, bundle.Revision); err == nil {
		t.Fatal("plan work was reset into another directory")
	}
	if err := checkTestCampaignBundleWork(filepath.Join(active.Root, "artifacts", "nodes", "0"), owner, true); err == nil {
		t.Fatal("immutable evidence directory became writable work")
	}
	release()
	if err := enforceTestCampaignPlanHandoff(filepath.Join(active.Root, "artifacts", "plan.json"), nil); err == nil {
		t.Fatal("imported plan lost its ownership context")
	}
}

func TestCampaignBundleRelocatesRecoveryWithoutRewritingHashes(t *testing.T) {
	// adr: 984 — portable parent resolution preserves the original checkpoint and continuation identities.
	f, bundle, _ := exportCampaignBundleTestFixture(t, true)
	claims := filepath.Join(f.root, "claims")
	if err := os.Mkdir(claims, 0o700); err != nil {
		t.Fatal(err)
	}
	index, err := importTestCampaignBundle(bundle, filepath.Join(f.root, "recipient"), true, claims)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.directory, filepath.Join(f.root, "source-moved")); err != nil {
		t.Fatal(err)
	}
	resolver, err := loadTestCampaignBundleResolver(filepath.Join(index.Root, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var continuation testCampaignPolicyPlan
	if err := readTestCampaignJSON(filepath.Join(index.Root, "artifacts", "recovery", "continuation-plan.json"), &continuation); err != nil {
		t.Fatal(err)
	}
	if err := validateTestCampaignRecoverySeal(continuation, resolver); err != nil {
		t.Fatal(err)
	}
	lineage, complete, issues := testCampaignStatusTrace(continuation, resolver)
	if !complete || len(lineage) != 1 || !lineage[0].Verified || len(issues) != 0 ||
		lineage[0].Directory != filepath.Join(index.Root, "artifacts", "nodes", "0") {
		t.Fatalf("parent reference was not portable: %+v %+v", lineage, issues)
	}
	// Artifact mutations are caught when the import is reopened.
	path := filepath.Join(index.Root, "artifacts", "nodes", "0", "checkpoint.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTestCampaignBundleResolver(filepath.Join(index.Root, "bundle-index.json")); err == nil {
		t.Fatal("changed import artifact accepted")
	}
}

func TestCampaignBundlePreservesOrphanReservationsWithoutLogContents(t *testing.T) {
	// adr: 984 — excluded logs cannot turn uncertain work into unspent pending allocation.
	f := newCampaignStatusTestFiles(t)
	if err := os.WriteFile(filepath.Join(f.directory, "batch-b.log"), []byte("PRIVATE-LOG-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := collectTestCampaignBundle(f.plan, f.checkpoint, f.policy, f.scenarios, f.directory, "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Orphans) != 1 || bundle.Orphans[0].Batch != "b" {
		t.Fatal("orphan reservation metadata lost")
	}
	for _, file := range bundle.Files {
		if bytes.Contains(file.Data, []byte("PRIVATE-LOG-CONTENT")) {
			t.Fatal("excluded log content was exported")
		}
	}
	index, err := importTestCampaignBundle(bundle, filepath.Join(f.root, "inspect"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint testCampaignCheckpoint
	if err := readTestCampaignJSON(filepath.Join(index.Root, "work", "checkpoint.json"), &checkpoint); err != nil {
		t.Fatal(err)
	}
	report, err := buildTestCampaignStatus(f.plan, &checkpoint, filepath.Join(index.Root, "work"), "", f.policy, f.scenarios, nil, nil)
	if err != nil || report.Reserved.Runs != 2 || report.Remaining.Runs != 0 {
		t.Fatalf("orphan was refunded: %+v %v", report, err)
	}
}

func TestCampaignContinuationBindsOneNativeWorkDirectory(t *testing.T) {
	// adr: 984 — exporting an ancestor must not reset an already claimed continuation.
	f := newCampaignStatusTestFiles(t)
	recovery := filepath.Join(f.root, "recovery")
	if code := cmdTestCampaignReconcile([]string{"--plan", f.planPath, "--policy", f.policyPath, "--manifest", f.manifestPath, "--directory", f.directory, "--output-directory", recovery}); code != 0 {
		t.Fatal("recovery setup failed")
	}
	var continuation testCampaignPolicyPlan
	if err := readTestCampaignJSON(filepath.Join(recovery, "continuation-plan.json"), &continuation); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(f.root, "continuation-work")
	if err := registerTestCampaignContinuationOwner(continuation, work, nil); err != nil {
		t.Fatal(err)
	}
	if err := registerTestCampaignContinuationOwner(continuation, work, nil); err != nil {
		t.Fatal("matching owner cannot resume")
	}
	if err := registerTestCampaignContinuationOwner(continuation, filepath.Join(f.root, "reset"), nil); err == nil {
		t.Fatal("continuation reset into another directory")
	}
	if code := cmdTestCampaignBundleExport([]string{"--plan", f.planPath, "--policy", f.policyPath, "--manifest", f.manifestPath, "--directory", f.directory, "--recovery-bundle", recovery, "--output", filepath.Join(f.root, "ancestor.json")}); code != 1 {
		t.Fatal("ancestor of a claimed continuation was exported")
	}
}

func TestCampaignBundleRefusesDuplicateKeysAndRawPayloadCaptures(t *testing.T) {
	// adr: 984 — credential checks cannot be bypassed by hidden duplicate fields.
	if err := checkTestCampaignBundleJSONKeys([]byte("{\"body\":\"private\",\"body\":\"\"}")); err == nil {
		t.Fatal("duplicate artifact keys accepted")
	}
	if err := checkTestCampaignBundleSensitive([]byte("{\"response_body\":\"application-private-data\"}"), nil); err == nil {
		t.Fatal("raw response payload accepted")
	}
}
