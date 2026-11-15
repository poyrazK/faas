package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// CaptureSnapshotWithStandards exposes the park/warm capture entry point
// (ADR-20261110210000 slice 3d-3 tests).
func (e *Engine) CaptureSnapshotWithStandards(ctx context.Context, ins state.Instance, vmstate, memKey, stateKey string, before bool, mode string) (SnapshotBytes, error) {
	return e.captureSnapshotWithStandards(ctx, ins, vmstate, memKey, stateKey, before, mode)
}

// SnapshotWrittenPayload exposes the snapshot_written payload builder.
func (e *Engine) SnapshotWrittenPayload(ctx context.Context, deploymentID, storageKey string, b SnapshotBytes, tier string) []byte {
	return e.snapshotWrittenPayload(ctx, "", time.Time{}, deploymentID, "", "", storageKey, b, tier, nil)
}

// PlainSnapshotCounts reports the fake's plaintext park and warm captures.
func (r *warmResumeFakeVMM) PlainSnapshotCounts() (park, warm int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshots, r.warmSnapshots
}
