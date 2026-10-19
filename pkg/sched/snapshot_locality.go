package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// snapshotPlacementHints preserves a known warm location, then prefers the
// producer for the first wake. The producer has no fan-out replica row, so
// checking only ready replicas used to discard its locality. ChoosePlacement
// still checks liveness and capacity and can fall back to another node.
func (e *Engine) snapshotPlacementHints(ctx context.Context, snapshotID, warmHint string) (string, []string, bool) {
	return e.placementHintsFrom(ctx, e.readSnapshotLocality(ctx, snapshotID), warmHint)
}

// snapshotLocalityRead is one read of a snapshot's locality. A wake makes it
// while it checks the snapshot it is about to choose (ADR-1741).
type snapshotLocalityRead struct {
	snapshotID string
	locality   state.SnapshotLocality
	err        error
}

func (e *Engine) readSnapshotLocality(ctx context.Context, snapshotID string) snapshotLocalityRead {
	read := snapshotLocalityRead{snapshotID: snapshotID}
	switch store := e.store.(type) {
	case state.SnapshotLocalityStore:
		read.locality, read.err = store.SnapshotLocalityFor(ctx, snapshotID)
	case state.SnapshotReplicaStore:
		read.locality.ReadyNodeIDs, read.err = store.ReadySnapshotReplicaNodes(ctx, snapshotID)
	}
	return read
}

func (e *Engine) placementHintsFrom(ctx context.Context, read snapshotLocalityRead, warmHint string) (string, []string, bool) {
	locality, err, snapshotID := read.locality, read.err, read.snapshotID
	if err != nil {
		e.log.Debug("snapshot locality lookup failed; using normal placement", "snapshot_id", snapshotID, "err", err)
		return warmHint, nil, false
	}
	known := locality.OriginNodeID != "" || len(locality.ReadyNodeIDs) > 0
	if known &&
		warmHint != locality.OriginNodeID && !containsNodeID(locality.ReadyNodeIDs, warmHint) {
		warmHint = ""
	}
	// Explicit burst spreading must not pin every sibling to the producer.
	if warmHint == "" && !isPlacementSpread(ctx) {
		warmHint = locality.OriginNodeID
	}
	return warmHint, locality.ReadyNodeIDs, known
}
