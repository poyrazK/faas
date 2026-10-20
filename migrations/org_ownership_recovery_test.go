//go:build !no_pg

package migrations_test

// adr: 202610171904, 202610171908

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// Ownership rollback must preserve the newer migration-capacity admission
// accounting, and replay must restore the same company policy and constraints.
func TestOrganizationOwnershipDownUpPreservesCapacityAccounting(t *testing.T) {
	pool := openMigrated(t)
	ctx := t.Context()
	var schema, before string
	if err := pool.QueryRow(ctx, `SELECT current_schema(), pg_get_functiondef('service_capacity_snapshot()'::regprocedure)`).Scan(&schema, &before); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(before, "FROM migration_admissions") {
		t.Fatal("ownership expansion lost incoming migration capacity accounting")
	}
	constraints := replayConstraintFingerprint(t, pool)
	sources := migrations.SourcesForSchema(schema)
	names := []string{
		"20261018003100001_org_access_revocation.sql",
		"20261018003100002_org_app_quota_ownership.sql",
		"20261019190114207_org_resource_lifecycle_online.sql",
		"20261019190114312_org_project_ownership_online.sql",
		"20261019190114433_org_managed_postgres_ownership_online.sql",
		"20261019190114551_org_object_storage_ownership_online.sql",
		"20261019190114676_org_queue_ownership_online.sql",
	}
	raws := make([]string, len(names))
	for i, name := range names {
		raw, err := fs.ReadFile(sources, name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "-- +goose Down") {
			t.Fatalf("%s has no rollback", name)
		}
		raws[i] = string(raw)
	}
	// The online steps commit inside DO blocks, so each runs on its own as goose runs it.
	for i := len(names) - 1; i >= 0; i-- {
		if err := pgtest.ExecMigration(ctx, t, pool, raws[i], false); err != nil {
			t.Fatalf("rollback %s: %v", names[i], err)
		}
	}
	var rolledBack string
	if err := pool.QueryRow(ctx, `SELECT pg_get_functiondef('service_capacity_snapshot()'::regprocedure)`).Scan(&rolledBack); err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(before, "JOIN app_resource_accounts ac ON ac.app_id=a.id", "JOIN accounts ac ON ac.id=a.account_id")
	if rolledBack != want {
		t.Fatal("rollback changed capacity accounting beyond restoring personal policy")
	}
	for i, raw := range raws {
		if err := pgtest.ExecMigration(ctx, t, pool, raw, true); err != nil {
			t.Fatalf("reapply %s: %v", names[i], err)
		}
	}
	var restored string
	if err := pool.QueryRow(ctx, `SELECT pg_get_functiondef('service_capacity_snapshot()'::regprocedure)`).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if restored != before || replayConstraintFingerprint(t, pool) != constraints {
		t.Fatal("ownership down/up changed capacity policy or constraints")
	}
}
