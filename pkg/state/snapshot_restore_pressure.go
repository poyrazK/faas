package state

import (
	"context"
	"time"
)

// SnapshotRestorePressureCoordinator provides a short fleet-wide critical
// section around reading restore pressure and reserving the selected node.
// The optional interface keeps other Store implementations source-compatible.
type SnapshotRestorePressureCoordinator interface {
	AcquireSnapshotRestorePressure(ctx context.Context) (SnapshotRestorePressureSession, error)
}

// SnapshotRestorePressureSession is held only while a placement decision is
// made. A returned release function removes the durable lease after the
// snapshot restore RPC finishes; the lease expiry covers process crashes.
type SnapshotRestorePressureSession interface {
	ActiveSnapshotRestoreCounts(ctx context.Context) (map[string]int, error)
	ReserveSnapshotRestore(ctx context.Context, nodeID string, ttl time.Duration) (release func(), err error)
	Close()
}
