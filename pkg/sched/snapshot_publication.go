package sched

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// A snapshot is reusable deployment cache, not persistence of the most recent
// invocation. Keep a healthy capture instead of uploading the same tier on each
// idle park. A stale or missing row still takes the normal capture path.
func (e *Engine) reusableSnapshot(ctx context.Context, depID, tier string) (state.Snapshot, bool) {
	snap, err := e.store.LatestSnapshotForTier(ctx, depID, tier)
	return snap, err == nil && !snap.Stale && snap.FCVersion == e.fcVer &&
		snap.StorageKey != "" && state.SnapshotDriveKey(snap) != ""
}

func (e *Engine) captureInitOrReuse(ctx context.Context, ins state.Instance, vmstate, memKey, stateKey string, beforeCheckpoint bool) (SnapshotBytes, *state.Snapshot, error) {
	measured, err := e.measuredSnapshotSource(ctx, ins)
	if err != nil {
		return SnapshotBytes{}, nil, err
	}
	// Measured cache reuse needs its own current review and restore admission.
	if snap, ok := e.reusableSnapshot(ctx, ins.DeploymentID, state.SnapshotTierInit); ok && !measured {
		err := e.vmm.Destroy(ctx, ins.NodeID, ins.ID)
		return SnapshotBytes{MemBytes: snap.MemBytes, VMStateBytes: snap.DiskBytes, StoredBytes: snap.StoredBytes}, &snap, err
	}
	b, err := e.captureSnapshotWithStandards(ctx, ins, vmstate, memKey, stateKey, beforeCheckpoint, "park")
	return b, nil, err
}

func (e *Engine) snapshotStateLocators(nodeID string, snap state.Snapshot) (string, string) {
	key := state.SnapshotVMStateKey(snap)
	hostPath := filepath.Join(SnapDir(), strings.TrimPrefix(key, "snap/"))
	if nodeID == e.defaultLocalNodeID && !state.IsSnapshotCaptureKey(snap.StorageKey) {
		return hostPath, ""
	}
	return hostPath, key
}
