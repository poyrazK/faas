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
	pool := pgtest.OpenMigrated(t)
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
		"20261017190000001_org_access_revocation.sql",
		"20261017190000002_org_app_quota_ownership.sql",
		"20261017190000003_org_resource_lifecycle.sql",
		"20261017190000004_org_project_ownership.sql",
		"20261017190000005_org_managed_postgres_ownership.sql",
		"20261017190000006_org_object_storage_ownership.sql",
		"20261017190000007_org_queue_ownership.sql",
	}
	ups, downs := make([]string, len(names)), make([]string, len(names))
	for i, name := range names {
		raw, err := fs.ReadFile(sources, name)
		if err != nil {
			t.Fatal(err)
		}
		var ok bool
		ups[i], downs[i], ok = strings.Cut(string(raw), "-- +goose Down")
		if !ok {
			t.Fatalf("%s has no rollback", name)
		}
	}
	for i := len(names) - 1; i >= 0; i-- {
		if _, err := pool.Exec(ctx, downs[i]); err != nil {
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
	for i, up := range ups {
		if _, err := pool.Exec(ctx, up); err != nil {
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
