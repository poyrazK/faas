package state

import (
	"context"
	"time"
)

// NetworkReading says which node's vmmd produced a cumulative network reading
// (ADR-20261122090000). A live migration moves an instance to a node whose
// counter starts from zero; the source node's final reading arrives as a
// departed row.
type NetworkReading struct {
	// NodeID is the node whose counter this is; empty when unknown.
	NodeID string
	// Departed marks the final reading of an instance that left NodeID.
	Departed bool
}

// NodeNetworkObserver checkpoints cumulative network readings per node of
// origin. internal is vmmd's cumulative internal-egress reading, or nil when
// internal egress stays billed (see InternalEgressObserver).
type NodeNetworkObserver interface {
	AppendNodeNetworkUsageObservation(ctx context.Context, accountID, appID, instanceID string, minute time.Time, reading NetworkReading,
		netTxCumulative int64, netTxValid bool, netRxCumulative int64, netRxValid bool, internal *int64) (netTx, netRx, excluded int64, err error)
}

// networkReadingStep relates a reading to the node its checkpoint counts.
// skip: a departed reading from a node the checkpoint already left behind,
// whose bytes were applied before the switch. fresh: a live reading from
// another node, a new counter whose whole value is new usage. node is the
// checkpoint's node afterwards.
func networkReadingStep(checkpointNode string, r NetworkReading) (skip, fresh bool, node string) {
	switch {
	case r.NodeID == "":
		return false, false, checkpointNode
	case checkpointNode == "" || checkpointNode == r.NodeID:
		return false, false, r.NodeID
	case r.Departed:
		return true, false, checkpointNode
	default:
		return false, true, r.NodeID
	}
}
