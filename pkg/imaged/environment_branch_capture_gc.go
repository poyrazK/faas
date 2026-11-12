// adr: 20261031193635
package imaged

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// environmentBranchCaptureObjectGrace keeps a recorded key well past the
	// capture lease (api.EnvironmentBranchCaptureLease), so an attempt that is
	// still writing is never collected.
	environmentBranchCaptureObjectGrace = time.Hour
	// environmentBranchCaptureGCBatch bounds one pass.
	environmentBranchCaptureGCBatch = 50
)

// collectEnvironmentBranchCaptureObjects deletes the objects of capture
// attempts no ready capture references: failed and retried attempts, retired
// captures, and captures of deleted parent deployments. A key is forgotten
// only once every object is gone, so a refused delete retries next pass.
func (l *Loop) collectEnvironmentBranchCaptureObjects(ctx context.Context, be crashCaptureDeleter, now time.Time) {
	objects, ok := l.store.(state.EnvironmentBranchCaptureObjectStore)
	if !ok {
		return
	}
	due, err := objects.EnvironmentBranchCaptureGarbage(ctx, now.Add(-environmentBranchCaptureObjectGrace), environmentBranchCaptureGCBatch)
	if err != nil {
		l.log.Warn("imaged: environment branch capture garbage list", "err", err)
		return
	}
	for _, object := range due {
		if err := deleteCrashCaptureKeys(ctx, be, environmentBranchCaptureKeys(object)); err != nil {
			l.log.Warn("imaged: environment branch capture delete", "key", object.StorageKey, "err", err)
			continue
		}
		if err := objects.ForgetEnvironmentBranchCaptureObject(ctx, object.StorageKey); err != nil && !errors.Is(err, state.ErrConflict) {
			l.log.Warn("imaged: environment branch capture forget", "key", object.StorageKey, "err", err)
		}
	}
}

// environmentBranchCaptureKeys lists an attempt's objects: memory, vmstate,
// private drive and backing identity.
func environmentBranchCaptureKeys(object state.EnvironmentBranchCaptureObject) []string {
	snap := state.Snapshot{DeploymentID: object.DeploymentID, StorageKey: object.StorageKey, Tier: state.SnapshotTierWarm}
	keys := []string{object.StorageKey, state.SnapshotVMStateKey(snap)}
	for _, key := range []string{state.SnapshotDriveKey(snap), state.SnapshotBackingKey(snap)} {
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}
