//go:build linux

// adr: 20261022184951 — park and warm run the original native producer.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/extension"
	"github.com/onebox-faas/faas/pkg/state"
)

type nativeRuntimeCapturePublicationReleaser interface {
	ReleaseCompletedRuntimeCapture(context.Context, nativeRuntimeCaptureAuthority) error
}

// captureNativeRuntimeSnapshot writes the durable start before any hook, pause
// or output, then runs the same producer as qualification capture. An
// uncertain effect is never replayed: on failure the start stays incomplete,
// the VM is not resumed here, and the caller retires it.
func (v *JailerVMM) captureNativeRuntimeSnapshot(ctx context.Context, lease Lease, spec SnapshotSpec) (info SnapshotInfo, result error) {
	req, r := spec.nativeRuntime, v.nativeRecovery
	if req == nil || !v.nativeRuntimeCaptureEnabled() || r.journal == nil {
		return info, fmt.Errorf("native recovery: snapshot publication producer is unavailable: %w", state.ErrConflict)
	}
	now := time.Now().UTC()
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Sub(now) > api.NativeRuntimeCaptureMaxDuration {
		deadline = now.Add(api.NativeRuntimeCaptureMaxDuration)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	j := r.journal.runtimeCaptures()
	lock, err := j.lock(ctx, lease.Instance)
	if err != nil {
		return info, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	physical, err := v.nativeRuntimeCapturePhysical(ctx, lease, req.NativeGeneration)
	if err != nil {
		return info, err
	}
	if current, err := j.read(lease.Instance); err == nil && current.Capture.CompletedAt.IsZero() {
		return info, fmt.Errorf("native runtime capture: an earlier capture of this VM is uncertain: %w", state.ErrConflict)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return info, err
	}
	physicalLease := physical.Lease
	physicalLease.processGeneration, physicalLease.profileStartedAt = 0, time.Time{}
	authority := nativeRuntimeCaptureAuthority{Version: 1, InstanceID: lease.Instance, DeploymentID: req.DeploymentID,
		NativeGeneration: physical.Generation, KernelBootID: physical.KernelBootID, NativeLease: physicalLease,
		Keys: nativeRuntimeCaptureKeys(spec.StorageKey), AcceptedAt: now, Deadline: deadline.UTC()}
	if spec.VMStateStorageKey != authority.Keys.VMState {
		return info, errors.New("native runtime capture: device state must use the coupled v2 key")
	}
	record := nativeRuntimeCaptureRecord{Version: 1, Authority: authority, Capture: nativeQualificationCaptureRecord{Version: 1,
		InstanceID: lease.Instance, CaptureID: nativeRuntimeCaptureID(spec.StorageKey), NativeGeneration: physical.Generation,
		KernelBootID: physical.KernelBootID, FCVersion: req.FCVersion, StartedAt: now}}
	if err := j.write(record); err != nil {
		return info, err
	}
	ctx = nativeRuntimeSnapshotCaptureContext(ctx, authority, record.Capture, physical)
	// Guest-channel hooks create no host outputs. They run after the durable
	// start and before the producer's pause.
	if err := v.TriggerExtensionHook(ctx, lease, string(extension.PhasePreSnapshot), nil); err != nil {
		slog.Default().Debug("vmm: pre-snapshot extension hook unavailable", "instance", lease.Instance, "err", err)
	}
	if spec.BeforeCheckpoint {
		if err := v.TriggerBeforeCheckpoint(ctx, lease); err != nil {
			return info, err
		}
	}
	info, err = v.captureEnvironmentQualificationSnapshot(ctx, lease, req.Backing)
	if err != nil {
		return SnapshotInfo{}, err
	}
	record.Capture.Info, record.Capture.Backing, record.Capture.CompletedAt = info, req.Backing, time.Now().UTC()
	if err := j.write(record); err != nil {
		return SnapshotInfo{}, err
	}
	v.preBoot.captured(lease.Instance, spec.StorageKey)
	// The published objects are now ordinary snapshot cache, owned by the
	// snapshot row and its GC. Producer bookkeeping is released best-effort:
	// a leftover completed record or intent grants nothing.
	cleanup := context.WithoutCancel(ctx)
	if releaser, ok := r.publications.(nativeRuntimeCapturePublicationReleaser); ok {
		if err := releaser.ReleaseCompletedRuntimeCapture(cleanup, authority); err != nil {
			slog.Default().Warn("native runtime capture: release publication intent", "instance", lease.Instance, "capture", record.Capture.CaptureID, "err", err)
		}
	}
	if err := j.remove(record); err != nil {
		slog.Default().Warn("native runtime capture: remove completed record", "instance", lease.Instance, "capture", record.Capture.CaptureID, "err", err)
	}
	return info, nil
}

// The live physical owner must be this daemon's own current launch.
func (v *JailerVMM) nativeRuntimeCapturePhysical(ctx context.Context, lease Lease, generation string) (physical nativeLaunchRecord, result error) {
	r := v.nativeRecovery
	if err := r.checkDaemonOwnership(); err != nil {
		return physical, err
	}
	lock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return physical, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	physical, err = r.journal.read(lease.Instance)
	if err != nil {
		return physical, err
	}
	bootID, err := r.journal.currentBootID()
	if err != nil {
		return physical, err
	}
	if !liveNativeSnapshotOwner(physical) || physical.Generation != r.generation(lease.Instance) || generation != "" && physical.Generation != generation ||
		physical.KernelBootID != bootID || !sameNativePhysicalLease(physical.Lease, lease) {
		return physical, fmt.Errorf("native runtime capture: original live physical owner is required: %w", state.ErrConflict)
	}
	return physical, ctx.Err()
}
