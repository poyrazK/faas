package fcvm

// adr: 581. Pin the load inputs; an API acknowledgment is not consumed RAM.

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func (v *JailerVMM) pinVerifiedSnapshotBlobs(ctx context.Context, lease Lease, root string, spec RestoreSpec, memory, vmstate string) (err error) {
	if spec.verifiedSnapshot == nil {
		return nil
	}
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return err
	}
	handoff := spec.verifiedSnapshot.inputs.owner
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || len(handoff.restoreBlobs) != 0 {
		return runtimeadmission.ErrStale
	}
	sandbox, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sandbox.Close()) }()
	for i, artifact := range []runtimeadmission.CapturedArtifact{handoff.restoreCapture.Memory, handoff.restoreCapture.VMState} {
		path := []string{memory, vmstate}[i]
		source := runtimeadmission.ArtifactSource{Kind: "full-rootfs", StorageKey: artifact.StorageKey, Digest: artifact.Digest, Bytes: artifact.Bytes}
		pinned, err := pinRuntimeDrive(ctx, sandbox, Drive{PathOnHost: path, IsReadOnly: true}, source)
		if err != nil {
			return err
		}
		handoff.restoreBlobs = append(handoff.restoreBlobs, pinned)
		if pinned.info.Mode().Perm()&0o222 != 0 {
			return runtimeadmission.ErrInvalid
		}
	}
	return ctx.Err()
}

func (v *JailerVMM) checkPinnedSnapshotBlobs(ctx context.Context, root string, plan *verifiedSnapshotRestore) (err error) {
	handoff := plan.inputs.owner
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || handoff.restoreLoad != plan || len(handoff.restoreBlobs) != 2 {
		return runtimeadmission.ErrStale
	}
	sandbox, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sandbox.Close()) }()
	for _, blob := range handoff.restoreBlobs {
		info, err := sandbox.Lstat(blob.path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 || !os.SameFile(info, blob.info) {
			return errors.Join(runtimeadmission.ErrStale, err)
		}
		actual, _, err := measurePinnedRuntimeDrive(ctx, blob.file, blob.observation.Source.Bytes)
		if err != nil || actual != blob.observation.Producer {
			return errors.Join(runtimeadmission.ErrInvalid, err)
		}
	}
	return ctx.Err()
}

func (v *JailerVMM) loadRestoredSnapshot(ctx context.Context, lease Lease, root string, spec RestoreSpec, body map[string]any) error {
	plan := spec.verifiedSnapshot
	if plan == nil {
		return v.apiPut(ctx, lease.Instance, "/snapshot/load", body)
	}
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return err
	}
	raw, err := verifiedSnapshotLoadCommand(spec, body)
	if err != nil {
		return err
	}
	if err := claimVerifiedSnapshotLoad(plan); err != nil {
		return err
	}
	if err := v.measureFinalRuntimeDrives(ctx, lease, root, raw); err != nil {
		return err
	}
	if err := v.checkPinnedSnapshotBlobs(ctx, root, plan); err != nil {
		return err
	}
	if err := v.apiPut(ctx, lease.Instance, "/snapshot/load", json.RawMessage(raw)); err != nil {
		return err
	}
	if err := errors.Join(v.checkVerifiedSnapshotLoad(ctx, lease, spec), v.checkPinnedSnapshotBlobs(ctx, root, plan)); err != nil {
		return err
	}
	plan.inputs.owner.mu.Lock()
	defer plan.inputs.owner.mu.Unlock()
	if plan.inputs.owner.closed {
		return runtimeadmission.ErrStale
	}
	plan.accepted = true
	return nil
}

func claimVerifiedSnapshotLoad(plan *verifiedSnapshotRestore) error {
	plan.inputs.owner.mu.Lock()
	defer plan.inputs.owner.mu.Unlock()
	if plan.inputs.owner.closed || !plan.started {
		return runtimeadmission.ErrStale
	}
	if plan.attempted {
		return runtimeadmission.ErrReplay
	}
	plan.attempted = true
	return nil
}

func verifiedSnapshotLoadCommand(spec RestoreSpec, body map[string]any) ([]byte, error) {
	expected := map[string]any{"snapshot_path": vmstateSnapshotName,
		"mem_backend": map[string]any{"backend_type": "File", "backend_path": memSnapshotName}, "resume_vm": !spec.KeepPaused}
	got, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	want, err := json.Marshal(expected)
	if err != nil || string(got) != string(want) {
		return nil, runtimeadmission.ErrInvalid
	}
	return got, nil
}

func (v *JailerVMM) observeVerifiedSnapshotDrives(ctx context.Context, lease Lease, spec RestoreSpec) error {
	if spec.verifiedSnapshot == nil {
		return nil
	}
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return err
	}
	return v.observeApprovedRuntimeDrives(ctx, lease)
}
