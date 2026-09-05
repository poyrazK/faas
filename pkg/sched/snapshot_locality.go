package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// snapshotPlacementHints preserves a known warm location, then prefers the
// producer for the first wake. The producer has no fan-out replica row, so
// checking only ready replicas used to discard its locality. ChoosePlacement
// still checks liveness and capacity and can fall back to another node.
func (e *Engine) snapshotPlacementHints(ctx context.Context, snapshotID, warmHint string) (string, []string) {
	var locality state.SnapshotLocality
	var err error
	switch store := e.store.(type) {
	case state.SnapshotLocalityStore:
		locality, err = store.SnapshotLocalityFor(ctx, snapshotID)
	case state.SnapshotReplicaStore:
		locality.ReadyNodeIDs, err = store.ReadySnapshotReplicaNodes(ctx, snapshotID)
	}
	if err != nil {
		e.log.Debug("snapshot locality lookup failed; using normal placement", "snapshot_id", snapshotID, "err", err)
		return warmHint, nil
	}
	if (locality.OriginNodeID != "" || len(locality.ReadyNodeIDs) > 0) &&
		warmHint != locality.OriginNodeID && !containsNodeID(locality.ReadyNodeIDs, warmHint) {
		warmHint = ""
	}
	// Explicit burst spreading must not pin every sibling to the producer.
	if warmHint == "" && !isBurstPlacementSpread(ctx) {
		warmHint = locality.OriginNodeID
	}
	return warmHint, locality.ReadyNodeIDs
}
