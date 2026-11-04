//go:build linux

// adr: 20261022184951 — a completed runtime capture releases its producer bookkeeping.
package fcvm

import (
	"context"
	"errors"

	"golang.org/x/sys/unix"
)

// ReleaseCompletedRuntimeCapture removes a version-2 intent and its four
// object receipts once the runtime capture record is durably complete. The
// objects stay: they are ordinary snapshot cache, owned by the snapshot row and
// imaged's GC. Qualification intents are never released here.
func (j *linuxNativeSnapshotPublicationJournal) ReleaseCompletedRuntimeCapture(ctx context.Context, authority nativeRuntimeCaptureAuthority) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.checkLocked(); err != nil {
		return err
	}
	capture := nativeRuntimeCaptureID(authority.Keys.Memory)
	intent, err := j.readLocked(ctx, capture)
	if err != nil {
		return err
	}
	authority.NativeLease.processGeneration = 0
	if intent.Version != 2 || intent.Runtime != authority {
		return errors.New("native snapshot publication: release requires the original runtime intent")
	}
	for _, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		if _, err := j.readObjectLocked(ctx, intent, kind); err != nil {
			return errors.Join(err, errors.New("native snapshot publication: release requires every original object receipt"))
		}
	}
	for _, kind := range [...]string{"mem", "vmstate", "drive", "backing"} {
		if err := unix.Unlinkat(int(j.owner.Fd()), nativePublicationReceiptName(capture, kind), 0); err != nil {
			return err
		}
	}
	if err := unix.Unlinkat(int(j.owner.Fd()), capture+".json", 0); err != nil {
		return err
	}
	return errors.Join(j.owner.Sync(), ctx.Err())
}
