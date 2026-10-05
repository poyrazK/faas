package fcvm

// adr: 595
// Native mapping observation is private; it does not enable a restore protocol.

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type RuntimeSnapshotHandoffObservation struct {
	CaptureToken, EvidenceHash, ConfigHash, ProcessStart string
	ProcessPID                                           int
	Memory, VMState, PrivateDrive                        runtimeadmission.CapturedArtifact
	Ranges                                               []RuntimeSnapshotMemoryRange
}

func (v *JailerVMM) observeVerifiedSnapshotMemory(ctx context.Context, lease Lease, spec RestoreSpec) error {
	if err := v.checkVerifiedSnapshotLoad(ctx, lease, spec); err != nil {
		return err
	}
	plan := spec.verifiedSnapshot
	if plan == nil {
		return runtimeadmission.ErrUnavailable
	}
	handoff := plan.inputs.owner
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	plan.observation = nil
	if handoff.closed || handoff.restoreLoad != plan || !plan.accepted || len(handoff.restoreBlobs) != 2 || handoff.observation.ProcessPID <= 0 {
		return runtimeadmission.ErrStale
	}
	cmd, err := v.currentRuntimeDriveProcess(lease)
	if err != nil || cmd.Process.Pid != handoff.observation.ProcessPID {
		return errors.Join(runtimeadmission.ErrStale, err)
	}
	start, ranges, err := observeSnapshotMemoryMaps(ctx, "/proc", cmd.Process.Pid, lease.UID, handoff.restoreBlobs[0])
	if err != nil || start != handoff.observation.ProcessStart {
		return errors.Join(runtimeadmission.ErrStale, err)
	}
	for _, blob := range handoff.restoreBlobs {
		actual, info, err := measurePinnedRuntimeDrive(ctx, blob.file, blob.observation.Source.Bytes)
		if err != nil || actual != blob.observation.Producer || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 != 0 {
			return errors.Join(runtimeadmission.ErrInvalid, err)
		}
	}
	checkedStart, checkedRanges, err := observeSnapshotMemoryMaps(ctx, "/proc", cmd.Process.Pid, lease.UID, handoff.restoreBlobs[0])
	if err != nil || checkedStart != start || !slices.Equal(checkedRanges, ranges) {
		return errors.Join(runtimeadmission.ErrStale, err)
	}
	if err := checkVerifiedSnapshotLoadRequest(lease, plan.request); err != nil {
		return err
	}
	current, err := v.currentRuntimeDriveProcess(lease)
	if err != nil || current != cmd || ctx.Err() != nil {
		return errors.Join(runtimeadmission.ErrStale, err, ctx.Err())
	}
	plan.observation = &RuntimeSnapshotHandoffObservation{CaptureToken: plan.request.Binding.SnapshotCaptureToken,
		EvidenceHash: plan.request.Binding.SnapshotEvidenceHash, ConfigHash: handoff.observation.ConfigHash,
		ProcessPID: cmd.Process.Pid, ProcessStart: start, Memory: plan.request.Capture.Memory,
		VMState: plan.request.Capture.VMState, PrivateDrive: plan.request.Capture.PrivateDrive, Ranges: ranges}
	return nil
}

// ObservedRuntimeSnapshotConsumption returns a coupled drive/memory witness.
// Manager admission and advertised restore capability remain independently gated.
func (v *JailerVMM) ObservedRuntimeSnapshotConsumption(ctx context.Context, lease Lease) (runtimeadmission.ArtifactConsumption, runtimeadmission.SnapshotConsumption, error) {
	observed, err := v.ObservedRuntimeSnapshot(ctx, lease)
	if err != nil {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, err
	}
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, errors.Join(runtimeadmission.ErrStale, err)
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	plan := handoff.restoreLoad
	if handoff.closed || plan == nil || plan.observation == nil || handoff.observation.ProcessPID != observed.ProcessPID || handoff.observation.ProcessStart != observed.ProcessStart || handoff.observation.ConfigHash != observed.ConfigHash {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, runtimeadmission.ErrStale
	}
	cmd, err := v.currentRuntimeDriveProcess(lease)
	if err != nil || cmd.Process.Pid != observed.ProcessPID {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, errors.Join(runtimeadmission.ErrStale, err)
	}
	drives := runtimeConsumptionFromObservation(handoff.observation)
	proof := runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: observed.CaptureToken,
		EvidenceHash: observed.EvidenceHash, Memory: observed.Memory, VMState: observed.VMState, PrivateDrive: observed.PrivateDrive, MappedMemoryBytes: observed.Memory.Bytes}
	evidence := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: plan.request.Binding.SnapshotCaptureToken, FCVersion: plan.request.Snapshot.FCVersion, Capture: plan.request.Capture}
	if err := proof.CheckEvidence(plan.request.Binding, drives, plan.keepPaused, evidence, time.Now()); err != nil || ctx.Err() != nil {
		return runtimeadmission.ArtifactConsumption{}, runtimeadmission.SnapshotConsumption{}, errors.Join(err, ctx.Err())
	}
	return drives, proof, nil
}

// ObservedRuntimeSnapshot refreshes actual drive and memory-file consumption.
// It is neither a guest-readiness acknowledgment nor a durable restore receipt.
func (v *JailerVMM) ObservedRuntimeSnapshot(ctx context.Context, lease Lease) (RuntimeSnapshotHandoffObservation, error) {
	handoff, err := v.runtimeDriveHandoff(lease)
	if err != nil || handoff == nil {
		return RuntimeSnapshotHandoffObservation{}, errors.Join(runtimeadmission.ErrUnavailable, err)
	}
	handoff.mu.Lock()
	plan := handoff.restoreLoad
	handoff.mu.Unlock()
	if plan == nil {
		return RuntimeSnapshotHandoffObservation{}, runtimeadmission.ErrUnavailable
	}
	spec := snapshotRestoreSpec(lease, plan.request, plan.keepPaused)
	spec.verifiedSnapshot = plan
	if err := v.observeVerifiedSnapshotDrives(ctx, lease, spec); err != nil {
		return RuntimeSnapshotHandoffObservation{}, err
	}
	handoff.mu.Lock()
	defer handoff.mu.Unlock()
	if handoff.closed || handoff.restoreLoad != plan || plan.observation == nil {
		return RuntimeSnapshotHandoffObservation{}, runtimeadmission.ErrStale
	}
	copy := *plan.observation
	copy.Ranges = slices.Clone(copy.Ranges)
	return copy, ctx.Err()
}
