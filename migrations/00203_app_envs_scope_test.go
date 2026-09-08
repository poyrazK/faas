//go:build !no_pg

// Migration-apply test for 00203_app_envs_scope.sql
// (ADR-090 PR-A / multi-scope app envs).
//
// Pins:
//
//  1. Migration set applies cleanly through 00203 (no goose
//     duplicate-version panic). PR-A carries reservation fences at
//     00198, 00199, 00200, 00201, and 00202 (matching files in
//     this directory) to satisfy TestMigrationsContiguous on
//     the branch tip. The renumber chain was
//     00198 → 00199 → 00200 → 00201 → 00202 → 00203 as PR #819,
//     PR #826, and PR #829 claimed the earlier slots (see
//     migrations/00203_app_envs_scope.sql header for the full
//     chain).
//  2. app_envs.scope column exists, is text NOT NULL, and has the
//     DEFAULT 'default' literal (PG11+ fast-default). Every
//     pre-00203 row gets scope='default' lazily on first read/write
//     without an UPDATE rewrite, so the migration is metadata-only.
//  3. PK is widened to (app_id, scope, key) — verified via
//     pg_constraint (NOT pg_index.indkey which is unstable across
//     PG versions per ADR-041). The shape is the replica of
//     00064_invocations_dead_letter.sql's PK swap.
//  4. app_envs_scope_shape CHECK exists with the same regex as
//     cmd/apid/handlers.go:600 (validSlug). A free-form scope
//     string is NOT acceptable per ADR-090 D1.
//  5. Composite index app_envs_account_app_scope_idx (account_id,
//     app_id, scope) exists — supports the scope-aware list path
//     PR-A introduces (ListAppEnvInScope).
//  6. Backfill pin: INSERT a row with no scope column → SELECT
//     back → assert scope = 'default'. Catches a future refactor
//     that drops the DEFAULT clause and breaks wake-time reads of
//     pre-PR rows via the flat (scope='default') surface.
//  7. Replay-safety: the apply_walk_test.go harness runs MigrateUp
//     twice. The migration's IF NOT EXISTS + IF EXISTS guards make
//     the second pass a no-op; the harness fails loudly if the
//     second pass errors.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00203_AppEnvsScope(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// (2) scope column shape: text NOT NULL DEFAULT 'default'.
	rows, err := pool.Query(ctx, `
		SELECT data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'app_envs'
		  AND column_name = 'scope'
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatalf("app_envs.scope column missing")
	}
	var dataType, nullable, columnDefault string
	if err := rows.Scan(&dataType, &nullable, &columnDefault); err != nil {
		rows.Close()
		t.Fatalf("scan scope column: %v", err)
	}
	rows.Close()
	if dataType != "text" {
		t.Errorf("app_envs.scope: got data_type=%s, want text", dataType)
	}
	if nullable != "NO" {
		t.Errorf("app_envs.scope: got nullable=%s, want NO (NOT NULL DEFAULT 'default' is the fast-default that backfills pre-PR rows)", nullable)
	}
	// PG normalizes the default literal to "'default'::text" in
	// information_schema. Accept both common spellings.
	if columnDefault != "'default'::text" && columnDefault != "'default'" {
		t.Errorf("app_envs.scope: got column_default=%q, want 'default' (literal string)", columnDefault)
	}

	// (3) PK widened to (app_id, scope, key). Lookup via
	// pg_constraint so we don't depend on pg_index.indkey which
	// is unstable across PG versions per ADR-041.
	pkRows, err := pool.Query(ctx, `
		SELECT a.attname
		FROM pg_constraint c
		JOIN unnest(c.conkey) WITH ORDINALITY AS u(attnum, ord) ON TRUE
		JOIN pg_attribute a
		  ON a.attrelid = c.conrelid AND a.attnum = u.attnum
		WHERE c.conname = 'app_envs_pkey'
		  AND c.conrelid = 'app_envs'::regclass
		ORDER BY u.ord
	`)
	if err != nil {
		t.Fatalf("query pg_constraint for app_envs_pkey: %v", err)
	}
	var pkCols []string
	for pkRows.Next() {
		var c string
		if err := pkRows.Scan(&c); err != nil {
			pkRows.Close()
			t.Fatalf("scan pk column: %v", err)
		}
		pkCols = append(pkCols, c)
	}
	pkRows.Close()
	if err := pkRows.Err(); err != nil {
		t.Fatalf("pkRows.Err: %v", err)
	}
	wantPK := []string{"app_id", "scope", "key"}
	if len(pkCols) != len(wantPK) {
		t.Fatalf("app_envs_pkey: got %v, want %v (PK must be widened from (app_id, key) to (app_id, scope, key))", pkCols, wantPK)
	}
	for i := range wantPK {
		if pkCols[i] != wantPK[i] {
			t.Errorf("app_envs_pkey col %d: got %s, want %s", i, pkCols[i], wantPK[i])
		}
	}

	// (4) app_envs_scope_shape CHECK exists.
	var chkExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_catalog.pg_constraint
			WHERE conname = 'app_envs_scope_shape'
			  AND conrelid = 'app_envs'::regclass
		)
	`).Scan(&chkExists); err != nil {
		t.Fatalf("query scope_shape constraint: %v", err)
	}
	if !chkExists {
		t.Errorf("app_envs_scope_shape CHECK missing (scope must be a validSlug-shaped identifier, not a free-form string)")
	}

	// (5) composite index exists.
	var idxExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = current_schema()
			  AND tablename = 'app_envs'
			  AND indexname = 'app_envs_account_app_scope_idx'
		)
	`).Scan(&idxExists); err != nil {
		t.Fatalf("query app_envs_account_app_scope_idx: %v", err)
	}
	if !idxExists {
		t.Errorf("app_envs_account_app_scope_idx missing (composite index supports the scope-aware list path)")
	}

	// (6) backfill pin: insert without scope, read back, assert
	// scope='default'. The INSERT uses the historical column
	// list (account_id, app_id, key, value) — the pre-PR shape —
	// so the ONLY way the row gets a scope value is via the
	// NOT NULL DEFAULT clause. A future regression that drops
	// the DEFAULT would fail this assertion and surface here.
	var gotScope string
	if err := pool.QueryRow(ctx, `
		INSERT INTO app_envs (account_id, app_id, key, value)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'PR_A_BACKFILL_KEY', 'v')
		RETURNING scope
	`).Scan(&gotScope); err != nil {
		t.Fatalf("backfill insert: %v", err)
	}
	if gotScope != "default" {
		t.Errorf("backfill: got scope=%q, want 'default' (NOT NULL DEFAULT 'default' must materialize on insert)", gotScope)
	}
}
