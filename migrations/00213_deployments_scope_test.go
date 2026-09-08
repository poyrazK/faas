//go:build !no_pg

// Migration-apply test for 00213_deployments_scope.sql
// (ADR-091 / per-deployment env targeting).
//
// Pins:
//
//  1. Migration set applies cleanly through 00213 (no goose
//     duplicate-version panic). PR-D was renumbered five
//     times during the cross-PR slot gate dance: 00207 → 00208
//     (after PRs #826 and #835 claimed 00207) → 00209 (after
//     PR #838 claimed 00208) → 00211 (after PR #829 paddle
//     claimed 00209) → 00212 (after PR #835's cron-uniqueness
//     migration landed at slot 00210 on main and our 00210
//     fence became a real-migration collision; rebase dropped
//     the 00210 fence and renumbered our real migration) →
//     00213 (after PR #838 won slot 00212 with
//     00212_github_webhook_secrets.sql on the same cluster
//     chase, leaving our 00212 as a duplicate; the second
//     rebase dropped our 00212 file and renumbered once more).
//     The 00207, 00208, and 00209 slots are now held under
//     ADR-041 reservation fences (the 00209 fence is required
//     because TestMigrationsContiguous uses position index —
//     every position N in the embedded set must have a
//     NNNNN-prefix file, even after renumbering away).
//  2. deployments.scope column exists, is text NOT NULL, and has
//     the DEFAULT 'default' literal (PG11+ fast-default). Every
//     pre-00213 deployment gets scope='default' lazily on first
//     read/write without an UPDATE rewrite, so the migration is
//     metadata-only. Backwards-compat: existing wakes behave
//     exactly as today.
//  3. deployments_scope_shape CHECK exists with the same regex
//     as app_envs_scope_shape (00203) and
//     pkg/api/env_scope.go::EnvScopePattern. A free-form scope
//     string is NOT acceptable — scope is a domain-valid slug.
//  4. deployments_app_scope_live_uniq PARTIAL UNIQUE index
//     exists on (app_id, scope) WHERE status='live'. This is
//     the load-bearing invariant for the wake-target selector:
//     at most one live deployment per (app_id, scope). Without
//     it, two live deployments with the same scope would
//     create a non-deterministic wake selector (Postgres would
//     pick whichever row it scanned first). The uniqueness
//     pin below (5) catches a future regression that drops
//     the partial index.
//  5. Uniqueness pin: two status='live' deployments on the same
//     (app_id, scope) must FAIL the second INSERT with a
//     Postgres unique-violation error (SQLSTATE 23505). This
//     is the migration's load-bearing invariant; if the partial
//     index regresses, the second INSERT silently succeeds and
//     the test catches it via the explicit error code check.
//  6. Backfill pin: insert a row WITHOUT specifying scope → read
//     back → assert scope='default'. Catches a future refactor
//     that drops the DEFAULT clause and breaks wake-time reads
//     of pre-PR deployments.
//  7. Replay-safety: the apply_walk_test.go harness runs
//     MigrateUp twice. The migration's IF NOT EXISTS + IF EXISTS
//     guards make the second pass a no-op; the harness fails
//     loudly if the second pass errors.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00213_DeploymentsScope(t *testing.T) {
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
		  AND table_name = 'deployments'
		  AND column_name = 'scope'
	`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatalf("deployments.scope column missing")
	}
	var dataType, nullable, columnDefault string
	if err := rows.Scan(&dataType, &nullable, &columnDefault); err != nil {
		rows.Close()
		t.Fatalf("scan scope column: %v", err)
	}
	rows.Close()
	if dataType != "text" {
		t.Errorf("deployments.scope: got data_type=%s, want text", dataType)
	}
	if nullable != "NO" {
		t.Errorf("deployments.scope: got nullable=%s, want NO (NOT NULL DEFAULT 'default' is the fast-default that backfills pre-PR rows)", nullable)
	}
	// PG normalizes the default literal to "'default'::text" in
	// information_schema. Accept both common spellings.
	if columnDefault != "'default'::text" && columnDefault != "'default'" {
		t.Errorf("deployments.scope: got column_default=%q, want 'default' (literal string)", columnDefault)
	}

	// (3) deployments_scope_shape CHECK exists.
	var chkExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_catalog.pg_constraint
			WHERE conname = 'deployments_scope_shape'
			  AND conrelid = 'deployments'::regclass
		)
	`).Scan(&chkExists); err != nil {
		t.Fatalf("query scope_shape constraint: %v", err)
	}
	if !chkExists {
		t.Errorf("deployments_scope_shape CHECK missing (scope must be a validSlug-shaped identifier, not a free-form string)")
	}

	// (4) deployments_app_scope_live_uniq PARTIAL UNIQUE index
	// exists with the WHERE status='live' predicate. We check
	// the indexdef column for the substring as a robust gate
	// against a future regression that creates a non-partial
	// unique index (which would block multiple non-live
	// deployments on the same scope — a different invariant).
	var indexDef string
	if err := pool.QueryRow(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND tablename = 'deployments'
		  AND indexname = 'deployments_app_scope_live_uniq'
	`).Scan(&indexDef); err != nil {
		t.Fatalf("query deployments_app_scope_live_uniq: %v", err)
	}
	if !strings.Contains(indexDef, "WHERE") ||
		!strings.Contains(indexDef, "status") ||
		!strings.Contains(indexDef, "live") {
		t.Errorf("deployments_app_scope_live_uniq: got indexdef=%q, want partial index WHERE status='live' (load-bearing invariant: at most one live deployment per (app_id, scope))", indexDef)
	}

	// (5) Uniqueness pin: insert two status='live' deployments
	// on the same (app_id, scope). The second MUST fail with
	// SQLSTATE 23505 (unique_violation). Without this pin, a
	// future regression that drops the partial index would
	// silently allow two live deployments on the same scope,
	// reintroducing the non-deterministic wake-target selector
	// that ADR-091 eliminates. app_id is pinned via
	// uuid.NewString() so the two rows truly collide on
	// (app_id, scope); gen_random_uuid() would make both rows
	// differ at app_id and the test would silently pass even
	// if the partial index regressed.
	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO deployments (id, app_id, status, image_digest, scope)
		VALUES (gen_random_uuid(), $1, 'live', 'sha256:test1', 'prod')
	`, appID); err != nil {
		t.Fatalf("first live deployment insert: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO deployments (id, app_id, status, image_digest, scope)
		VALUES (gen_random_uuid(), $1, 'live', 'sha256:test2', 'prod')
	`, appID)
	if err == nil {
		t.Fatal("second live deployment on same (app_id, scope=prod) inserted without error; deployments_app_scope_live_uniq partial index is missing or regressed")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("second live deployment: got non-Postgres error: %v", err)
	}
	if pgErr.Code != "23505" {
		t.Errorf("second live deployment: got SQLSTATE=%s, want 23505 (unique_violation from deployments_app_scope_live_uniq)", pgErr.Code)
	}
	if !strings.Contains(pgErr.ConstraintName, "deployments_app_scope_live_uniq") {
		t.Errorf("second live deployment: got constraint=%q, want deployments_app_scope_live_uniq", pgErr.ConstraintName)
	}

	// (6) Backfill pin: insert WITHOUT scope column → read back →
	// assert scope='default'. The historical column list omits
	// scope so the ONLY way the row gets a scope value is via
	// the NOT NULL DEFAULT clause. A future regression that
	// drops the DEFAULT would fail this assertion and surface
	// here, before any production wake runs against a
	// pre-PR deployment.
	var gotScope string
	if err := pool.QueryRow(ctx, `
		INSERT INTO deployments (id, app_id, status, image_digest)
		VALUES (gen_random_uuid(), gen_random_uuid(), 'pending', 'sha256:backfill')
		RETURNING scope
	`).Scan(&gotScope); err != nil {
		t.Fatalf("backfill insert: %v", err)
	}
	if gotScope != "default" {
		t.Errorf("backfill: got scope=%q, want 'default' (NOT NULL DEFAULT 'default' must materialize on insert)", gotScope)
	}
}
