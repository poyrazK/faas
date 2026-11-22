package fcvm

// Persistent session pause (ADR-20261122132055 §2). A pause captures a
// running session VM with Park, the terminal capture apps park with: memory,
// device state and the coupled private drive1 (the overlay upper that holds
// /workspace and everything else the session wrote) are published under the
// account's session namespace, and the VM, its cgroup, jail and netns are
// gone (invariant 4). The capture restores with WakeSandbox and
// SandboxWakeRequest.SessionCapture, which takes a new instance identity like
// any restore (invariant 5).
//
// The capture is published node-first (ADR-20261122132055 §2): into this
// node's cache only, so the pause waits for no upload, and
// PublishSandboxCapture uploads it to the shared store afterwards. A node
// without a cache publishes to its store as any capture does.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
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

// SandboxPaused is a pause's capture: the snapshot, its sizes, and whether
// its backing identity was written (a capture without one cold-boots on
// restore, ADR-510), so the shared store's copy is complete only with it.
type SandboxPaused struct {
	Snapshot   *Snapshot
	Info       SnapshotInfo
	HasBacking bool
}

// PauseSandbox captures sandboxID's VM and destroys it. kept reports whether
// the VM still runs after an error: a capture refused before the guest was
// touched keeps it, while one that failed after the pause loses it, since
// Park never leaves a paused guest behind.
func (m *Manager) PauseSandbox(ctx context.Context, p SandboxPause) (paused SandboxPaused, kept bool, err error) {
	if m == nil || m.vmm == nil {
		return SandboxPaused{}, false, errors.New("fcvm: sandbox sessions are not configured")
	}
	id, err := uuid.Parse(p.CaptureID)
	if err != nil || id.String() != p.CaptureID || p.AccountID == "" || p.SandboxID == "" {
		return SandboxPaused{}, m.sandboxLive(p.Instance, p.SandboxID), errors.New("fcvm: a sandbox pause needs a session, an account and a capture UUID")
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
		return SandboxPaused{}, false, ErrSandboxNotFound
	}
	if m.isFrozen(inst) {
		// The caller thaws first: Park refuses a frozen VM.
		return SandboxPaused{}, true, fmt.Errorf("fcvm: pause sandbox %s: %w", p.Instance, ErrInstanceFrozen)
	}
	memKey := state.SnapshotCaptureMemKey(storagekey.SandboxSessionSnapshotNamespace(p.AccountID), "", p.CaptureID)
	if !storagekey.IsSandboxSessionSnapshotKey(memKey, p.AccountID) || state.SnapshotDriveKey(state.Snapshot{StorageKey: memKey}) == "" {
		return SandboxPaused{}, true, errors.New("fcvm: a sandbox pause capture must be a v2 key of the account's session namespace")
	}
	spec := SnapshotSpec{StorageKey: memKey, VMStateStorageKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})}
	if m.storage != nil {
		spec.Storage = storage.NodeFirst(m.storage)
	}
	info, err := m.Park(ctx, p.Instance, spec)
	if err != nil {
		return SandboxPaused{}, m.sandboxLive(p.Instance, p.SandboxID), fmt.Errorf("fcvm: pause sandbox %s: %w", p.Instance, err)
	}
	snap := &Snapshot{StorageKey: memKey, VMStateStorageKey: spec.VMStateStorageKey, FCVersion: m.fcVersion,
		MemBytes: info.MemBytes, Networkless: networkless}
	backing := state.SnapshotBackingKey(state.Snapshot{StorageKey: memKey})
	return SandboxPaused{Snapshot: snap, Info: info, HasBacking: m.holdsObject(ctx, backing)}, false, nil
}

// holdsObject reports whether this node's store holds key, in its cache or
// its shared store.
func (m *Manager) holdsObject(ctx context.Context, key string) bool {
	if m.storage == nil || key == "" {
		return false
	}
	if storage.LocalOnly(m.storage, key) {
		return true
	}
	rc, err := m.storage.Get(ctx, key)
	if err != nil {
		return false
	}
	_ = rc.Close()
	return true
}

// sandboxCaptureKeys are a pause capture's objects.
func sandboxCaptureKeys(memKey string) []string {
	snap := state.Snapshot{StorageKey: memKey}
	return []string{memKey, state.SnapshotVMStateKey(snap), state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)}
}

// PublishSandboxCapture uploads a pause capture's local-only objects to the
// shared store (ADR-20261122132055 §2). Objects already there, or never
// written, are skipped; it is safe to call again after a failure.
func (m *Manager) PublishSandboxCapture(ctx context.Context, memKey string) error {
	if m == nil || m.storage == nil {
		return nil
	}
	var errs []error
	for _, key := range sandboxCaptureKeys(memKey) {
		if key == "" {
			continue
		}
		if _, err := storage.PublishLocal(ctx, m.storage, key); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PublishPendingSandboxCaptures uploads every pause capture object this
// node still holds alone, after a restart cut an upload short.
func (m *Manager) PublishPendingSandboxCaptures(ctx context.Context) (int, error) {
	if m == nil || m.storage == nil {
		return 0, nil
	}
	return storage.PublishLocalPrefix(ctx, m.storage, "snap/"+storagekey.SandboxSessionSnapshotNamespace(""))
}

// sandboxLive reports whether instance is still a live VM of sandboxID.
func (m *Manager) sandboxLive(instance, sandboxID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, ok := m.live[instance]
	return ok && inst != nil && inst.SandboxID == sandboxID
}
