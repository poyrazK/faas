package state_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQualificationSnapshotRequiresPinnedFirecrackerVersion(t *testing.T) {
	frame := state.EnvironmentQualificationExecution{DeploymentID: uuid.NewString()}
	captureID := uuid.NewString()
	snapshot := state.Snapshot{StorageKey: state.SnapshotCaptureMemKey(frame.DeploymentID, state.SnapshotTierWarm, captureID)}
	proof := state.EnvironmentQualificationSnapshot{CaptureID: captureID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(),
		FCVersion: "1.7.0", StorageKey: snapshot.StorageKey, VMStateStorageKey: state.SnapshotVMStateKey(snapshot),
		DriveStorageKey: state.SnapshotDriveKey(snapshot), BackingStorageKey: state.SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 512, StoredBytes: 2048}
	if err := state.ValidateEnvironmentQualificationSnapshot(frame, proof); err != nil {
		t.Fatal("complete capture proof rejected:", err)
	}
	for _, version := range []string{"", " ", "1.7.0 "} {
		invalid := proof
		invalid.FCVersion = version
		if err := state.ValidateEnvironmentQualificationSnapshot(frame, invalid); err == nil {
			t.Fatalf("capture without canonical Firecracker version was accepted: %q", version)
		}
	}
}
