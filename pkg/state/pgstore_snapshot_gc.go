package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListSnapshotsForGC(ctx context.Context) ([]SnapshotForGC, error) {
	return s.snapshotGarbageCollection(ctx, "active", 0)
}

func (s *PgStore) ListSnapshotsStaleOlderThan(ctx context.Context, retention time.Duration) ([]SnapshotForGC, error) {
	return s.snapshotGarbageCollection(ctx, "stale", int64(retention.Seconds()))
}

func (s *PgStore) ListSnapshotsPendingDelete(ctx context.Context) ([]SnapshotForGC, error) {
	return s.snapshotGarbageCollection(ctx, "pending", 0)
}

// One bounded join carries original environment ownership, immutable warm
// policy and physical layer keys through every GC path, including retries.
func (s *PgStore) snapshotGarbageCollection(ctx context.Context, mode string, retentionSeconds int64) ([]SnapshotForGC, error) {
	rows, err := sqlc.New().ReadSnapshotGarbageCollection(ctx, s.pool, sqlc.ReadSnapshotGarbageCollectionParams{
		Mode: mode, RetentionSeconds: retentionSeconds,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]SnapshotForGC, 0, len(rows))
	for _, row := range rows {
		out = append(out, SnapshotForGC{
			ID: uuidString(row.ID), DeploymentID: row.DeploymentID, AppID: row.AppID, AccountID: row.AccountID,
			AppSlug: row.AppSlug, AppStatus: AppStatus(row.AppStatus), DeploymentStatus: DeploymentStatus(row.DeploymentStatus),
			Scope: row.Scope, EnvironmentID: row.EnvironmentID, RuntimeOwnerInvalid: row.RuntimeOwnerInvalid,
			DeploymentRootfsKey: row.DeploymentRootfsKey, AppWarmSnapshotEnabled: row.WarmSnapshotEnabled,
			FCVersion: row.FcVersion, MemBytes: row.MemBytes, DiskBytes: row.DiskBytes,
			Tier: row.Tier, StorageKey: row.StorageKey, Stale: row.Stale, DeletePending: row.DeletePending, CreatedAt: row.CreatedAt.Time,
		})
	}
	return out, nil
}
