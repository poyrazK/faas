package fcvm

// Persistent session pause (ADR-20261122132055 §2). A pause captures a
// running session VM with Park, the terminal capture apps park with: memory,
// device state and the coupled private drive1 (the overlay upper that holds
// /workspace and everything else the session wrote) are published under the
// account's session namespace, and the VM, its cgroup, jail and netns are
// gone (invariant 4). The capture restores with WakeSandbox and
// SandboxWakeRequest.SessionCapture, which takes a new instance identity like
// any restore (invariant 5).

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

// SandboxPause names one pause capture: the session's VM and the capture's
// ID, a UUID the session's owner chose.
type SandboxPause struct {
	Instance  string
	SandboxID string
	AccountID string
	CaptureID string
}

// PauseSandbox captures sandboxID's VM and destroys it. kept reports whether
// the VM still runs after an error: a capture refused before the guest was
// touched keeps it, while one that failed after the pause loses it, since
// Park never leaves a paused guest behind.
func (m *Manager) PauseSandbox(ctx context.Context, p SandboxPause) (snap *Snapshot, info SnapshotInfo, kept bool, err error) {
	if m == nil || m.vmm == nil {
		return nil, SnapshotInfo{}, false, errors.New("fcvm: sandbox sessions are not configured")
	}
	id, err := uuid.Parse(p.CaptureID)
	if err != nil || id.String() != p.CaptureID || p.AccountID == "" || p.SandboxID == "" {
		return nil, SnapshotInfo{}, m.sandboxLive(p.Instance, p.SandboxID), errors.New("fcvm: a sandbox pause needs a session, an account and a capture UUID")
	}
	m.mu.Lock()
	inst, ok := m.live[p.Instance]
	ours := ok && inst != nil && inst.SandboxID == p.SandboxID && inst.ExecutionOnly
	var networkless bool
	if ours {
		networkless = inst.Lease.Networkless
	}
	m.mu.Unlock()
	if !ours {
		return nil, SnapshotInfo{}, false, ErrSandboxNotFound
	}
	if m.isFrozen(inst) {
		// The caller thaws first: Park refuses a frozen VM.
		return nil, SnapshotInfo{}, true, fmt.Errorf("fcvm: pause sandbox %s: %w", p.Instance, ErrInstanceFrozen)
	}
	memKey := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace(p.AccountID), "", p.CaptureID)
	if !storagekey.IsSandboxSessionSnapshotKey(memKey, p.AccountID) || state.SnapshotDriveKey(state.Snapshot{StorageKey: memKey}) == "" {
		return nil, SnapshotInfo{}, true, errors.New("fcvm: a sandbox pause capture must be a v2 key of the account's session namespace")
	}
	spec := SnapshotSpec{StorageKey: memKey, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})}
	info, err = m.Park(ctx, p.Instance, spec)
	if err != nil {
		return nil, SnapshotInfo{}, m.sandboxLive(p.Instance, p.SandboxID), fmt.Errorf("fcvm: pause sandbox %s: %w", p.Instance, err)
	}
	return &Snapshot{StorageKey: memKey, VMStateStorageKey: spec.VMStateStorageKey, FCVersion: m.fcVersion,
		MemBytes: info.MemBytes, Networkless: networkless}, info, false, nil
}

// sandboxLive reports whether instance is still a live VM of sandboxID.
func (m *Manager) sandboxLive(instance, sandboxID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	return ok && inst != nil && inst.SandboxID == sandboxID
}
