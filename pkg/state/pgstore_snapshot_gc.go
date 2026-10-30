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

// snapshotGCPageRows bounds one read of the GC projection. A variable so
// tests can page with a handful of rows.
var snapshotGCPageRows int32 = 10000

// One bounded join carries original environment ownership, immutable warm
// policy and physical layer keys through every GC path, including retries.
// The active set is read in pages of whole apps: the rollback window needs
// every row of an app, and the old single newest-first page over the fleet
// dropped the oldest generations once the live set passed the limit (bug
// hunt 2026-10-28 H66).
func (s *PgStore) snapshotGarbageCollection(ctx context.Context, mode string, retentionSeconds int64) ([]SnapshotForGC, error) {
	if mode != "active" {
		return s.snapshotGarbageCollectionPage(ctx, mode, retentionSeconds, "")
	}
	var out []SnapshotForGC
	after := ""
	for {
		page, err := s.snapshotGarbageCollectionPage(ctx, mode, 0, after)
		if err != nil {
			return nil, err
		}
		if len(page) < int(snapshotGCPageRows) {
			return append(out, page...), nil
		}
		// The last app may continue past the page; read it again from the
		// start of the next page, unless it fills the page on its own.
		last := page[len(page)-1].AppID
		cut := len(page)
		for cut > 0 && page[cut-1].AppID == last {
			cut--
		}
		if cut == 0 {
			cut = len(page)
		}
		out = append(out, page[:cut]...)
		after = page[cut-1].AppID
	}
}

func (s *PgStore) snapshotGarbageCollectionPage(ctx context.Context, mode string, retentionSeconds int64, afterAppID string) ([]SnapshotForGC, error) {
	rows, err := sqlc.New().ReadSnapshotGarbageCollection(ctx, s.pool, sqlc.ReadSnapshotGarbageCollectionParams{
		Mode: mode, RetentionSeconds: retentionSeconds, AfterAppID: afterAppID, RowLimit: snapshotGCPageRows,
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
