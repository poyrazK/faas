package sched

// adr: 351 — terminal init capture runs the callback and warm capture is skipped.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type captureRecordingVMM struct {
	*fakeVMM
	memKey, stateKey string
	beforeCheckpoint bool
}

func (v *captureRecordingVMM) PauseAndSnapshot(ctx context.Context, node, instance, hostPath, memKey, stateKey string, beforeCheckpoint bool) (SnapshotBytes, error) {
	v.memKey, v.stateKey = memKey, stateKey
	v.beforeCheckpoint = beforeCheckpoint
	return v.fakeVMM.PauseAndSnapshot(ctx, node, instance, hostPath, memKey, stateKey, beforeCheckpoint)
}

func TestBeforeCheckpointTerminalCaptureAndReuse(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 3)
	vmm := &captureRecordingVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	ins := state.Instance{ID: "i-checkpoint", AppID: app.ID, DeploymentID: dep.ID}
	if _, reused, err := e.captureInitOrReuse(ctx, ins, "/tmp/state", "snap/mem", "snap/state", true); err != nil || reused != nil {
		t.Fatalf("first capture: reused=%+v err=%v", reused, err)
	}
	if !vmm.beforeCheckpoint || vmm.snapshots != 1 {
		t.Fatalf("callback flag=%t captures=%d", vmm.beforeCheckpoint, vmm.snapshots)
	}
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "reusable")
	if _, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: key}); err != nil {
		t.Fatal(err)
	}
	if _, reused, err := e.captureInitOrReuse(ctx, ins, "/tmp/state", "snap/mem", "snap/state", true); err != nil || reused == nil {
		t.Fatalf("reuse: reused=%+v err=%v", reused, err)
	}
	if vmm.snapshots != 1 || vmm.destroys != 1 {
		t.Fatalf("reuse recaptured: captures=%d destroys=%d", vmm.snapshots, vmm.destroys)
	}
}

func TestBeforeCheckpointSkipsWarmCapture(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 3)
	app.WarmSnapshotEnabled = true
	app.Manifest.BeforeCheckpoint = &api.BeforeCheckpointHook{Path: "/checkpoint"}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.captureWarmSnapshotLocked(context.Background(), state.Instance{}, app); err != nil {
		t.Fatal(err)
	}
	if vmm.warmSnapshots != 0 {
		t.Fatalf("warm captures = %d", vmm.warmSnapshots)
	}
}

func TestParkRetainsUsableSnapshot(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 3)
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierInit, "first")
	_, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: key})
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{snapErr: errors.New("upload unavailable")}
	notify := &fakeNotifier{}
	e := newEngine(t, store, vmm, notify, "1.10.0")
	for range 3 {
		wake, err := e.Wake(ctx, app.ID, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(vmm.lastSnapRef.VMStatePath, strings.TrimPrefix(state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), "snap/")) {
			t.Fatalf("restore mixed keys: %+v", vmm.lastSnapRef)
		}
		if err := e.Park(ctx, wake.InstanceID); err != nil {
			t.Fatal(err)
		}
		ins, err := store.InstanceByID(ctx, wake.InstanceID)
		if err != nil || ins.State != string(state.StateParked) {
			t.Fatalf("parked state: %+v %v", ins, err)
		}
	}
	if vmm.snapshots != 0 || vmm.destroys != 3 || e.Ledger().ResidentRAM() != 0 {
		t.Fatalf("capture=%d destroy=%d resident=%d", vmm.snapshots, vmm.destroys, e.Ledger().ResidentRAM())
	}
	if notify.count("snapshot_written") != 0 {
		t.Fatal("reused snapshot was republished")
	}
}

func TestFailedCaptureDoesNotReuseEarlierObjectKeys(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 3)
	vmm := &captureRecordingVMM{fakeVMM: &fakeVMM{snapErr: context.DeadlineExceeded}}
	notify := &fakeNotifier{}
	e := newEngine(t, store, vmm, notify, "1.10.0")
	var previous string
	for range 2 {
		wake, err := e.Wake(ctx, app.ID, "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Park(ctx, wake.InstanceID); err == nil {
			t.Fatal("expected capture failure")
		}
		if !state.IsSnapshotCaptureKey(vmm.memKey) || vmm.memKey == previous || vmm.memKey == state.SnapMemKey(dep.ID) {
			t.Fatalf("reused publication key %q", vmm.memKey)
		}
		if vmm.stateKey != strings.TrimSuffix(vmm.memKey, "/mem")+"/vmstate" {
			t.Fatalf("unpaired keys %q %q", vmm.memKey, vmm.stateKey)
		}
		previous = vmm.memKey
	}
	if notify.count("snapshot_written") != 0 {
		t.Fatal("failed capture became selectable")
	}
}

func TestCaptureNotificationCarriesExactKeys(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 3)
	vmm := &captureRecordingVMM{fakeVMM: &fakeVMM{}}
	notify := &fakeNotifier{}
	e := newEngine(t, store, vmm, notify, "1.10.0")
	if err := e.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	for _, call := range notify.events {
		if call.channel != "snapshot_written" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(call.payload), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["storage_key"] != vmm.memKey {
			t.Fatalf("published %v, captured %s", payload["storage_key"], vmm.memKey)
		}
		if got := payload["base_image_version"]; got != fcvm.FAAS_BASE_IMAGE_VERSION {
			t.Fatalf("base_image_version = %v, want %s", got, fcvm.FAAS_BASE_IMAGE_VERSION)
		}
		return
	}
	t.Fatal("missing publication")
}

func TestSnapshotStateLocatorsKeepWarmGenerationPaired(t *testing.T) {
	e := newEngine(t, state.NewMemStore(), &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for _, tier := range []string{state.SnapshotTierInit, state.SnapshotTierWarm} {
		for _, node := range []string{e.defaultLocalNodeID, "remote-node"} {
			key := state.SnapshotCaptureMemKey("dep", tier, "capture")
			snap := state.Snapshot{DeploymentID: "dep", Tier: tier, StorageKey: key}
			hostPath, storageKey := e.snapshotStateLocators(node, snap)
			want := state.SnapshotVMStateKey(snap)
			if hostPath != SnapDir()+"/"+strings.TrimPrefix(want, "snap/") {
				t.Fatalf("wrong host path: %s", hostPath)
			}
			if node == e.defaultLocalNodeID {
				if storageKey != "" {
					t.Fatal("legacy local carrier changed")
				}
			} else if storageKey != want {
				t.Fatalf("mixed snapshot pair: %s %s", key, storageKey)
			}
		}
	}
}
