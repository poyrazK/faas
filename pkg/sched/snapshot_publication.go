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
//
// ADR-20261024225612 amendment 1: the capture must also restore on the node
// the instance runs on. An instance on a CPU platform that has no live row
// for this tier captures one, alongside the other platforms' rows, so that
// platform's next wake restores instead of cold-booting.
func (e *Engine) reusableSnapshotOn(ctx context.Context, depID, tier, nodeID string) (state.Snapshot, bool) {
	rows, err := state.LiveSnapshotsForTier(ctx, e.store, depID, tier)
	if err != nil {
		return state.Snapshot{}, false
	}
	nodes := e.activeNodesForCPU(ctx)
	template, model := e.nodeCPUIdentity(ctx, nodeID)
	node := state.ComputeNode{ID: nodeID, CPUTemplate: template, CPUModel: model}
	legacyOK := legacySnapshotsRestorable(nodes)
	for _, snap := range rows {
		if !snap.Stale && snap.FCVersion == e.fcVer && snap.StorageKey != "" && state.SnapshotDriveKey(snap) != "" &&
			snapshotRestorableOn(snap, node, legacyOK) {
			return snap, true
		}
	}
	return state.Snapshot{}, false
}

func (e *Engine) captureInitOrReuse(ctx context.Context, ins state.Instance, vmstate, memKey, stateKey string, beforeCheckpoint bool) (SnapshotBytes, *state.Snapshot, error) {
	measured, err := e.measuredSnapshotSource(ctx, ins)
	if err != nil {
		return SnapshotBytes{}, nil, err
	}
	// Measured cache reuse needs its own current review and restore admission.
	if snap, ok := e.reusableSnapshotOn(ctx, ins.DeploymentID, state.SnapshotTierInit, ins.NodeID); ok && !measured {
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
