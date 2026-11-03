package state

import (
	"context"
	"time"
)

// SnapshotRestorePressureLeases records in-flight snapshot restores per node
// so every schedd's placement can steer restores away from a node that is
// already busy restoring. Restore pressure is a soft placement signal (a
// watermark filter and a tie-break), so the leases need no critical section:
// a reader counts the committed leases of other schedds and adds its own
// in-flight restores itself (ADR 20261103195512). The optional interface keeps
// other Store implementations source-compatible.
type SnapshotRestorePressureLeases interface {
	// ActiveSnapshotRestoreCounts counts unexpired leases per node, leaving
	// out the caller's own lease IDs (which it already counts locally).
	ActiveSnapshotRestoreCounts(ctx context.Context, excludeLeaseIDs []string) (map[string]int, error)
	// ReserveSnapshotRestore records leaseID on nodeID for ttl. The write may
	// finish in the background; release removes the lease, and the expiry
	// covers process crashes.
	ReserveSnapshotRestore(ctx context.Context, leaseID, nodeID string, ttl time.Duration) (release func(), err error)
}
