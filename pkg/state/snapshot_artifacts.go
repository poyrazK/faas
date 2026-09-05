package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SnapshotArtifactStore supplies exact keys for deletion, including stale
// generations. A compute-node cache listing is not a registry inventory.
type SnapshotArtifactStore interface {
	SnapshotStorageKeys(context.Context, string) ([]string, error)
}

func (s *PgStore) SnapshotStorageKeys(ctx context.Context, deploymentID string) ([]string, error) {
	return sqlc.New().SnapshotStorageKeys(ctx, s.pool, mustPgUUID(deploymentID))
}

func (m *MemStore) SnapshotStorageKeys(_ context.Context, deploymentID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for _, snap := range m.snapshots {
		if snap.DeploymentID == deploymentID {
			keys = append(keys, snap.StorageKey)
		}
	}
	return keys, nil
}
