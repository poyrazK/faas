//go:build linux

// adr: 568 — dedicated restore controls never enter ordinary fallback.
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type nativeQualificationRestoreLoadSession struct {
	v        *JailerVMM
	target   nativeQualificationRestoreRecord
	owner    nativeLaunchRecord
	capture  nativeQualificationCaptureRecord
	backings nativeSnapshotBackingRecord
	cohort   nativeSnapshotRestoreCohort
	receipts nativeSnapshotRestoreReceiptJournal
	journal  *nativeQualificationRestoreLoadJournal
	images   nativeImageSourceJournal
	fence    nativeQualificationRestoreFence
}

// The incoming first-producer context and live original daemon are required.
// The returned record proves only load/resume/hook acknowledgements. Scoped
// channels, fresh readiness, graph smoke and activation remain separate gates.
func (v *JailerVMM) loadNativeQualificationRestore(ctx context.Context, lease Lease) (record nativeQualificationRestoreLoadRecord, result error) {
	r := v.nativeRecovery
	if r == nil || r.journal == nil || r.snapshotControl == nil || r.restoreResume == nil || r.restoreFence == nil {
		return record, errors.New("native restore load: original startup control adapters are required")
	}
	receipts, ok := r.publications.(nativeSnapshotRestoreReceiptJournal)
	if !ok {
		return record, errors.New("native restore load: original receipt journal is unavailable")
	}
	if _, ok := r.imageSources.(nativeQualificationRestoreImageBackend); !ok {
		return record, errors.New("native restore load: original target image adapter is unavailable")
	}
	lock, producer, err := r.journal.lockQualificationProducer(ctx, lease.Instance)
	if err != nil {
		return record, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if producer == nil || producer.restore == nil || !sameNativePhysicalLease(lease, producer.NativeLease) || producer.NativeGeneration == "" {
		return record, errors.New("native restore load: original target producer is required")
	}
	ctx, cancel := context.WithDeadline(ctx, producer.restore.Deadline)
	defer cancel()
	j := r.journal.qualifications(producer.Execution.NodeID).restores()
	capture, err := j.requireCapture(ctx, *producer.restore)
	if err != nil {
		return record, err
	}
	backings, err := j.incoming.readBackings(capture)
	if err != nil {
		return record, err
	}
	cohort, err := receipts.ReadRestoreCohort(ctx, capture.CaptureID)
	if err != nil {
		return record, err
	}
	if err := cohort.validate(capture); err != nil {
		return record, err
	}
	source, err := j.incoming.read(capture.InstanceID)
	original := cohort.Intent.Incoming
	original.Revoked = true
	if err != nil || source != original || cohort.Intent.JailBase != v.chrootBase {
		return record, errors.Join(err, errors.New("native restore load: publication intent lost original source execution"))
	}
	physicalLock, err := r.journal.lock(ctx, lease.Instance)
	if err != nil {
		return record, err
	}
	defer func() { result = errors.Join(result, physicalLock.Close()) }()
	owner, err := r.journal.read(lease.Instance)
	if err != nil {
		return record, err
	}
	fence, err := r.restoreFence.Pin(ctx, owner)
	if err != nil {
		return record, err
	}
	defer func() { result = errors.Join(result, fence.Close()) }()
	s := nativeQualificationRestoreLoadSession{v: v, target: *producer.restore, owner: owner, capture: capture, backings: backings,
		cohort: cohort, receipts: receipts, journal: j.loads(), images: nativeImageSourceJournal{owner: r.journal, backend: r.imageSources}, fence: fence}
	s.journal.writeValue = r.restoreLoadWrite
	if err := s.requireAuthority(ctx); err != nil {
		return record, err
	}
	record, err = s.verifyImages(ctx)
	if err != nil {
		return record, err
	}
	if err := consumeNativeQualificationRestoreLoad(ctx, s.target); err != nil {
		return record, err
	}
	if err := s.journal.begin(record); err != nil {
		return record, err
	}
	if err := s.require(ctx, record); err != nil {
		return record, err
	}
	body := map[string]any{"snapshot_path": vmstateSnapshotName, "mem_backend": map[string]any{"backend_type": "File", "backend_path": memSnapshotName}, "resume_vm": false}
	if err := r.snapshotControl.Request(ctx, v.socketPath(lease.Instance), owner, http.MethodPut, "/snapshot/load", body); err != nil {
		return record, err
	}
	if record, err = s.advance(ctx, record, nativeRestoreLoaded); err != nil {
		return record, err
	}
	if record, err = s.advance(ctx, record, nativeRestoreResumeStarted); err != nil {
		return record, err
	}
	if err := r.snapshotControl.Request(ctx, v.socketPath(lease.Instance), owner, http.MethodPatch, "/vm", map[string]any{"state": "Resumed"}); err != nil {
		return record, err
	}
	if record, err = s.advance(ctx, record, nativeRestoreResumed); err != nil {
		return record, err
	}
	if record, err = s.advance(ctx, record, nativeRestoreHookStarted); err != nil {
		return record, err
	}
	if err := r.restoreResume.Resume(ctx, v, owner); err != nil {
		return record, err
	}
	record, err = s.advance(ctx, record, nativeRestoreHookCompleted)
	if err == nil {
		permit, _ := ctx.Value(nativeQualificationRestoreLoadContextKey{}).(*nativeQualificationRestoreLoadPermit)
		permit.completed.Store(true) // Only the acknowledged live producer can open channels.
	}
	return record, err
}

func (s *nativeQualificationRestoreLoadSession) requireAuthority(ctx context.Context) error {
	r := s.v.nativeRecovery
	if !liveNativeSnapshotOwner(s.owner) || s.owner.Generation != s.target.NativeGeneration || s.owner.KernelBootID != s.target.KernelBootID ||
		!sameNativePhysicalLease(s.owner.Lease, s.target.NativeLease) || r.generation(s.owner.Lease.Instance) != s.owner.Generation {
		return errors.New("native restore load: original live target incarnation changed")
	}
	if err := r.checkDaemonOwnership(); err != nil {
		return err
	}
	current, err := r.journal.read(s.owner.Lease.Instance)
	if err != nil || current != s.owner {
		return errors.Join(err, errors.New("native restore load: original physical record changed"))
	}
	_, err = s.journal.restores.producer(ctx, s.owner.Lease.Instance, s.target)
	if err != nil {
		return err
	}
	if err := s.images.requireRetiredSnapshotBackings(s.backings, s.v.chrootRoot(s.capture.InstanceID)); err != nil {
		return err
	}
	return errors.Join(s.cohort.require(ctx, s.receipts), s.fence.Require(ctx), ctx.Err())
}

func (s *nativeQualificationRestoreLoadSession) verifyImages(ctx context.Context) (record nativeQualificationRestoreLoadRecord, err error) {
	owner, target := s.owner, s.target
	record = nativeQualificationRestoreLoadRecord{Version: 1, InstanceID: owner.Lease.Instance, IncomingGeneration: target.Generation,
		NativeGeneration: owner.Generation, KernelBootID: owner.KernelBootID, PID: owner.PID, StartTime: owner.StartTime,
		TargetSHA256: nativeRestoreTargetHash(target), CaptureSHA256: nativeRestoreEvidenceHash(s.capture), BackingsSHA256: nativeRestoreEvidenceHash(s.backings), Cgroup: s.fence.Group()}
	record.Phases[0] = s.journal.restores.incoming.clock().UTC()
	sources, refs, err := s.images.restoreLoadReferences(owner, s.v.chrootRoot(owner.Lease.Instance), false)
	if err != nil {
		return record, err
	}
	for i := range record.Images {
		var captured *nativeSnapshotBackingImage
		var name, digest string
		var size int64
		if i < 2 {
			captured = &s.backings.Images[i]
			name, digest, size = captured.Name, captured.SHA256, captured.LogicalBytes
		} else {
			receipt := s.cohort.Objects[i-2].Object
			name, digest, size = [...]string{memSnapshotName, vmstateSnapshotName, layerImageName}[i-2], receipt.SHA256, receipt.LogicalBytes
		}
		source, ref := sources[name], refs[name]
		if ref.ID == "" || ref.ReadOnly != (i != 4) {
			return record, errors.New("native restore load: original named target image is missing")
		}
		record.Images[i] = nativeSnapshotBackingImage{Epoch: source.Epoch, ReferenceID: ref.ID, Identity: source.Identity, Name: name, LogicalBytes: size, SHA256: digest}
		if err := s.images.verifyRestoreLoadImage(ctx, source, ref, record.Images[i], captured); err != nil {
			return record, fmt.Errorf("native restore load: verify original %s: %w", name, err)
		}
		if err := s.requireAuthority(ctx); err != nil {
			return record, err
		}
	}
	return record, errors.Join(record.requireOriginal(target, s.capture, s.backings, owner), s.images.requireRestoreLoadWitnesses(record, owner, s.v.chrootRoot(owner.Lease.Instance), false))
}

func (s *nativeQualificationRestoreLoadSession) require(ctx context.Context, record nativeQualificationRestoreLoadRecord) error {
	if err := s.requireAuthority(ctx); err != nil {
		return err
	}
	current, err := s.journal.read(record.InstanceID)
	if err != nil || current != record {
		return errors.Join(err, errors.New("native restore load: original effect record changed"))
	}
	capture, err := s.journal.restores.requireCapture(ctx, s.target)
	if err != nil {
		return err
	}
	backings, err := s.journal.restores.incoming.readBackings(capture)
	if err != nil {
		return err
	}
	return errors.Join(record.requireOriginal(s.target, capture, backings, s.owner),
		s.images.requireRestoreLoadWitnesses(record, s.owner, s.v.chrootRoot(record.InstanceID), false), ctx.Err(), requireNativeRestoreFenceGroup(record.Cgroup, s.fence.Group()))
}

func (s *nativeQualificationRestoreLoadSession) advance(ctx context.Context, record nativeQualificationRestoreLoadRecord, step int) (nativeQualificationRestoreLoadRecord, error) {
	if err := s.require(ctx, record); err != nil {
		return record, err
	}
	now := s.journal.restores.incoming.clock().UTC()
	if !now.Before(s.target.Deadline) {
		return record, errors.New("native restore load: original effect deadline expired")
	}
	// A backwards wall clock cannot manufacture an ordered acknowledgement.
	if now.Before(record.Phases[step-1]) {
		return record, errors.New("native restore load: effect clock moved backwards")
	}
	if next, err := s.journal.advance(record, step, now); err != nil {
		return next, err
	} else {
		return next, s.require(ctx, next)
	}
}
