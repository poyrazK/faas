// adr: 470
package sched

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestRegionalPrimeCarriesSnapshotNamespace(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	id := "eu-fsn"
	_, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: id, DisplayName: "Falkenstein", AcceptingNewApps: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateComputeNode(ctx, state.ComputeNode{Name: "regional-prime", TargetURL: "unix:///regional", Region: &id, Active: true, AdmissionCeilingMB: 2048, VPCPUs: 4, VCPUBudget: 8})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, "regional-prime@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "regional-prime", RAMMB: 128, MaxConcurrency: 1, ComputeRegion: id})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &captureRecordingVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if err := e.Prime(ctx, app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(vmm.memKey, region.ArtifactKey(id, "snap/"+dep.ID+"/")) || vmm.stateKey != state.SnapshotVMStateKey(state.Snapshot{StorageKey: vmm.memKey}) {
		t.Fatalf("snapshot keys=%s %s", vmm.memKey, vmm.stateKey)
	}
}

func TestRegionalDiskDriftEnumeratesExactNamespaces(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	_, _, dep := seedApp(t, store, api.PlanPro, 128, 1)
	legacy, _ := storage.NewLocalStorageBackend(t.TempDir())
	eu, _ := storage.NewLocalStorageBackend(t.TempDir())
	us, _ := storage.NewLocalStorageBackend(t.TempDir())
	router, _ := storage.NewRegionRouter(map[string]storage.StorageBackend{"eu-fsn": eu, "us-east": us}, legacy)
	key := region.ArtifactKey("eu-fsn", state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, uuid.NewString()))
	snapshot := state.Snapshot{DeploymentID: dep.ID, StorageKey: key, FCVersion: "1.10.0"}
	if _, err := store.CreateSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{snapshot.StorageKey, state.SnapshotVMStateKey(snapshot), state.SnapshotDriveKey(snapshot)} {
		if err := router.Put(ctx, key, strings.NewReader("snapshot")); err != nil {
			t.Fatal(err)
		}
	}
	drift := NewDiskDrift(store, nil).WithStorage(router)
	if count, err := drift.Tick(ctx); err != nil || count != 0 {
		t.Fatalf("healthy regional snapshot drift=%d err=%v", count, err)
	}
	if err := router.Delete(ctx, snapshot.StorageKey); err != nil {
		t.Fatal(err)
	}
	// An identical relative key elsewhere cannot satisfy the regional row.
	if err := router.Put(ctx, strings.Replace(snapshot.StorageKey, "eu-fsn", "us-east", 1), strings.NewReader("wrong region")); err != nil {
		t.Fatal(err)
	}
	if count, err := drift.Tick(ctx); err != nil || count == 0 {
		t.Fatalf("cross-region object hid missing snapshot: %d %v", count, err)
	}
}
