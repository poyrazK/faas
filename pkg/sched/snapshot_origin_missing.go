package sched

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	// SnapshotOriginMissingGrace is how old a snapshot must have been when a
	// node's fan-out found its object missing from the canonical store before
	// schedd retires it. A capture's upload lands well inside it.
	SnapshotOriginMissingGrace = time.Hour
	// SnapshotOriginMissingInterval is how often schedd looks for them.
	SnapshotOriginMissingInterval = 5 * time.Minute
	// snapshotOriginMissingBatch bounds one pass.
	snapshotOriginMissingBatch = 50
)

// SnapshotOriginMissingStore is the store surface of the sweep.
type SnapshotOriginMissingStore interface {
	SnapshotsMissingFromOrigin(ctx context.Context, grace time.Duration, limit int) ([]string, error)
	MarkSnapshotStale(ctx context.Context, snapshotID string) error
}

// RetireSnapshotsMissingFromOrigin marks stale every live snapshot a node's
// fan-out found missing from the canonical store after its grace. Snapshots
// are cache, not truth (ADR-005): the app's next wake cold-boots and captures
// a new one. Before this, the hourly fan-out requeue failed such a snapshot on
// every node forever and the next wake paid a failed restore first (prod hunt
// r72). It returns how many it marked.
func RetireSnapshotsMissingFromOrigin(ctx context.Context, store SnapshotOriginMissingStore, log *slog.Logger) (int, error) {
	ids, err := store.SnapshotsMissingFromOrigin(ctx, SnapshotOriginMissingGrace, snapshotOriginMissingBatch)
	if err != nil {
		return 0, fmt.Errorf("sched: snapshots missing from origin: %w", err)
	}
	marked := 0
	for _, id := range ids {
		if err := store.MarkSnapshotStale(ctx, id); err != nil {
			log.Warn("sched: mark snapshot with missing origin object stale", "snapshot_id", id, "err", err)
			continue
		}
		marked++
		log.Info("sched: snapshot object missing from storage; marked stale, next wake cold-boots", "snapshot_id", id)
	}
	return marked, nil
}

// RunSnapshotOriginMissingSweep runs RetireSnapshotsMissingFromOrigin every
// SnapshotOriginMissingInterval until ctx ends.
func RunSnapshotOriginMissingSweep(ctx context.Context, store SnapshotOriginMissingStore, log *slog.Logger) {
	ticker := time.NewTicker(SnapshotOriginMissingInterval)
	defer ticker.Stop()
	for {
		if _, err := RetireSnapshotsMissingFromOrigin(ctx, store, log); err != nil && ctx.Err() == nil {
			log.Warn("sched: snapshot origin sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
