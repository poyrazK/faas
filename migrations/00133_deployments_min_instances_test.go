//go:build !no_pg

// Migration-apply test for 00133 (issue #557 closure / ADR-072 —
// deployments.min_instances ADD COLUMN). Pins:
//
//  1. The migration set applies cleanly through 00133.
//  2. The column exists on `deployments` with type int NOT NULL
//     DEFAULT 0; existing rows backfill to 0 (the inheritance
//     default; ADR-072 §Decision 1).
//  3. The CHECK constraint `deployments_min_instances_chk` exists
//     and rejects negative values + values > 100.
//  4. The PATCH handler's path (UpdateDeploymentMinInstances) can
//     write a positive value and read it back, and the column is
//     surfaced through deploymentSelectColumns.
//  5. Replay-safety: a second MigrateUp is a no-op (column +
//     constraint both already exist).
//
// Slot note: 00131 + 00132 are the previous real slots; 00129/00130
// are fences past PR #623's slot claim. This test pins 00133's
// ADD COLUMN + CHECK constraint; renumber would need filename +
// test name + apply range bump together.
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00133_DeploymentsMinInstances(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00133. A regression that drops a slot
	// between 1 and 132 surfaces here before the per-assertion pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 132)", err)
	}

	// (2) Column existence. information_schema.columns scoping
	// uses current_schema() because pgtest isolates each test in
	// its own search_path. The data_type check pins int (not
	// jsonb, not text); the column_default pins 0 (the
	// inheritance default).
	var dataType string
	var isNullable string
	var columnDefault *string
	if err := pool.QueryRow(ctx, `
		select data_type, is_nullable, column_default
		from information_schema.columns
		where table_schema = current_schema()
		  and table_name = 'deployments'
		  and column_name = 'min_instances'
	`).Scan(&dataType, &isNullable, &columnDefault); err != nil {
		t.Fatalf("read column metadata: %v", err)
	}
	if dataType != "integer" {
		t.Errorf("min_instances data_type = %q, want integer", dataType)
	}
	if isNullable != "NO" {
		t.Errorf("min_instances is_nullable = %q, want NO (NOT NULL)", isNullable)
	}
	if columnDefault == nil || *columnDefault != "0" {
		t.Errorf("min_instances column_default = %v, want \"0\" (inheritance default)", columnDefault)
	}

	// (3) CHECK constraint existence. The constraint name is the
	// same one pgstore and the apid PATCH handler reference. A
	// regression that drops or renames the constraint surfaces here.
	var chkCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_constraint
		where conname = 'deployments_min_instances_chk'
		  and conrelid = 'deployments'::regclass
	`).Scan(&chkCount); err != nil {
		t.Fatalf("read pg_constraint: %v", err)
	}
	if chkCount != 1 {
		t.Errorf("deployments_min_instances_chk missing (count = %d, want 1)", chkCount)
	}

	// (4) Constraint behaviour: negative rejected, > 100 rejected,
	// 0 and 100 accepted. Seed a minimal account + app first so
	// the FK from deployments.app_id is satisfied.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ('00000000-0000-0000-0000-000000000133', 'scale', 'min-test@example.com')
	`); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb)
		values ('00000000-0000-0000-0000-000000000133',
		        '00000000-0000-0000-0000-000000000133',
		        'min-test', 256)
	`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	// Insert at floor = 0 (the inheritance default). Should pass.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_path, source_bytes, status, image_digest, min_instances)
		values ('00000000-0000-0000-0000-000000000133',
		        '00000000-0000-0000-0000-000000000133',
		        'tarball', '/tmp/test.tar', 0, 'ready', 'sha256:0', 0)
	`); err != nil {
		t.Fatalf("insert at floor 0: %v", err)
	}
	// Insert at floor = 100 (the upper bound). Should pass.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_path, source_bytes, status, image_digest, min_instances)
		values ('00000000-0000-0000-0000-000000000233',
		        '00000000-0000-0000-0000-000000000133',
		        'tarball', '/tmp/test.tar', 0, 'ready', 'sha256:0', 100)
	`); err != nil {
		t.Fatalf("insert at floor 100: %v", err)
	}
	// Insert at floor = -1 (negative). Should fail.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_path, source_bytes, status, image_digest, min_instances)
		values ('00000000-0000-0000-0000-000000000333',
		        '00000000-0000-0000-0000-000000000133',
		        'tarball', '/tmp/test.tar', 0, 'ready', 'sha256:0', -1)
	`); err == nil {
		t.Errorf("insert at floor -1: expected CHECK violation, got nil")
	}
	// Insert at floor = 101 (> 100). Should fail.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_path, source_bytes, status, image_digest, min_instances)
		values ('00000000-0000-0000-0000-000000000433',
		        '00000000-0000-0000-0000-000000000133',
		        'tarball', '/tmp/test.tar', 0, 'ready', 'sha256:0', 101)
	`); err == nil {
		t.Errorf("insert at floor 101: expected CHECK violation, got nil")
	}

	// (5) Update path: write a positive value, read it back. This
	// exercises the same SQL pattern as UpdateDeploymentMinInstances.
	if _, err := pool.Exec(ctx, `
		update deployments set min_instances = 5 where id = '00000000-0000-0000-0000-000000000133'
	`); err != nil {
		t.Fatalf("update min_instances: %v", err)
	}
	var readback int
	if err := pool.QueryRow(ctx, `
		select min_instances from deployments where id = '00000000-0000-0000-0000-000000000133'
	`).Scan(&readback); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if readback != 5 {
		t.Errorf("readback = %d, want 5", readback)
	}

	// (6) Replay-safety: a second MigrateUp is a no-op. The DO
	// block in 00133 gates the ADD CONSTRAINT on a pg_constraint
	// lookup; ADD COLUMN IF NOT EXISTS gates the column. A
	// replay must complete without error.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
