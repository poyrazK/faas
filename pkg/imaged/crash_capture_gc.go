package imaged

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// crashCaptureGCBatch bounds one tick's expiry work.
const crashCaptureGCBatch = 50

// expireCrashCaptures deletes the files of every ADR-733 crash capture past
// its expires_at and marks it expired. imaged owns capture file deletion as
// it does for snapshot rows; a capture is only marked expired once all four
// objects (mem, vmstate, private drive, backing identity) are gone, so a
// failed delete is retried on the next tick.
func (l *Loop) expireCrashCaptures(ctx context.Context, now time.Time) {
	due, err := l.store.ExpiredCrashCaptures(ctx, now, crashCaptureGCBatch)
	if err != nil || len(due) == 0 {
		if err != nil {
			l.log.Warn("imaged: crash capture expiry list", "err", err)
		}
		return
	}
	be, err := l.handler.storageFor()
	if err != nil {
		l.log.Warn("imaged: crash capture expiry storage", "err", err)
		return
	}
	for _, capture := range due {
		if err := deleteCrashCaptureFiles(ctx, be, capture); err != nil {
			l.log.Warn("imaged: crash capture delete", "capture", capture.ID, "err", err)
			continue
		}
		if _, err := l.store.ExpireCrashCapture(ctx, capture.ID, now); err != nil && !errors.Is(err, state.ErrNotFound) {
			l.log.Warn("imaged: crash capture expire", "capture", capture.ID, "err", err)
		}
	}
}

type crashCaptureDeleter interface {
	Delete(ctx context.Context, key string) error
}

func deleteCrashCaptureFiles(ctx context.Context, be crashCaptureDeleter, capture state.CrashCapture) error {
	if capture.StorageKey == nil || capture.VMStateStorageKey == nil {
		return nil
	}
	snap := state.Snapshot{DeploymentID: capture.DeploymentID, StorageKey: *capture.StorageKey, Tier: state.SnapshotTierWarm}
	keys := []string{*capture.StorageKey, *capture.VMStateStorageKey, state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)}
	var errs []error
	for _, key := range keys {
		if key == "" {
			continue
		}
		if err := be.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) && !errors.Is(err, storage.ErrDeleteQuarantined) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
