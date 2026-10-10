// adr: 069
package sched

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestWakeRestoresCompanionSnapshot reproduces production-us rc.251: a
// deployment with a 64 MiB companion boots a 512+64 MiB guest and captures a
// snapshot of that size. Wake compared it with the app's 512 MiB alone, marked
// it stale and cold-booted every time.
func TestWakeRestoresCompanionSnapshot(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:companion", Status: state.DeployPending,
		Sidecars: json.RawMessage(`[{"name":"heartbeat","type":"sidecar","ram_mb":64}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: dep.ID, SidecarName: "heartbeat", StorageKey: "apps/app/heartbeat.ext4",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: (512 + 64) << 20,
		StorageKey: state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "c1"),
		Tier:       state.SnapshotTierInit,
	})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(ctx, app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if vmm.restores != 1 || vmm.coldBoots != 0 {
		t.Fatalf("restores=%d coldBoots=%d; want the companion snapshot restored", vmm.restores, vmm.coldBoots)
	}
	got, err := store.LatestSnapshot(ctx, dep.ID)
	if err != nil || got.ID != snap.ID || got.Stale {
		t.Fatalf("companion snapshot after wake = %+v, %v; want it kept fresh", got, err)
	}
}
