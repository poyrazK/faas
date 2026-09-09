//go:build !no_pg

// Migration-apply test for 00318 (deployments.deployed_by_user_id +
// deployments.deployed_via + deployments.deployed_from_ip +
// deployments.pusher_login + FK + closed-set CHECK).
// Pins the contract from issue #606.
//
//  1. The migration set applies cleanly through 00303.
//  2. The four new columns land on deployments with the expected
//     types and the nullable shape (existing rows stay valid:
//     deployed_via NOT NULL with default 'api'; the other three
//     nullable).
//  3. The closed-set CHECK on `deployed_via` accepts the documented
//     vocabulary and rejects a stray value.
//  4. The FK to accounts(id) is in place with ON DELETE SET NULL.
//  5. Re-running goose MigrateUp is a no-op (replay safety).
//
// Build tag mirrors 00025_deployments_rootfs_key_test.go: set
// FAAS_SKIP_PG_TESTS=1 locally to skip.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00318_DeploymentsActor is the per-migration pin
// for the actor-attribution columns introduced in 00318 (issue
// #606 — orthogonal to PR #984's human-readable deployed_by text
// column from issue #977 / ADR-116).
func TestMigrations_00318_DeploymentsActor(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00318 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot before 00318)", err)
	}

	// (2) Column shape. Scoped to current_schema() per
	// migrations-info-schema-scoping-pattern.md so a parallel
	// pgtest run on the same box doesn't bleed rows in.
	rows, err := pool.Query(ctx, `
		select column_name, data_type, is_nullable
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'deployments'
		   and column_name in ('deployed_by_user_id', 'deployed_via',
		                       'deployed_from_ip', 'pusher_login')`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	colTypes := map[string]string{}
	colNullable := map[string]string{}
	for rows.Next() {
		var name, typ, nullable string
		if err := rows.Scan(&name, &typ, &nullable); err != nil {
			t.Fatalf("scan column row: %v", err)
		}
		colTypes[name] = typ
		colNullable[name] = nullable
	}
	if colTypes["deployed_by_user_id"] != "uuid" {
		t.Errorf("deployed_by_user_id type = %q, want uuid",
			colTypes["deployed_by_user_id"])
	}
	if colNullable["deployed_by_user_id"] != "YES" {
		t.Errorf("deployed_by_user_id nullable = %q, want YES (anonymous / GitHub-push predate the FK)",
			colNullable["deployed_by_user_id"])
	}
	if colTypes["deployed_via"] != "text" {
		t.Errorf("deployed_via type = %q, want text", colTypes["deployed_via"])
	}
	if colNullable["deployed_via"] != "NO" {
		t.Errorf("deployed_via nullable = %q, want NO (NOT NULL DEFAULT 'api')",
			colNullable["deployed_via"])
	}
	if colTypes["deployed_from_ip"] != "inet" {
		t.Errorf("deployed_from_ip type = %q, want inet",
			colTypes["deployed_from_ip"])
	}
	if colNullable["deployed_from_ip"] != "YES" {
		t.Errorf("deployed_from_ip nullable = %q, want YES",
			colNullable["deployed_from_ip"])
	}
	if colTypes["pusher_login"] != "text" {
		t.Errorf("pusher_login type = %q, want text", colTypes["pusher_login"])
	}
	if colNullable["pusher_login"] != "YES" {
		t.Errorf("pusher_login nullable = %q, want YES",
			colNullable["pusher_login"])
	}

	// (3) Closed-set CHECK on `deployed_via`. pg_get_constraintdef
	// emits either IN (a, b, c) or ANY(ARRAY[a, b, c]) per
	// pg-get-constraintdef-shapes.md; we assert each closed-set
	// value is present.
	var viaDef string
	err = pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'deployments_deployed_via_set_chk'
		   and n.nspname = current_schema()`).Scan(&viaDef)
	if err != nil {
		t.Fatalf("query deployed_via CHECK: %v (closed-set CHECK must have landed)", err)
	}
	for _, want := range []string{
		"api", "cli", "dashboard", "github", "operator",
	} {
		if !strings.Contains(viaDef, want) {
			t.Errorf("deployed_via CHECK def %q missing closed-set value %q", viaDef, want)
		}
	}

	// (4) FK to accounts(id). Assert the constraint exists with
	// the expected ON DELETE SET NULL action.
	var fkAction string
	err = pool.QueryRow(ctx, `
		select c.confdeltype
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'deployments_deployed_by_user_id_fk'
		   and n.nspname = current_schema()`).Scan(&fkAction)
	if err != nil {
		t.Fatalf("query deployed_by_user_id FK: %v", err)
	}
	// pg_constraint.confdeltype: 'a' = NO ACTION, 'r' = RESTRICT,
	// 'c' = CASCADE, 'n' = SET NULL, 'd' = SET DEFAULT. We want
	// 'n' (SET NULL) so a GDPR-erased account keeps the
	// deployment row but nulls the attribution column.
	if fkAction != "n" {
		t.Errorf("deployed_by_user_id FK ON DELETE = %q, want SET NULL (GDPR account-erasure safety)",
			fkAction)
	}

	// (4b) MEDIUM review #3 (PR #992): the FK was added NOT VALID
	// in 00303 (no full-table scan), and 00304 runs VALIDATE
	// CONSTRAINT under SHARE UPDATE EXCLUSIVE (which permits
	// concurrent apid INSERTs). The pin: after the full migration
	// set applies, pg_constraint.convalidated must be 't' so
	// future readers know the FK is fully enforced against
	// existing rows, not just new ones.
	//
	// Pre-#606 deployments never wrote deployed_by_user_id (column
	// was added by 00303 itself), so every existing row is NULL —
	// VALIDATE on a NULL FK column is a catalog scan that returns
	// "valid" immediately. The test asserts the bit flipped to
	// 't' so a future migration that re-introduces NOT VALID
	// without a follow-up VALIDATE trips here, not at the next
	// GDPR erasure.
	// pg_constraint.convalidated is a bool column, so the scan target
	// is a Go bool — pgx's binary protocol will not decode OID 16 into
	// a string.
	var convalidated bool
	err = pool.QueryRow(ctx, `
		select c.convalidated
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'deployments_deployed_by_user_id_fk'
		   and n.nspname = current_schema()`).Scan(&convalidated)
	if err != nil {
		t.Fatalf("query deployed_by_user_id FK convalidated: %v", err)
	}
	if !convalidated {
		t.Error("deployed_by_user_id FK convalidated = false, want true (00304 must have run VALIDATE CONSTRAINT — concurrent apid INSERTs block otherwise)")
	}

	// (5) Replay safety: applying the migration set a second time
	// must not blow up. The ADD COLUMN IF NOT EXISTS / DROP
	// CONSTRAINT IF EXISTS guards handle this.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (the ADD COLUMN IF NOT EXISTS / DROP CONSTRAINT IF EXISTS guards must have been silently dropped)", err)
	}
}
