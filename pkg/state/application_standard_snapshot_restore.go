package state

// adr: 593. Restore catalog selection is history; issuance fences current input.

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func StandardSnapshotRestoreEvidence(r ApplicationStandardSnapshotCaptureRecord) (runtimeadmission.SnapshotRestoreEvidence, error) {
	if standardSnapshotRecordValid(r) != nil || r.Acknowledgment == nil {
		return runtimeadmission.SnapshotRestoreEvidence{}, ErrApplicationStandardRuntimeStale
	}
	e := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: r.Grant.Token, FCVersion: r.Grant.FCVersion, Capture: r.Acknowledgment.Capture.Clone()}
	return e, e.Validate(time.Now())
}

func checkStandardSnapshotRestoreBinding(binding runtimeadmission.Binding, capture InstanceApplicationStandardAdmission, r ApplicationStandardSnapshotCaptureRecord, snap Snapshot, now time.Time) error {
	evidence, err := StandardSnapshotRestoreEvidence(r)
	if err != nil || snap.Stale || snap.DeletePending || snap.ApplicationStandardCaptureToken != binding.SnapshotCaptureToken || !sameStandardUUID(snap.DeploymentID, binding.DeploymentID) {
		return ErrApplicationStandardRuntimeStale
	}
	memoryBytes, err := standardSnapshotRestoreMemoryBytes(capture.inputs)
	if err != nil || snap.MemBytes != memoryBytes || snap.MemBytes != evidence.Capture.Memory.Bytes || snap.DiskBytes != evidence.Capture.VMState.Bytes || snap.StorageKey != evidence.Capture.Memory.StorageKey || snap.FCVersion != evidence.FCVersion {
		return ErrApplicationStandardRuntimeStale
	}
	if err := evidence.Check(binding, standardCapturedArtifactSources(capture), snap.StorageKey, SnapshotVMStateKey(snap), snap.FCVersion, snap.MemBytes, now); err != nil {
		return ErrApplicationStandardRuntimeStale
	}
	match, err := standardNativeRuntimeInputsMatch(r.inputs, capture.inputs)
	if err != nil || !match {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func standardSnapshotRestoreMemoryBytes(raw []byte) (int64, error) {
	var input struct {
		RAMMB int64 `json:"instance_ram_mb"`
	}
	if json.Unmarshal(raw, &input) != nil || input.RAMMB <= 0 || input.RAMMB > api.ApplicationStandardSnapshotMaxArtifactBytes>>20 {
		return 0, ErrApplicationStandardRuntimeStale
	}
	return input.RAMMB << 20, nil
}

func (m *MemStore) checkStandardSnapshotRestoreLocked(binding runtimeadmission.Binding, capture InstanceApplicationStandardAdmission, now time.Time) error {
	if binding.SnapshotCaptureToken == "" {
		return nil
	}
	r, found := m.applicationStandardSnapshotCaptures[binding.SnapshotCaptureToken]
	if !found {
		return ErrApplicationStandardRuntimeStale
	}
	for _, snap := range m.snapshots {
		if snap.ApplicationStandardCaptureToken == binding.SnapshotCaptureToken && !snap.Stale && !snap.DeletePending {
			return checkStandardSnapshotRestoreBinding(binding, capture, r, snap, now)
		}
	}
	return ErrApplicationStandardRuntimeStale
}
