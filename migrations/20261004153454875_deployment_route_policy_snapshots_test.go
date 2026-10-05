//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_20261004153454875DeploymentRoutePolicySnapshots(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	var tableExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('deployment_route_policy_snapshots') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatalf("query snapshot table: %v", err)
	}
	if !tableExists {
		t.Fatal("deployment_route_policy_snapshots table missing")
	}
	for _, column := range []string{"deployment_id", "app_id", "scope", "snapshot", "sha256", "schema_version", "captured_at"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = 'deployment_route_policy_snapshots' AND column_name = $1
		)`, column).Scan(&exists); err != nil {
			t.Fatalf("query column %s: %v", column, err)
		}
		if !exists {
			t.Errorf("snapshot column %q missing", column)
		}
	}
	var indexExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('deployment_route_policy_snapshots_app_scope_idx') IS NOT NULL`).Scan(&indexExists); err != nil {
		t.Fatalf("query snapshot index: %v", err)
	}
	if !indexExists {
		t.Fatal("deployment route-policy app/scope index missing")
	}
}
