package sched

// adr: 733

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// sealingVMM records sealed warm snapshots and the snapshot refs restores
// receive.
type sealingVMM struct {
	*recordingStopVMM
	mu        sync.Mutex
	sealedIDs []string
	restores  []SnapshotRef
}

func (s *sealingVMM) WarmSnapshotSealed(_ context.Context, _, _, _, _, captureID string) (SnapshotBytes, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealedIDs = append(s.sealedIDs, captureID)
	return SnapshotBytes{MemBytes: 1 << 20}, []byte("sealed:" + captureID), nil
}

func (s *sealingVMM) CreateFromSnapshot(ctx context.Context, nodeID, instance string, app AppSpec, snap SnapshotRef) (*WakeOutcome, error) {
	s.mu.Lock()
	s.restores = append(s.restores, snap)
	s.mu.Unlock()
	return s.recordingStopVMM.CreateFromSnapshot(ctx, nodeID, instance, app, snap)
}

func TestCaptureCrash_SealsAtTheSource(t *testing.T) {
	store := state.NewMemStore()
	vmm := &sealingVMM{recordingStopVMM: &recordingStopVMM{fakeVMM: &fakeVMM{}}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithSealedCrashCaptures(true)
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeNormal))
	capture := requestCapture(t, store, ins)

	done, err := e.CaptureCrash(context.Background(), capture)
	if err != nil {
		t.Fatalf("CaptureCrash: %v", err)
	}
	if string(done.SealedKey) != "sealed:"+capture.ID || len(vmm.sealedIDs) != 1 || vmm.warmSnapshots != 0 {
		t.Fatalf("sealed=%q sealedIDs=%v plain warm=%d; want one sealed capture and no plaintext one",
			done.SealedKey, vmm.sealedIDs, vmm.warmSnapshots)
	}
	ready, err := store.CompleteCrashCapture(context.Background(), done)
	if err != nil || !ready.Sealed() {
		t.Fatalf("complete = %+v, %v; want sealed", ready, err)
	}

	// A fork of it hands vmmd the capture id and sealed key, and the
	// vmstate storage key rather than a host path.
	snap, sealedKey, err := e.forkSnapshot(context.Background(), state.AppFork{AppID: ready.AppID, CrashCaptureID: &ready.ID},
		state.Deployment{ID: ready.DeploymentID}, "", state.App{})
	if err != nil || string(sealedKey) != "sealed:"+capture.ID || snap.ID != ready.ID {
		t.Fatalf("fork snapshot = %+v key=%q err=%v", snap, sealedKey, err)
	}
}

func TestCaptureCrash_RefusesWhenVMMCannotSeal(t *testing.T) {
	store := state.NewMemStore()
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithSealedCrashCaptures(true)
	ins := seedRunningInstanceWithMode(t, e, store, string(state.InstanceModeNormal))
	capture := requestCapture(t, store, ins)
	if _, err := e.CaptureCrash(context.Background(), capture); !errors.Is(err, ErrCrashStorageRemote) {
		t.Fatalf("CaptureCrash without a sealing VMM err = %v, want ErrCrashStorageRemote", err)
	}
	if vmm.warmSnapshots != 0 {
		t.Fatalf("a plaintext capture was taken (%d)", vmm.warmSnapshots)
	}
}
