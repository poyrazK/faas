package sched

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type captureRecordingVMM struct {
	*fakeVMM
	memKey, stateKey string
}

func (v *captureRecordingVMM) PauseAndSnapshot(ctx context.Context, node, instance, hostPath, memKey, stateKey string) (SnapshotBytes, error) {
	v.memKey, v.stateKey = memKey, stateKey
	return v.fakeVMM.PauseAndSnapshot(ctx, node, instance, hostPath, memKey, stateKey)
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
		return
	}
	t.Fatal("missing publication")
}
