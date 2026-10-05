package runtimeadmission

// adr: 592. A carried catalog envelope belongs to exactly one restore request.

import (
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// CheckSnapshotRestorePayload validates a private wire envelope. Native
// capability and measured loading are separate requirements; this check does
// not authorize VM allocation or assert that a restore took place.
func CheckSnapshotRestorePayload(req *vmmdpb.CreateAdmittedRuntimeRequest, binding Binding, now time.Time) error {
	if req == nil || RejectUnknown(req) != nil || !binding.validSnapshotBinding() {
		return ErrInvalid
	}
	if req.SnapshotRestore == nil {
		if binding.SnapshotCaptureToken != "" {
			return ErrInvalid
		}
		return nil
	}
	if binding.SnapshotCaptureToken == "" {
		return ErrInvalid
	}
	restore := req.GetRestore()
	if restore == nil || restore.Snapshot == nil || restore.App == nil || restore.Build != nil || restore.Snapshot.Networkless || restore.Snapshot.DeploymentId != binding.DeploymentID || restore.App.MemSizeMib <= 0 {
		return ErrInvalid
	}
	evidence, err := SnapshotRestoreEvidenceFromProto(req.SnapshotRestore)
	if err != nil {
		return err
	}
	sources, err := ArtifactSourcesFromProto(req.ArtifactSources)
	if err != nil {
		return err
	}
	return evidence.Check(binding, sources, restore.Snapshot.StorageKey, restore.Snapshot.VmstateStorageKey, restore.Snapshot.FcVersion, int64(restore.App.MemSizeMib)<<20, now)
}
