// adr: 568 — restore input evidence never creates native execution authority.
package fcvm

import (
	"context"
	"errors"
	"math"
)

// Fixed order: memory, device state, private drive, backing identity. Inventory
// can supply these original receipts, but cannot mint a live producer permit.
type nativeSnapshotRestoreCohort struct {
	Intent  nativeSnapshotPublicationIntent
	Objects [4]nativeSnapshotPublicationObjectReceipt
}

type nativeSnapshotRestoreReceiptJournal interface {
	nativeSnapshotPublicationJournal
	nativeSnapshotPublicationReceiptJournal
	ReadRestoreCohort(context.Context, string) (nativeSnapshotRestoreCohort, error)
}

func (c nativeSnapshotRestoreCohort) validate(completed nativeQualificationCaptureRecord) error {
	if err := errors.Join(c.Intent.validate(), completed.validate(c.Intent.Incoming)); err != nil {
		return err
	}
	if completed.CompletedAt.IsZero() {
		return errors.New("native snapshot restore: original capture completion is required")
	}
	initial := completed
	initial.CompletedAt, initial.Info, initial.Backing = c.Intent.Capture.CompletedAt, SnapshotInfo{}, BackingIdentity{}
	if initial != c.Intent.Capture {
		return errors.New("native snapshot restore: completion belongs to another original capture")
	}
	var stored int64
	for i, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		r := c.Objects[i]
		if err := r.validate(c.Intent); err != nil {
			return err
		}
		if r.Kind != kind {
			return errors.New("native snapshot restore: complete original object cohort is required")
		}
		for _, previous := range c.Objects[:i] {
			a, b := previous.Object, r.Object
			sameObject := a.Backend == b.Backend && a.Location == b.Location && a.ObjectKey == b.ObjectKey
			// Two canonical local roots may be bind aliases on the same host.
			// The original inode must differ even when their path strings do.
			sameInode := a.Local != nil && b.Local != nil && a.Local.Device == b.Local.Device && a.Local.Inode == b.Local.Inode
			if r.File == previous.File || sameObject || sameInode {
				return errors.New("native snapshot restore: cohort objects or receipt files are aliased")
			}
		}
		if i < 3 {
			if r.Object.StoredBytes > math.MaxInt64-stored {
				return errors.New("native snapshot restore: original cohort accounting overflow")
			}
			stored += r.Object.StoredBytes
		}
	}
	if c.Objects[0].Object.LogicalBytes != completed.Info.MemBytes || c.Objects[1].Object.LogicalBytes != completed.Info.VMStateBytes || stored != completed.Info.StoredBytes {
		return errors.New("native snapshot restore: completion differs from original artifact receipts")
	}
	return nil
}

func (c nativeSnapshotRestoreCohort) require(ctx context.Context, journal nativeSnapshotRestoreReceiptJournal) error {
	if err := journal.Require(ctx, c.Intent); err != nil {
		return err
	}
	for _, receipt := range c.Objects {
		if err := journal.RequireObject(ctx, c.Intent, receipt); err != nil {
			return err
		}
	}
	return ctx.Err()
}
