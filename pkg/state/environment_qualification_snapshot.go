package state

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/google/uuid"
)

// EnvironmentQualificationSnapshot describes one completed warm capture of the
// original qualification VM. It grants no readiness, restore, or activation
// authority. CaptureID is the host's incoming generation, distinct from its
// physical NativeGeneration. All objects belong to that immutable namespace.
type EnvironmentQualificationSnapshot struct {
	CaptureID         string `json:"capture_id"`
	NativeGeneration  string `json:"native_generation"`
	KernelBootID      string `json:"kernel_boot_id"`
	StorageKey        string `json:"storage_key"`
	VMStateStorageKey string `json:"vmstate_storage_key"`
	DriveStorageKey   string `json:"drive_storage_key"`
	BackingStorageKey string `json:"backing_storage_key"`
	MemBytes          int64  `json:"mem_bytes"`
	VMStateBytes      int64  `json:"vmstate_bytes"`
	StoredBytes       int64  `json:"stored_bytes"`
}

// Capture evidence is retained separately from reusable serving snapshots.
// Recording it grants no health, smoke, restore, or activation authority.
type EnvironmentQualificationSnapshotReceipt struct {
	Execution  EnvironmentQualificationExecution `json:"execution"`
	Snapshot   EnvironmentQualificationSnapshot  `json:"snapshot"`
	Inputs     RuntimeConfigInputs               `json:"-"`
	RecordedAt time.Time                         `json:"recorded_at"`
}

type EnvironmentQualificationSnapshotStore interface {
	RecordEnvironmentQualificationSnapshot(context.Context, EnvironmentWorkloadQualificationRequest, EnvironmentQualificationExecution, EnvironmentQualificationSnapshot) (EnvironmentQualificationSnapshotReceipt, error)
	EnvironmentQualificationSnapshotReceipt(context.Context, string) (EnvironmentQualificationSnapshotReceipt, error)
}

func ValidateEnvironmentQualificationSnapshot(frame EnvironmentQualificationExecution, proof EnvironmentQualificationSnapshot) error {
	if !qualificationRecoveryUUIDValid(frame.DeploymentID) {
		return ErrConflict
	}
	for _, value := range []string{proof.CaptureID, proof.NativeGeneration, proof.KernelBootID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return fmt.Errorf("qualification capture identity is incomplete: %w", ErrConflict)
		}
	}
	mem := SnapshotCaptureMemKey(frame.DeploymentID, SnapshotTierWarm, proof.CaptureID)
	snapshot := Snapshot{StorageKey: mem}
	if proof.CaptureID == proof.NativeGeneration || proof.StorageKey != mem || proof.VMStateStorageKey != SnapshotVMStateKey(snapshot) ||
		proof.DriveStorageKey != SnapshotDriveKey(snapshot) || proof.BackingStorageKey != SnapshotBackingKey(snapshot) ||
		proof.MemBytes <= 0 || proof.VMStateBytes <= 0 || proof.StoredBytes <= 0 {
		return fmt.Errorf("qualification capture namespace or completion is unconfirmed: %w", ErrConflict)
	}
	return nil
}

func cloneQualificationSnapshotReceipt(receipt EnvironmentQualificationSnapshotReceipt) EnvironmentQualificationSnapshotReceipt {
	receipt.Inputs = cloneRuntimeConfigInputs(receipt.Inputs)
	return receipt
}

func qualificationCaptureInputsEqual(a, b RuntimeConfigInputs) bool {
	return a.Scope == b.Scope && a.Boundary.Equal(b.Boundary) && a.AllSecrets == b.AllSecrets &&
		maps.Equal(a.Variables, b.Variables) && maps.Equal(a.SecretVersions, b.SecretVersions) &&
		maps.Equal(a.SecretRefs, b.SecretRefs) && maps.Equal(a.SidecarSecretVersions, b.SidecarSecretVersions)
}
