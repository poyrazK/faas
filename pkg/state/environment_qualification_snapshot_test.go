package state

import (
	"testing"

	"github.com/google/uuid"
)

func TestQualificationSnapshotRequiresPinnedFirecrackerVersion(t *testing.T) {
	frame := EnvironmentQualificationExecution{DeploymentID: uuid.NewString()}
	captureID := uuid.NewString()
	snapshot := Snapshot{StorageKey: SnapshotCaptureMemKey(frame.DeploymentID, SnapshotTierWarm, captureID)}
	proof := EnvironmentQualificationSnapshot{CaptureID: captureID, NativeGeneration: uuid.NewString(), KernelBootID: uuid.NewString(),
		FCVersion: "1.7.0", StorageKey: snapshot.StorageKey, VMStateStorageKey: SnapshotVMStateKey(snapshot),
		DriveStorageKey: SnapshotDriveKey(snapshot), BackingStorageKey: SnapshotBackingKey(snapshot), MemBytes: 1024, VMStateBytes: 512, StoredBytes: 2048}
	if err := ValidateEnvironmentQualificationSnapshot(frame, proof); err != nil {
		t.Fatal("complete capture proof rejected:", err)
	}
	for _, version := range []string{"", " ", "1.7.0 "} {
		invalid := proof
		invalid.FCVersion = version
		if err := ValidateEnvironmentQualificationSnapshot(frame, invalid); err == nil {
			t.Fatalf("capture without canonical Firecracker version was accepted: %q", version)
		}
	}
}
