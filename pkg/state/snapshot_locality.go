package state

import (
	"context"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SnapshotLocality keeps the producer hint separate from verified replicas.
// Publishing a snapshot may succeed even if the producer's cache write fails;
// neither field is an admission requirement or a promise of page residency.
type SnapshotLocality struct {
	OriginNodeID string
	ReadyNodeIDs []string
}

// SnapshotLocalityStore is optional for compatibility with older stores. The
// combined read adds producer affinity without another wake-path DB round trip.
type SnapshotLocalityStore interface {
	SnapshotLocalityFor(ctx context.Context, snapshotID string) (SnapshotLocality, error)
}

func (s *PgStore) SnapshotLocalityFor(ctx context.Context, snapshotID string) (SnapshotLocality, error) {
	id, err := parsePgUUID(snapshotID)
	if err != nil {
		return SnapshotLocality{}, fmt.Errorf("state: snapshot locality id: %w", err)
	}
	rows, err := sqlc.New().SnapshotLocalityNodes(ctx, s.pool, id)
	if err != nil {
		return SnapshotLocality{}, fmt.Errorf("state: snapshot locality: %w", err)
	}
	var out SnapshotLocality
	for _, row := range rows {
		if row.IsOrigin {
			out.OriginNodeID = row.NodeID
		} else {
			out.ReadyNodeIDs = append(out.ReadyNodeIDs, row.NodeID)
		}
	}
	return out, nil
}

func (m *MemStore) SnapshotLocalityFor(_ context.Context, snapshotID string) (SnapshotLocality, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := SnapshotLocality{OriginNodeID: m.snapshotOrigins[snapshotID].nodeID}
	for key, row := range m.snapshotReplicas {
		if key.snapshotID == snapshotID && row.state == SnapshotReplicaReady {
			out.ReadyNodeIDs = append(out.ReadyNodeIDs, key.nodeID)
		}
	}
	sort.Strings(out.ReadyNodeIDs)
	return out, nil
}
