//go:build !no_pg

package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationBackfillsLegacyAppDeletionDeadlineAndRootfsKey(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpOnce(ctx, t, pool)
	accountID := seedAccount(t, ctx, pool)
	appID := seedApp(t, ctx, pool, accountID)
	deploymentID := seedDeployment(t, ctx, pool, appID, "live")

	if _, err := pool.Exec(ctx, `alter table apps disable trigger apps_stamp_deletion_deadline`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update apps set status='deleted', deleted_at=null, delete_grace_until=null where id=$1`, appID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update deployments set rootfs_path='/legacy/custom/rootfs.ext4', rootfs_key='' where id=$1`, deploymentID); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("20260913221000000_app_deletion_deadline_backfill.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(string(body), "-- +goose Down", 2)[0]
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatalf("reapply migration Up: %v", err)
	}

	var complete bool
	if err := pool.QueryRow(ctx, `select deleted_at is not null and delete_grace_until >= deleted_at + interval '7 days' from apps where id=$1`, appID).Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("legacy tombstone did not receive a full migration-time grace window")
	}
	var rootfsKey string
	if err := pool.QueryRow(ctx, `select rootfs_key from deployments where id=$1`, deploymentID).Scan(&rootfsKey); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rootfsKey, "apps/") || !strings.HasSuffix(rootfsKey, "/rootfs.ext4") {
		t.Fatalf("backfilled rootfs_key = %q", rootfsKey)
	}

	secondAppID := seedApp(t, ctx, pool, accountID)
	if _, err := pool.Exec(ctx, `update apps set status='deleted' where id=$1`, secondAppID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select deleted_at is not null and delete_grace_until is not null from apps where id=$1`, secondAppID).Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if !complete {
		t.Fatal("delete trigger did not stamp a complete tombstone")
	}
}
