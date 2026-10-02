// adr: 435 — environment intent and runtime ownership contracts.
package sched

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func scopedSecretRuntimeFixture(t *testing.T) (*state.MemStore, state.Account, state.App, state.Deployment) {
	t.Helper()
	store := state.NewMemStore()
	account, _, _ := seedApp(t, store, api.PlanPro, 256, 5)
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "secret-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "secret-api", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5, Status: state.AppActive, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "staging"} {
		for _, key := range []string{"DATABASE_A", "DATABASE_B", "LEGACY"} {
			if err := store.UpsertAppSecretInScope(t.Context(), account.ID, app.ID, scope, key, []byte(scope+"-sealed-"+key)); err != nil {
				t.Fatal(err)
			}
		}
		target := "secret:DATABASE_B"
		if scope == "staging" {
			target = "secret:DATABASE_A"
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), account.ID, app.ID, scope, "DATABASE_URL", target); err != nil {
			t.Fatal(err)
		}
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive, OverrideEnvSecrets: json.RawMessage(`{"DATABASE_URL":"secret:DATABASE_A","SECOND_ALIAS":"secret:DATABASE_B"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return store, account, app, dep
}

func TestEnvironmentSecretReferencesResolveSourceNamesAndPreserveLegacy(t *testing.T) {
	store, account, app, dep := scopedSecretRuntimeFixture(t)
	engine := &Engine{store: store, log: testLog()}
	loaded, err := engine.loadDeploymentSealedEnvDelivery(t.Context(), account.ID, app.ID, dep)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AllSecrets || len(loaded.Entries) != 2 || len(loaded.Candidates) != 1 || loaded.Candidates[0].Key != "DATABASE_B" {
		t.Fatalf("alias selection did not deduplicate source: %+v", loaded)
	}
	for _, entry := range loaded.Entries {
		if entry.SourceKey != "DATABASE_B" || string(entry.Ciphertext) != "production-sealed-DATABASE_B" {
			t.Fatal("alias used destination name rather than selected source")
		}
	}
	refs := map[string]string{"DATABASE_URL": "secret:DATABASE_B", "SECOND_ALIAS": "secret:DATABASE_B"}
	if !maps.Equal(loaded.References, refs) {
		t.Fatalf("mapping evidence: %+v", loaded.References)
	}
	legacy, err := engine.loadSealedEnvDeliveryFor(t.Context(), account.ID, app.ID, "production", nil)
	if err != nil || !legacy.AllSecrets || len(legacy.Entries) != 4 || legacy.References["LEGACY"] != "secret:LEGACY" || legacy.References["DATABASE_URL"] != "secret:DATABASE_B" {
		t.Fatalf("managed reference removed unmanaged legacy inputs: %+v %v", legacy, err)
	}
	sidecar, err := engine.resolveSealedEnvDeliveryFor(t.Context(), account.ID, app.ID, "production", map[string]string{"DATABASE_A": "secret:DATABASE_A"}, false)
	if err != nil || len(sidecar.Entries) != 1 || sidecar.Entries[0].Key != "DATABASE_A" || string(sidecar.Entries[0].Ciphertext) != "production-sealed-DATABASE_A" {
		t.Fatalf("primary references expanded sidecar access: %+v %v", sidecar, err)
	}
}

func TestEnvironmentSecretReferencesWakeAndMigrationCarryMappingProof(t *testing.T) {
	store, account, app, dep := scopedSecretRuntimeFixture(t)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	wake, err := engine.Wake(t.Context(), app.ID, dep.ID, "production", TriggerAppWake)
	if err != nil {
		t.Fatal(err)
	}
	receipt, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), wake.InstanceID)
	if err != nil || !exists || receipt.SecretRefs["DATABASE_URL"] != "secret:DATABASE_B" || receipt.SecretVersions["production/DATABASE_B"] < 1 || len(receipt.SecretVersions) != 1 {
		t.Fatalf("wake lost actual mapping: %+v %v %v", receipt, exists, err)
	}
	if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, receipt); err != nil || !fresh {
		t.Fatalf("boot proof rejected: %v %v", fresh, err)
	}
	for _, entry := range vmm.lastColdBootSpec.SealedEnv {
		if entry.SourceKey != "DATABASE_B" || string(entry.Ciphertext) != "production-sealed-DATABASE_B" {
			t.Fatal("boot delivered wrong scoped source")
		}
	}
	if err := store.PutAppEnvironmentSecretReference(t.Context(), account.ID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_A"); err != nil {
		t.Fatal(err)
	}
	if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, receipt); err != nil || fresh {
		t.Fatalf("old mapping remained fresh after intent change: %v %v", fresh, err)
	}
	spec, err := engine.BuildAppSpecForMigration(t.Context(), wake.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if spec.migrationRuntime == nil || spec.migrationRuntime.Cold.SecretRefs["DATABASE_URL"] != "secret:DATABASE_A" || spec.migrationRuntime.Restored == nil || spec.migrationRuntime.Restored.SecretRefs["DATABASE_URL"] != "secret:DATABASE_B" {
		t.Fatalf("migration confused captured and cold inputs: %+v", spec.migrationRuntime)
	}
	if len(spec.SealedEnv) != 2 || string(spec.SealedEnv[0].Ciphertext) != "production-sealed-DATABASE_A" {
		t.Fatal("migration cold fallback used stale mapping")
	}
}

func TestEnvironmentSecretReferencesPrimeCapturesMappingProof(t *testing.T) {
	store, _, app, dep := scopedSecretRuntimeFixture(t)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.Prime(t.Context(), app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	instances, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 1 {
		t.Fatalf("prime source: %+v %v", instances, err)
	}
	instance := instances[0]
	// Simulate imaged publishing the scheduler's snapshot_written event.
	snapshot, err := store.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: state.SnapMemKey(dep.ID)}, instance.ID, instance.StartedAt)
	if err != nil {
		t.Fatal(err)
	}
	receipt, exists, err := store.SnapshotRuntimeConfigReceipt(t.Context(), snapshot.ID)
	if err != nil || !exists || receipt.SecretRefs["DATABASE_URL"] != "secret:DATABASE_B" {
		t.Fatalf("prime lost mapping: %+v %v %v", receipt, exists, err)
	}
	if len(vmm.lastColdBootSpec.SealedEnv) != 2 || vmm.coldBoots != 1 || vmm.snapshots != 1 {
		t.Fatalf("prime payload: %+v", vmm.lastColdBootSpec.SealedEnv)
	}
}

func TestEnvironmentSecretReferencesCorruptIntentNeverExpandsDelivery(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`{"DATABASE_URL":`), json.RawMessage(`{"DATABASE_URL": "malformed"}`)} {
		t.Run(string(raw), func(t *testing.T) {
			store, _, app, dep := scopedSecretRuntimeFixture(t)
			// The decoder path represents a malformed persisted value even though
			// ordinary public requests validate it before storing a deployment.
			dep.OverrideEnvSecrets = raw
			engine := &Engine{store: store, log: testLog()}
			if loaded, err := engine.loadDeploymentSealedEnvDelivery(t.Context(), app.AccountID, app.ID, dep); err == nil || len(loaded.Entries) != 0 {
				t.Fatalf("corruption broadened delivery: %+v %v", loaded, err)
			}
		})
	}
}

func TestEnvironmentSecretReferencesCannotFallBackAcrossScopes(t *testing.T) {
	store, account, app, dep := scopedSecretRuntimeFixture(t)
	if err := store.UpsertAppSecretInScope(t.Context(), account.ID, app.ID, "default", "DEFAULT_ONLY", []byte("sealed-default")); err != nil {
		t.Fatal(err)
	}
	dep.OverrideEnvSecrets = json.RawMessage(`{"OTHER":"secret:DEFAULT_ONLY"}`)
	engine := &Engine{store: store, log: testLog()}
	loaded, err := engine.loadDeploymentSealedEnvDelivery(t.Context(), account.ID, app.ID, dep)
	if err == nil || len(loaded.Entries) != 0 || strings.Contains(err.Error(), "sealed-default") {
		t.Fatalf("scope fallback staged or exposed a value: %+v %v", loaded, err)
	}
}

func TestEnvironmentSecretReferencesBindSameNameSources(t *testing.T) {
	store, account, app, _ := scopedSecretRuntimeFixture(t)
	if err := store.PutAppEnvironmentSecretReference(t.Context(), account.ID, app.ID, "production", "LEGACY", "secret:LEGACY"); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{store: store, log: testLog()}
	loaded, err := engine.loadSealedEnvDeliveryFor(t.Context(), account.ID, app.ID, "production", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range loaded.Entries {
		if entry.Key == "LEGACY" {
			if entry.SourceKey != "LEGACY" {
				t.Fatal("same-name managed reference did not bind its sealed source")
			}
			return
		}
	}
	t.Fatal("same-name destination missing")
}

func TestEnvironmentSecretReferencesSuppressionSurvivesWakeAndKeepsSidecars(t *testing.T) {
	store, account, app, dep := scopedSecretRuntimeFixture(t)
	for _, key := range []string{"DATABASE_URL", "DATABASE_A", "LEGACY"} {
		if err := store.DeleteAppEnvironmentSecretReference(t.Context(), account.ID, app.ID, "production", key); err != nil {
			t.Fatal(err)
		}
	}
	engine := &Engine{store: store, log: testLog()}
	loaded, err := engine.loadDeploymentSealedEnvDelivery(t.Context(), account.ID, app.ID, dep)
	if err != nil || len(loaded.Entries) != 1 || loaded.References["DATABASE_URL"] != "" || loaded.References["SECOND_ALIAS"] != "secret:DATABASE_B" {
		t.Fatalf("legacy alias reappeared: %+v %v", loaded, err)
	}
	automatic, err := engine.loadSealedEnvDeliveryFor(t.Context(), account.ID, app.ID, "production", nil)
	if err != nil || !automatic.AllSecrets || !maps.Equal(automatic.References, map[string]string{"DATABASE_B": "secret:DATABASE_B"}) {
		t.Fatalf("automatic keys reappeared: %+v %v", automatic, err)
	}
	sidecar, err := engine.resolveSealedEnvDeliveryFor(t.Context(), account.ID, app.ID, "production", map[string]string{"LEGACY": "secret:LEGACY"}, false)
	if err != nil || len(sidecar.Entries) != 1 || sidecar.References["LEGACY"] != "secret:LEGACY" {
		t.Fatalf("primary suppression changed sidecar: %+v %v", sidecar, err)
	}
	vmm := &fakeVMM{}
	wakeEngine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	wake, err := wakeEngine.Wake(t.Context(), app.ID, dep.ID, "production", TriggerAppWake)
	if err != nil {
		t.Fatal(err)
	}
	inputs, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), wake.InstanceID)
	if err != nil || !exists || !maps.Equal(inputs.SecretRefs, loaded.References) {
		t.Fatalf("wake lost suppression evidence: %+v %v %v", inputs, exists, err)
	}
	if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, inputs); err != nil || !fresh {
		t.Fatalf("suppressed wake proof: %v %v", fresh, err)
	}
	if len(vmm.lastColdBootSpec.SealedEnv) != 1 || vmm.lastColdBootSpec.SealedEnv[0].Key != "SECOND_ALIAS" {
		t.Fatal("wake staged suppressed key")
	}
	if err := store.PutAppEnvironmentSecretReference(t.Context(), account.ID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_A"); err != nil {
		t.Fatal(err)
	}
	if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, inputs); err != nil || fresh {
		t.Fatalf("reenable did not invalidate old boot: %v %v", fresh, err)
	}
	loaded, err = wakeEngine.loadDeploymentSealedEnvDelivery(t.Context(), account.ID, app.ID, dep)
	if err != nil || loaded.References["DATABASE_URL"] != "secret:DATABASE_A" {
		t.Fatalf("explicit mapping could not re-enable key: %+v %v", loaded, err)
	}
}
