package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/state"
)

// A native backend must fence its private-drive export and publication
// producers before the Manager can start a capture. Native journal ownership
// alone does not implement this surface.
type environmentQualificationSnapshotBackend interface {
	checkEnvironmentQualificationSnapshotSupport() error
}

// Native drive export still needs its durable producer adapter. Preserve the
// existing freezeSnapshotDrive gate, and refuse before pause/snapshot effects.
func (v *JailerVMM) checkEnvironmentQualificationSnapshotSupport() error {
	return fmt.Errorf("native qualification: private-drive capture producer is unavailable: %w", state.ErrConflict)
}

// CaptureEnvironmentQualification captures exactly the original private VM.
// vmmd chooses all object keys; callers cannot redirect capture to host paths,
// shared deployment keys, or another attempt. This is capture evidence only,
// never a successful restore, smoke check, or graph qualification receipt.
func (m *Manager) CaptureEnvironmentQualification(ctx context.Context, frame state.EnvironmentQualificationExecution) (proof state.EnvironmentQualificationSnapshot, result error) {
	j, err := m.qualificationJournal(frame)
	if err != nil {
		return proof, err
	}
	backend, ok := m.vmm.(environmentQualificationSnapshotBackend)
	if !ok {
		return proof, state.ErrConflict
	}
	if err := backend.checkEnvironmentQualificationSnapshotSupport(); err != nil {
		return proof, err
	}
	incoming, err := j.snapshotAuthority(ctx, frame)
	if err != nil {
		return proof, err
	}
	ctx, cancel := context.WithDeadline(ctx, incoming.Deadline)
	defer cancel()
	ctx, inst, flight, err := m.beginLiveInstanceFlight(nativeQualificationContext(ctx, incoming), frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer m.finishInstanceFlight(frame.InstanceID, flight)
	// Serialize with revocation through publication. Generic Destroy still
	// cancels and joins this flight before any native teardown.
	lock, err := j.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	current, err := j.read(frame.InstanceID)
	if err != nil || current != incoming || !j.clock().Before(current.Deadline) || ctx.Err() != nil {
		return proof, errors.Join(err, ctx.Err(), state.ErrConflict)
	}
	if inst.AppTaskOnly || inst.ExecutionOnly || inst.IsJob || inst.Paused || inst.Method != WakeColdBoot ||
		inst.AppID != frame.AppID || inst.DeploymentID != frame.DeploymentID || inst.nativeGeneration != incoming.NativeGeneration ||
		!sameNativePhysicalLease(inst.Lease, incoming.NativeLease) {
		return proof, state.ErrConflict
	}
	if err := m.checkLiveAdmission(ctx, inst); err != nil {
		return proof, err
	}
	if err := j.requireSnapshotPhysical(ctx, incoming); err != nil {
		return proof, err
	}
	capture, err := j.readCapture(incoming)
	if err == nil {
		if capture.CompletedAt.IsZero() {
			return proof, fmt.Errorf("native qualification: capture outcome is uncertain: %w", state.ErrConflict)
		}
		return qualificationSnapshotProof(incoming, capture.Info), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return proof, err
	}
	backing, err := m.qualificationSnapshotBacking(inst.Lease.Instance)
	if err != nil {
		return proof, err
	}
	capture = nativeQualificationCaptureRecord{Version: 1, InstanceID: frame.InstanceID, CaptureID: incoming.Generation,
		NativeGeneration: incoming.NativeGeneration, KernelBootID: incoming.KernelBootID, StartedAt: j.clock().UTC()}
	if err := j.writeCapture(incoming, capture); err != nil {
		return proof, err
	}
	keys := qualificationSnapshotProof(incoming, SnapshotInfo{})
	info, err := m.warmSnapshotInstance(ctx, inst, SnapshotSpec{StorageKey: keys.StorageKey, VMStateStorageKey: keys.VMStateStorageKey})
	if err != nil {
		return proof, err
	}
	// A best-effort sidecar cannot make a restorable capture receipt. Bind
	// the images remembered at boot and require successful publication.
	body, err := json.Marshal(backing)
	if err != nil {
		return proof, err
	}
	if err := m.storage.Put(ctx, keys.BackingStorageKey, bytes.NewReader(body)); err != nil {
		return proof, err
	}
	if err := j.requireSnapshotPhysical(ctx, incoming); err != nil {
		return proof, err
	}
	if err := ctx.Err(); err != nil {
		return proof, err
	}
	capture.Info, capture.Backing, capture.CompletedAt = info, backing, j.clock().UTC()
	if err := j.writeCapture(incoming, capture); err != nil {
		return proof, err
	}
	if err := ctx.Err(); err != nil {
		return proof, err
	}
	return qualificationSnapshotProof(incoming, info), nil
}

func (j *nativeQualificationJournal) snapshotAuthority(ctx context.Context, frame state.EnvironmentQualificationExecution) (incoming nativeQualificationRecord, result error) {
	lock, err := j.lock(ctx, frame.InstanceID)
	if err != nil {
		return incoming, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	incoming, err = j.read(frame.InstanceID)
	if err != nil {
		return incoming, err
	}
	if incoming.Execution != frame || !incoming.CreateStarted || incoming.Revoked || incoming.NativeGeneration == "" ||
		!j.clock().Before(incoming.Deadline) || ctx.Err() != nil {
		return incoming, errors.Join(ctx.Err(), state.ErrConflict)
	}
	return incoming, nil
}

func (j *nativeQualificationJournal) requireSnapshotPhysical(ctx context.Context, incoming nativeQualificationRecord) (result error) {
	lock, err := j.owner.lock(ctx, incoming.Execution.InstanceID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	physical, err := j.owner.read(incoming.Execution.InstanceID)
	if err != nil {
		return err
	}
	if physical.Generation != incoming.NativeGeneration || physical.KernelBootID != incoming.KernelBootID ||
		!sameNativePhysicalLease(physical.Lease, incoming.NativeLease) || !physical.Authorized || physical.PID <= 0 || physical.StartTime == 0 ||
		physical.Revoked || physical.ExitConfirmed || physical.ResourcesRemoved {
		return state.ErrConflict
	}
	return ctx.Err()
}

func (m *Manager) qualificationSnapshotBacking(instance string) (BackingIdentity, error) {
	m.backingMu.Lock()
	identity := m.instanceBacking[instance]
	m.backingMu.Unlock()
	if m.storage == nil || !identity.complete() {
		return BackingIdentity{}, ErrSnapshotBackingUnverified
	}
	return identity, nil
}

func qualificationSnapshotProof(incoming nativeQualificationRecord, info SnapshotInfo) state.EnvironmentQualificationSnapshot {
	mem := state.SnapshotCaptureMemKey(incoming.Execution.DeploymentID, state.SnapshotTierWarm, incoming.Generation)
	snapshot := state.Snapshot{StorageKey: mem}
	return state.EnvironmentQualificationSnapshot{CaptureID: incoming.Generation, NativeGeneration: incoming.NativeGeneration,
		KernelBootID: incoming.KernelBootID, StorageKey: mem, VMStateStorageKey: state.SnapshotVMStateKey(snapshot),
		DriveStorageKey: state.SnapshotDriveKey(snapshot), BackingStorageKey: state.SnapshotBackingKey(snapshot),
		MemBytes: info.MemBytes, VMStateBytes: info.VMStateBytes, StoredBytes: info.StoredBytes}
}
