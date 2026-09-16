//go:build !short

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type pgStorageRollupAdapter struct{ store *state.PgStore }

func (a pgStorageRollupAdapter) ListAllApps(ctx context.Context) ([]meter.AppRow, error) {
	apps, err := a.store.ListAllApps(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]meter.AppRow, 0, len(apps))
	for _, app := range apps {
		rows = append(rows, meter.AppRow{AccountID: app.AccountID, AppID: app.ID})
	}
	return rows, nil
}

func (a pgStorageRollupAdapter) LatestSnapshotBytes(ctx context.Context, appID string) (int64, int64, error) {
	return a.store.LatestSnapshotBytes(ctx, appID)
}

func (a pgStorageRollupAdapter) AppendSnapshotStorage(ctx context.Context, accountID, appID string, day time.Time, snapshotBytes, layerBytes int64) error {
	return a.store.AppendSnapshotStorage(ctx, accountID, appID, day, snapshotBytes, layerBytes)
}

func TestPgRetainedLayerBytesFeedsStorageRollup(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	accountID, appID, deploymentID := seedLiveDeploy(t, store, ctx, "-retained-layer", "retained-layer")
	const rootfsBytes int64 = 25_973_545
	const sidecarBytes int64 = 2_097_152
	if err := store.SetDeploymentRootfs(ctx, deploymentID, "/var/lib/faas/apps/retained-layer/rootfs.ext4", "apps/retained-layer/rootfs.ext4", rootfsBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
		DeploymentID: deploymentID, SidecarName: "proxy", StorageKey: "apps/retained-layer/proxy.ext4",
		Bytes: sidecarBytes, ContentDigest: "sha256:proxy",
	}); err != nil {
		t.Fatal(err)
	}

	adapter := pgStorageRollupAdapter{store: store}
	day := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	written, err := meter.StorageRollupOnce(ctx, adapter, day, store.RetainedLayerBytes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if written < 1 {
		t.Fatalf("written = %d, want at least seeded app", written)
	}
	rows, err := store.StorageUsage(ctx, accountID, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].AppID != appID || rows[0].LayerBytes != rootfsBytes+sidecarBytes {
		t.Fatalf("storage rows = %+v, want app=%s layer_bytes=%d", rows, appID, rootfsBytes+sidecarBytes)
	}
}
