// adr: 568 — live output production requires the original started capture.
package fcvm

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

type nativeSnapshotCaptureContextKey struct{}

// Exactly one authority is set: a qualification incoming record, or a runtime
// park/warm authority (ADR 20261022184951). Both bind the same capture record
// shape and physical owner, so every producer check is shared.
type nativeSnapshotCapturePermit struct {
	Incoming nativeQualificationRecord
	Runtime  nativeRuntimeCaptureAuthority
	Capture  nativeQualificationCaptureRecord
	Physical nativeLaunchRecord
}

// The Manager creates this capability while holding the incoming lock, after
// durable capture start. Boot's incoming capability cannot authorize outputs.
func nativeSnapshotCaptureContext(ctx context.Context, incoming nativeQualificationRecord, capture nativeQualificationCaptureRecord, physical nativeLaunchRecord) context.Context {
	return context.WithValue(ctx, nativeSnapshotCaptureContextKey{}, nativeSnapshotCapturePermit{Incoming: incoming, Capture: capture, Physical: physical})
}

// The runtime capture caller holds the runtime capture lock after its durable
// start, as qualification holds the incoming lock.
func nativeRuntimeSnapshotCaptureContext(ctx context.Context, authority nativeRuntimeCaptureAuthority, capture nativeQualificationCaptureRecord, physical nativeLaunchRecord) context.Context {
	return context.WithValue(ctx, nativeSnapshotCaptureContextKey{}, nativeSnapshotCapturePermit{Runtime: authority, Capture: capture, Physical: physical})
}

func (p nativeSnapshotCapturePermit) runtime() bool { return p.Runtime.Version != 0 }

func (p nativeSnapshotCapturePermit) instanceID() string {
	if p.runtime() {
		return p.Runtime.InstanceID
	}
	return p.Incoming.Execution.InstanceID
}

func (p nativeSnapshotCapturePermit) nativeLease() Lease {
	if p.runtime() {
		return p.Runtime.NativeLease
	}
	return p.Incoming.NativeLease
}

func (p nativeSnapshotCapturePermit) nativeGeneration() string {
	if p.runtime() {
		return p.Runtime.NativeGeneration
	}
	return p.Incoming.NativeGeneration
}

func (p nativeSnapshotCapturePermit) kernelBootID() string {
	if p.runtime() {
		return p.Runtime.KernelBootID
	}
	return p.Incoming.KernelBootID
}

func (p nativeSnapshotCapturePermit) deadline() time.Time {
	if p.runtime() {
		return p.Runtime.Deadline
	}
	return p.Incoming.Deadline
}

func (p nativeSnapshotCapturePermit) keys() nativeSnapshotPublicationObjectKeys {
	if p.runtime() {
		return p.Runtime.Keys
	}
	return nativeSnapshotIntentKeys(p.Incoming)
}

// validateCapture checks the capture record against its own authority only.
func (p nativeSnapshotCapturePermit) validateCapture() error {
	if p.runtime() {
		if p.Incoming != (nativeQualificationRecord{}) {
			return errors.New("native snapshot capture: one capture cannot carry two authorities")
		}
		return p.Capture.validateRuntime(p.Runtime)
	}
	return p.Capture.validate(p.Incoming)
}

// validateStarted additionally requires a live, unrevoked and incomplete start.
func (p nativeSnapshotCapturePermit) validateStarted() error {
	if err := p.validateCapture(); err != nil {
		return err
	}
	if p.runtime() {
		if err := p.Runtime.validate(); err != nil {
			return err
		}
	} else {
		if err := p.Incoming.validate(p.Incoming.Execution.NodeID); err != nil {
			return err
		}
		if !p.Incoming.CreateStarted || p.Incoming.Revoked {
			return errors.New("native snapshot capture: original incoming capture is revoked or unstarted")
		}
	}
	if !p.Capture.CompletedAt.IsZero() {
		return errors.New("native snapshot capture: original incomplete capture is required")
	}
	return nil
}

func (p nativeSnapshotCapturePermit) sameAuthority(other nativeSnapshotCapturePermit) bool {
	return p.Incoming == other.Incoming && p.Runtime == other.Runtime && p.Capture.Equal(other.Capture)
}

// requireCurrent re-reads the durable authority and capture start. A cleanup
// check passes deadline=false: returning the memory fence outlives the effect
// deadline but never a changed or replaced capture.
func (p nativeSnapshotCapturePermit) requireCurrent(owner *nativeLaunchJournal, deadline bool) error {
	if p.runtime() {
		j := owner.runtimeCaptures()
		record, err := j.read(p.Runtime.InstanceID)
		if err != nil {
			return err
		}
		if record.Authority != p.Runtime || deadline && !j.clock().Before(record.Authority.Deadline) {
			return errors.New("native snapshot capture: original runtime capture authority changed")
		}
		if !record.Capture.Equal(p.Capture) {
			return errors.New("native snapshot capture: original capture start changed")
		}
		return nil
	}
	q := owner.qualifications(p.Incoming.Execution.NodeID)
	incoming, err := q.read(p.Incoming.Execution.InstanceID)
	if err != nil {
		return err
	}
	if incoming != p.Incoming || deadline && !q.clock().Before(incoming.Deadline) {
		return errors.New("native snapshot capture: original incoming capture authority changed")
	}
	capture, err := q.readCapture(incoming)
	if err != nil {
		return err
	}
	if !capture.Equal(p.Capture) {
		return errors.New("native snapshot capture: original capture start changed")
	}
	return nil
}

func (p nativeSnapshotCapturePermit) writeBackings(owner *nativeLaunchJournal, record nativeSnapshotBackingRecord) error {
	if p.runtime() {
		return owner.runtimeCaptures().writeBackings(record)
	}
	return owner.qualifications(p.Incoming.Execution.NodeID).writeBackings(record)
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
	if r == nil || r.journal == nil || !ok || permit.instanceID() != lease.Instance || !sameNativePhysicalLease(permit.nativeLease(), lease) {
		return "", errors.New("native snapshot output: original capture capability is required")
	}
	r.mu.Lock()
	generation, daemonLock := r.owned[lease.Instance], r.daemonLock
	r.mu.Unlock()
	if generation == "" || generation != permit.nativeGeneration() || daemonLock == nil {
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
	if !sameNativeSnapshotProcess(owner, permit.Physical) || owner.PID != expected.PID || owner.StartTime != expected.StartTime || owner.Generation != permit.nativeGeneration() || owner.KernelBootID != permit.kernelBootID() || !sameNativePhysicalLease(owner.Lease, permit.nativeLease()) || owner.Lease.Instance != permit.instanceID() {
		return errors.New("native snapshot output: original live physical authority changed")
	}
	if err := permit.validateStarted(); err != nil {
		return err
	}
	if err := permit.requireCurrent(j.owner, true); err != nil {
		return err
	}
	return ctx.Err()
}

func sameNativeSnapshotProcess(current, original nativeLaunchRecord) bool {
	return liveNativeSnapshotOwner(current) && liveNativeSnapshotOwner(original) && current.Generation == original.Generation &&
		current.KernelBootID == original.KernelBootID && current.PID == original.PID && current.StartTime == original.StartTime &&
		sameNativePhysicalLease(current.Lease, original.Lease)
}
