package fcvm

// adr: 590. A protected load cannot re-resolve a mutable artifact locator.

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (v *JailerVMM) resolveRestoreBlobForInputs(ctx context.Context, lease Lease, spec RestoreSpec, artifact, key, fallback string) (string, restoreArtifactTiming, error) {
	if spec.verifiedSnapshot == nil {
		return v.resolveRestoreBlob(ctx, lease.Instance, artifact, key, fallback)
	}
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return "", restoreArtifactTiming{}, err
	}
	plan := spec.verifiedSnapshot
	path, captured := plan.inputs.memory, plan.request.Capture.Memory
	if artifact == "vmstate" {
		path, captured = plan.inputs.vmstate, plan.request.Capture.VMState
	} else if artifact != "mem" {
		return "", restoreArtifactTiming{}, runtimeadmission.ErrInvalid
	}
	if key != captured.StorageKey || fallback != "" {
		return "", restoreArtifactTiming{}, runtimeadmission.ErrStale
	}
	return path, restoreArtifactTiming{Artifact: artifact, Source: "verified", Bytes: captured.Bytes}, nil
}

func (v *JailerVMM) resolveRestoreArtifactsForInputs(ctx context.Context, lease Lease, spec RestoreSpec, artifacts []restoreArtifactSpec) ([]restoreArtifactResolution, error) {
	if spec.verifiedSnapshot == nil {
		return v.resolveRestoreArtifacts(ctx, lease.Instance, artifacts)
	}
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return nil, err
	}
	if len(artifacts) < 3 || artifacts[0].artifact != "kernel" || artifacts[0].key != spec.KernelKey {
		return nil, runtimeadmission.ErrInvalid
	}
	// Kernel identity remains the existing platform backing qualification.
	result, err := v.resolveRestoreArtifacts(ctx, lease.Instance, artifacts[:1])
	if err != nil {
		return nil, err
	}
	plan := spec.verifiedSnapshot
	for i, drive := range BuildColdBootConfig(plan.request.Runtime, lease.Slot).Drives {
		role, key := "base", drive.PathOnHost
		if i == 1 {
			role, key = "main", plan.request.Capture.PrivateDrive.StorageKey
		} else if i > 1 {
			role = "sidecar:" + plan.request.Runtime.Workloads[i-1].Name
		}
		if len(artifacts) <= i+1 || artifacts[i+1].artifact != role || artifacts[i+1].key != key || plan.inputs.drives[drive.DriveID] == "" {
			return nil, runtimeadmission.ErrStale
		}
		path := plan.inputs.drives[drive.DriveID]
		result = append(result, restoreArtifactResolution{path: path, restoreArtifactTiming: restoreArtifactTiming{Artifact: role, Source: "verified", Bytes: artifactBytes(path)}})
	}
	if len(result) != len(artifacts) {
		return nil, runtimeadmission.ErrInvalid
	}
	return result, ctx.Err()
}

func snapshotRestoreSidecarName(spec RestoreSpec, index int, path string) string {
	if spec.verifiedSnapshot != nil {
		// provisionForOwner names config drive i as sidecarDriveImageName(i-1).
		// Here index is the workload index, one less than its config drive index.
		return sidecarDriveImageName(index)
	}
	return filepath.Base(path)
}

func verifiedSnapshotDriveConfig(lease Lease, spec RestoreSpec) (VMConfig, error) {
	if spec.verifiedSnapshot == nil {
		return VMConfig{}, runtimeadmission.ErrInvalid
	}
	plan := spec.verifiedSnapshot
	config := BuildColdBootConfig(plan.request.Runtime, lease.Slot)
	for i := range config.Drives {
		drive := &config.Drives[i]
		switch i {
		case 0:
			drive.PathOnHost = stableReadOnlyName(plan.inputs.drives[drive.DriveID], baseImageName)
		case 1:
			drive.PathOnHost = layerImageName
		default:
			drive.PathOnHost = sidecarDriveImageName(i - 1)
		}
	}
	return config, nil
}

func (v *JailerVMM) pinVerifiedSnapshotDrives(ctx context.Context, lease Lease, root string, spec RestoreSpec) error {
	if spec.verifiedSnapshot == nil {
		return nil
	}
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return err
	}
	config, err := verifiedSnapshotDriveConfig(lease, spec)
	if err != nil {
		return err
	}
	return errors.Join(v.pinApprovedRuntimeDrives(ctx, lease, root, config), ctx.Err())
}
