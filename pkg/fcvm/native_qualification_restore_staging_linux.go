//go:build linux

// adr: 568 — original target authority spans receipt reads and native staging.
package fcvm

import (
	"context"
	"errors"
)

// This internal operation still grants no launch/load/readiness. Only the
// first live target producer can join capture evidence to its prepared images;
// ordinary Restore and recovered journal entries cannot obtain this permit.
func (v *JailerVMM) stageNativeQualificationRestore(ctx context.Context, owner nativeLaunchRecord) (result error) {
	permit, ok := ctx.Value(nativeQualificationRestoreContextKey{}).(nativeQualificationRestoreRecord)
	r := v.nativeRecovery
	if !ok || r == nil || r.journal == nil || v.storage == nil || v.nativeImageStagingRoot == "" {
		return errors.New("native qualification restore: original producer and receipt staging adapters are required")
	}
	journal, ok := r.publications.(nativeSnapshotRestoreReceiptJournal)
	if !ok {
		return errors.New("native qualification restore: original object receipt journal is unavailable")
	}
	lock, producer, err := r.journal.lockQualificationProducer(ctx, owner.Lease.Instance)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	if producer == nil || producer.restore == nil || producer.NativeGeneration == "" || producer.NativeGeneration != owner.Generation ||
		producer.restore.Generation != permit.Generation || !sameNativePhysicalLease(producer.NativeLease, owner.Lease) {
		return errors.New("native qualification restore: staging differs from the original target binding")
	}
	if err := v.requireNativeRestoreStagingOwner(owner); err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(ctx, producer.restore.Deadline)
	defer cancel()
	j := r.journal.qualifications(producer.Execution.NodeID).restores()
	capture, err := j.requireCapture(ctx, *producer.restore)
	if err != nil {
		return err
	}
	return withNativeSnapshotRestoreInputs(ctx, journal, v.storage, capture, v.nativeImageStagingRoot, func(inputs nativeSnapshotRestoreInputs) error {
		// The immutable publication intent must name the exact source frame,
		// not merely matching artifact keys or a capture UUID.
		source, err := j.incoming.read(producer.Execution.CaptureInstanceID)
		original := inputs.Cohort.Intent.Incoming
		original.Revoked = true
		if err != nil || source != original || inputs.Cohort.Intent.JailBase != v.chrootBase {
			return errors.Join(err, errors.New("native qualification restore: receipts lost original source execution"))
		}
		return v.stageNativeSnapshotRestoreInputs(ctx, owner, v.chrootRoot(owner.Lease.Instance), inputs, capture, journal)
	})
}
