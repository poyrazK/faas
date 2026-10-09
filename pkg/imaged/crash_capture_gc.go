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
// it does for snapshot rows; a capture is only marked expired once every
// object (plaintext and encrypted) is gone, so a failed delete is retried on
// the next tick. Expiry also drops the sealed key.
func (l *Loop) expireCrashCaptures(ctx context.Context, be crashCaptureDeleter, now time.Time) {
	due, err := l.store.ExpiredCrashCaptures(ctx, now, crashCaptureGCBatch)
	if err != nil {
		l.log.Warn("imaged: crash capture expiry list", "err", err)
		return
	}
	for _, capture := range due {
		err := deleteCrashCaptureFiles(ctx, be, capture)
		l.crashMetrics.op("expire", err)
		if err != nil {
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

// crashCaptureKeys lists a capture's plaintext objects: memory, vmstate,
// private drive and backing identity. Each has an encrypted twin at
// key + crashCaptureEncryptedSuffix.
func crashCaptureKeys(capture state.CrashCapture) []string {
	if capture.StorageKey == nil || capture.VMStateStorageKey == nil {
		return nil
	}
	snap := state.Snapshot{DeploymentID: capture.DeploymentID, StorageKey: *capture.StorageKey, Tier: state.SnapshotTierWarm}
	keys := make([]string, 0, 4)
	for _, key := range []string{*capture.StorageKey, *capture.VMStateStorageKey, state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func deleteCrashCaptureFiles(ctx context.Context, be crashCaptureDeleter, capture state.CrashCapture) error {
	var keys []string
	for _, key := range crashCaptureKeys(capture) {
		keys = append(keys, key, key+crashCaptureEncryptedSuffix)
	}
	return deleteCrashCaptureKeys(ctx, be, keys)
}

// deleteCrashCaptureKeys treats only a missing object as deleted: a
// refused delete leaves evidence behind, so it is retried.
func deleteCrashCaptureKeys(ctx context.Context, be crashCaptureDeleter, keys []string) error {
	var errs []error
	for _, key := range keys {
		if err := be.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
