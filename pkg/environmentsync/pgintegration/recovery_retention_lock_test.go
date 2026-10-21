package pgintegration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Recovery retention guards run on every deployment artifact write. An account
// without a retention policy must not serialize those writes on its account
// row: foreign-key checks and ordinary account updates proceed while one is
// open (ADR-20261017190807).
func TestPgRecoveryRetentionGuardLeavesAccountsWithoutPolicyUnlocked(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	source, _, app := intentFixture(t, store, "report")
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: source.EnvironmentSlug, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example/shop@sha256:" + strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	tag, err := writer.Exec(ctx, `UPDATE deployments SET rootfs_path=$2,rootfs_key=$3,rootfs_bytes=4096 WHERE id=$1`,
		dep.ID, "/srv/fc/apps/"+dep.ID+".ext4", "apps/"+app.Slug+"/"+dep.ID+".ext4")
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("artifact write: %v %d", err, tag.RowsAffected())
	}
	other, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback(ctx) }()
	if _, err := other.Exec(ctx, "SET LOCAL lock_timeout = '2s'"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"SELECT 1 FROM accounts WHERE id=$1 FOR KEY SHARE",     // every foreign-key check
		"SELECT 1 FROM accounts WHERE id=$1 FOR NO KEY UPDATE", // ordinary account updates
	} {
		if _, err := other.Exec(ctx, statement, source.AccountID); err != nil {
			t.Fatalf("%s waited on a deployment write without a retention policy: %v", statement, err)
		}
	}
}
