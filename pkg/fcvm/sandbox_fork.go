package fcvm

// Sandbox fork (ADR-20261122132055 §3). A fork captures a running session VM
// without ending it: the warm-snapshot path pauses the guest only while
// Firecracker writes the snapshot and the session's volume is cloned, then
// resumes it before the capture is published. The capture is a session
// capture like a pause's, under the account's session namespace and
// published node-first; each fork restores it into a session of its own
// (WakeSandbox with SessionCapture), taking a new instance identity, with a
// volume restored from the clone's backup.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// SandboxFork names one fork capture of a running session. Volumes and
// VolumeBackupID, when set, clone the session's volume as the capture
// leaves it, for backup VolumeBackupID.
type SandboxFork struct {
	Instance       string
	SandboxID      string
	AccountID      string
	CaptureID      string
	Volumes        *SandboxVolumes
	VolumeBackupID string
}

// SandboxForked is a fork's capture and, for a session with a volume, the
// volume's clone at the capture, still to upload.
type SandboxForked struct {
	SandboxPaused
	Volume *PendingVolumeBackup
}

// ForkSandbox captures sandboxID's VM and leaves it running. A frozen VM is
// refused: the caller thaws it first, as for a pause.
func (m *Manager) ForkSandbox(ctx context.Context, f SandboxFork) (SandboxForked, error) {
	if m == nil || m.vmm == nil {
		return SandboxForked{}, errors.New("fcvm: sandbox sessions are not configured")
	}
	id, err := uuid.Parse(f.CaptureID)
	if err != nil || id.String() != f.CaptureID || f.AccountID == "" || f.SandboxID == "" {
		return SandboxForked{}, errors.New("fcvm: a sandbox fork needs a session, an account and a capture UUID")
	}
	if (f.Volumes == nil) != (f.VolumeBackupID == "") {
		return SandboxForked{}, errors.New("fcvm: a sandbox fork clones a volume with both its volumes and a backup ID")
	}
	m.mu.Lock()
	inst, ok := m.live[f.Instance]
	ours := ok && inst != nil && inst.SandboxID == f.SandboxID && inst.ExecutionOnly
	var networkless bool
	if ours {
		networkless = inst.Lease.Networkless
	}
	m.mu.Unlock()
	if !ours {
		return SandboxForked{}, ErrSandboxNotFound
	}
	if m.isFrozen(inst) {
		return SandboxForked{}, fmt.Errorf("fcvm: fork sandbox %s: %w", f.Instance, ErrInstanceFrozen)
	}
	memKey := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace(f.AccountID), "", f.CaptureID)
	if !storagekey.IsSandboxSessionSnapshotKey(memKey, f.AccountID) || state.SnapshotDriveKey(state.Snapshot{StorageKey: memKey}) == "" {
		return SandboxForked{}, errors.New("fcvm: a sandbox fork capture must be a v2 key of the account's session namespace")
	}
	spec := SnapshotSpec{StorageKey: memKey, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})}
	if m.storage != nil {
		spec.Storage = storage.NodeFirst(m.storage)
	}
	var volume *PendingVolumeBackup
	if f.Volumes != nil {
		spec.whilePaused = func() error {
			p, err := f.Volumes.PrepareBackup(f.AccountID, f.SandboxID, f.VolumeBackupID)
			volume = p
			return err
		}
	}
	info, err := m.WarmSnapshot(ctx, f.Instance, spec)
	if err != nil {
		if volume != nil {
			volume.Discard()
		}
		return SandboxForked{}, fmt.Errorf("fcvm: fork sandbox %s: %w", f.Instance, err)
	}
	if f.Volumes != nil && volume == nil {
		return SandboxForked{}, fmt.Errorf("fcvm: fork sandbox %s: the volume was not cloned with the capture", f.Instance)
	}
	snap := &Snapshot{StorageKey: memKey, VMStateStorageKey: spec.VMStateStorageKey, FCVersion: m.fcVersion,
		MemBytes: info.MemBytes, Networkless: networkless}
	backing := state.SnapshotBackingKey(state.Snapshot{StorageKey: memKey})
	return SandboxForked{SandboxPaused: SandboxPaused{Snapshot: snap, Info: info, HasBacking: m.holdsObject(ctx, backing)}, Volume: volume}, nil
}
