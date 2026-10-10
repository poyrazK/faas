// adr: 568 — live output production requires the original started capture.
package fcvm

import (
	"context"
	"errors"
	"path/filepath"
)

type nativeSnapshotCaptureContextKey struct{}

type nativeSnapshotCapturePermit struct {
	Incoming nativeQualificationRecord
	Capture  nativeQualificationCaptureRecord
	Physical nativeLaunchRecord
}

// The Manager creates this capability while holding the incoming lock, after
// durable capture start. Boot's incoming capability cannot authorize outputs.
func nativeSnapshotCaptureContext(ctx context.Context, incoming nativeQualificationRecord, capture nativeQualificationCaptureRecord, physical nativeLaunchRecord) context.Context {
	return context.WithValue(ctx, nativeSnapshotCaptureContextKey{}, nativeSnapshotCapturePermit{Incoming: incoming, Capture: capture, Physical: physical})
}

type nativeSnapshotOutputBackend interface {
	PrepareSnapshotOutput(context.Context, nativeLaunchRecord, string, string, string) (nativeImagePreparation, error)
}

type nativeSnapshotOutputNames struct {
	Memory  string
	VMState string
}

// Partial preparation retains its original epoch; callers cannot retry a
// failed capture or fall back to unowned memory/state files in the jail.
func (v *JailerVMM) stageNativeSnapshotOutputs(ctx context.Context, lease Lease, directory string) (names nativeSnapshotOutputNames, err error) {
	names.Memory, err = v.stageNativeSnapshotOutput(ctx, lease, directory, "mem")
	if err != nil {
		return names, err
	}
	names.VMState, err = v.stageNativeSnapshotOutput(ctx, lease, directory, "vmstate")
	return names, err
}

func nativeSnapshotOutputName(captureID, kind string) (string, error) {
	if !canonicalNativeHelperID(captureID) || kind != "mem" && kind != "vmstate" {
		return "", errors.New("native snapshot output: original capture and output kind are required")
	}
	return "capture-" + captureID + "-" + kind, nil
}

func (v *JailerVMM) stageNativeSnapshotOutput(ctx context.Context, lease Lease, directory, kind string) (string, error) {
	r := v.nativeRecovery
	permit, ok := ctx.Value(nativeSnapshotCaptureContextKey{}).(nativeSnapshotCapturePermit)
	if r == nil || r.journal == nil || !ok || permit.Incoming.Execution.InstanceID != lease.Instance || !sameNativePhysicalLease(permit.Incoming.NativeLease, lease) {
		return "", errors.New("native snapshot output: original capture capability is required")
	}
	r.mu.Lock()
	generation, daemonLock := r.owned[lease.Instance], r.daemonLock
	r.mu.Unlock()
	if generation == "" || generation != permit.Incoming.NativeGeneration || daemonLock == nil {
		return "", errors.New("native snapshot output: original daemon producer is required")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return "", err
	}
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return "", err
	}
	j := nativeImageSourceJournal{owner: r.journal, backend: r.imageSources, helperGroups: r.helperGroups}
	return j.stageCaptureOutput(ctx, owner, permit, v.chrootRoot(lease.Instance), directory, kind)
}

func (j *nativeImageSourceJournal) stageCaptureOutput(ctx context.Context, expected nativeLaunchRecord, permit nativeSnapshotCapturePermit, root, directory, kind string) (string, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || directory == "/" {
		return "", errors.New("native snapshot output: private disk placement is required")
	}
	backend, ok := j.backend.(nativeSnapshotOutputBackend)
	if !ok {
		return "", errors.New("native snapshot output: anonymous output backend is unavailable")
	}
	name, err := nativeSnapshotOutputName(permit.Capture.CaptureID, kind)
	if err != nil {
		return "", err
	}
	return j.stageOwned(ctx, expected, root, name, false, 0, func(owner nativeLaunchRecord) (nativeLaunchRecord, error) {
		if err := j.captureOutputAuthority(ctx, expected, owner, permit); err != nil {
			return owner, err
		}
		// Image references retain the original preparation identity. The
		// separate capture frame and authority above bind live PID/start time.
		owner.Authorized, owner.PID, owner.StartTime = false, 0, 0
		return owner, nil
	}, func(owner nativeLaunchRecord) (nativeImagePreparation, error) {
		return backend.PrepareSnapshotOutput(ctx, owner, root, directory, name)
	})
}

// The incoming lock remains held by the Manager; take no second incoming lock
// after the physical lock. Every check precedes anonymous file production.
func (j *nativeImageSourceJournal) captureOutputAuthority(ctx context.Context, expected, owner nativeLaunchRecord, permit nativeSnapshotCapturePermit) error {
	if !sameNativeSnapshotProcess(owner, permit.Physical) || owner.PID != expected.PID || owner.StartTime != expected.StartTime || owner.Generation != permit.Incoming.NativeGeneration || owner.KernelBootID != permit.Incoming.KernelBootID || !sameNativePhysicalLease(owner.Lease, permit.Incoming.NativeLease) {
		return errors.New("native snapshot output: original live physical authority changed")
	}
	if err := permit.Capture.validate(permit.Incoming); err != nil {
		return err
	}
	if !permit.Capture.CompletedAt.IsZero() || !permit.Incoming.CreateStarted || permit.Incoming.Revoked {
		return errors.New("native snapshot output: capture is completed, revoked or unstarted")
	}
	q := j.owner.qualifications(permit.Incoming.Execution.NodeID)
	incoming, err := q.read(owner.Lease.Instance)
	if err != nil {
		return err
	}
	if incoming != permit.Incoming || !q.clock().Before(incoming.Deadline) {
		return errors.New("native snapshot output: original incoming capture authority changed")
	}
	capture, err := q.readCapture(incoming)
	if err != nil {
		return err
	}
	if capture != permit.Capture {
		return errors.New("native snapshot output: original capture start changed")
	}
	return ctx.Err()
}

func sameNativeSnapshotProcess(current, original nativeLaunchRecord) bool {
	return liveNativeSnapshotOwner(current) && liveNativeSnapshotOwner(original) && current.Generation == original.Generation &&
		current.KernelBootID == original.KernelBootID && current.PID == original.PID && current.StartTime == original.StartTime &&
		sameNativePhysicalLease(current.Lease, original.Lease)
}
