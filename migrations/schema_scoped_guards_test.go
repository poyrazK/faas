//go:build !no_pg

// Guard migrations that create a type or constraint conditionally must look
// the object up in the schema they are migrating, not database-wide.
//
// pkg/db/pgtest isolates every test in its own schema on one shared
// database and `go test -p 4` migrates several of them at once. A guard
// such as `IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = ...)` sees
// a sibling schema's object, skips its own CREATE, and the next statement
// fails (`type "compute_node_lifecycle" does not exist`, CI run
// 34163270305) — or, for constraints, silently leaves the schema without
// the constraint the migration promised. This test plants same-named
// decoys in a sibling schema before migrating, which turns that race into
// a deterministic failure for any unscoped guard.

package migrations_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_GuardsAreSchemaScoped(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	var here string
	if err := pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&here); err != nil {
		t.Fatalf("current_schema: %v", err)
	}

	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	decoy := "decoy_" + hex.EncodeToString(suffix[:])

	// Same names as the objects the guarded migrations create, in a schema
	// that is NOT on this test's search_path. Constraint decoys are CHECKs:
	// pg_constraint.conname is what an unscoped guard matches on, the
	// constraint kind is irrelevant.
	stmts := []string{
		fmt.Sprintf(`CREATE SCHEMA %s`, decoy),
		fmt.Sprintf(`CREATE TYPE %s.compute_node_lifecycle AS ENUM ('active')`, decoy),
		fmt.Sprintf(`CREATE TABLE %s.instances (job_id int CONSTRAINT instances_job_id_fk CHECK (job_id IS NOT NULL))`, decoy),
		fmt.Sprintf(`CREATE TABLE %s.compute_nodes (x int CONSTRAINT compute_nodes_last_recovery_outcome_chk CHECK (x > 0))`, decoy),
		fmt.Sprintf(`CREATE TABLE %s.managed_postgres_databases (
			x int CONSTRAINT managed_postgres_restore_source_database_fk CHECK (x > 0),
			y int CONSTRAINT managed_postgres_restore_fields_ck CHECK (y > 0))`, decoy),
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("plant decoy: %v\n%s", err, s)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, decoy))
	})

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate with decoys present: %v", err)
	}

	// The type must resolve on this schema's search_path and live here,
	// not in the decoy schema.
	var typeSchema string
	if err := pool.QueryRow(ctx,
		`SELECT n.nspname FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		  WHERE t.oid = to_regtype('compute_node_lifecycle')`).Scan(&typeSchema); err != nil {
		t.Fatalf("compute_node_lifecycle does not resolve after migration: %v", err)
	}
	if typeSchema != here {
		t.Fatalf("compute_node_lifecycle resolved in schema %q, want %q", typeSchema, here)
	}

	// Every guarded constraint must exist on THIS schema's table.
	for _, c := range []struct{ table, name string }{
		{"instances", "instances_job_id_fk"},
		{"compute_nodes", "compute_nodes_last_recovery_outcome_chk"},
		{"managed_postgres_databases", "managed_postgres_restore_source_database_fk"},
		{"managed_postgres_databases", "managed_postgres_restore_fields_ck"},
	} {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_constraint WHERE conname = $1 AND conrelid = $2::regclass`,
			c.name, c.table).Scan(&n); err != nil {
			t.Fatalf("lookup %s on %s: %v", c.name, c.table, err)
		}
		if n != 1 {
			t.Errorf("constraint %s missing on %s.%s (found %d): the guard matched the decoy schema", c.name, here, c.table, n)
		}
	}
}
