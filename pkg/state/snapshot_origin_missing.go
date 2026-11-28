package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// snapshotOriginMissingPrefix starts a replica's last_error when the canonical
// storage backend answered NotFound for one of the snapshot's objects. It is
// the stable marker SnapshotsMissingFromOrigin reads; keep it unchanged.
const snapshotOriginMissingPrefix = "snapshot origin object missing: "

// SnapshotOriginMissingError classifies a fan-out NotFound from the canonical
// store: the failure is permanent and its recorded message says the origin
// object is gone, so schedd can retire the snapshot (prod hunt r72).
func SnapshotOriginMissingError(err error) error {
	if err == nil {
		return nil
	}
	return PermanentSnapshotReplicaError(fmt.Errorf("%s%w", snapshotOriginMissingPrefix, err))
}

// SnapshotsMissingFromOrigin returns up to limit live (non-stale) snapshots
// that a node's fan-out found missing from the canonical store, where the
// snapshot was already older than grace when the failure was recorded. The
// grace keeps a capture whose upload is still landing from being retired.
func (s *PgStore) SnapshotsMissingFromOrigin(ctx context.Context, grace time.Duration, limit int) ([]string, error) {
	if grace <= 0 || limit <= 0 {
		return nil, errors.New("state: snapshots missing from origin: grace and limit must be positive")
	}
	ids, err := sqlc.New().SnapshotsMissingFromOrigin(ctx, s.pool, sqlc.SnapshotsMissingFromOriginParams{
		GraceSeconds: grace.Seconds(),
		ErrorPattern: snapshotOriginMissingPrefix + "%",
		RowLimit:     int32(min(limit, math.MaxInt32)), // #nosec G115 -- bounded by min
	})
	if err != nil {
		return nil, fmt.Errorf("state: snapshots missing from origin: %w", mapErr(err))
	}
	return ids, nil
}

// SnapshotsMissingFromOrigin mirrors PgStore.SnapshotsMissingFromOrigin.
func (m *MemStore) SnapshotsMissingFromOrigin(_ context.Context, grace time.Duration, limit int) ([]string, error) {
	if grace <= 0 || limit <= 0 {
		return nil, errors.New("memstore: snapshots missing from origin: grace and limit must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var found []Snapshot
	for _, snap := range m.snapshots {
		if snap.Stale || snap.StorageKey == "" || !snap.CreatedAt.Before(now.Add(-grace)) {
			continue
		}
		for key, row := range m.snapshotReplicas {
			if key.snapshotID == snap.ID && row.state == SnapshotReplicaFailed && row.nextAttemptAt.IsZero() &&
				strings.HasPrefix(row.lastError, snapshotOriginMissingPrefix) && !row.updatedAt.Before(snap.CreatedAt.Add(grace)) {
				found = append(found, snap)
				break
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].CreatedAt.Before(found[j].CreatedAt) })
	var out []string
	for i := 0; i < len(found) && i < limit; i++ {
		out = append(out, found[i].ID)
	}
	return out, nil
}
