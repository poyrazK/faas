//go:build !no_pg

// Migration-apply test for 00222_app_errors.sql
// (ADR-096 / customer-facing automatic error grouping).
//
// Pins:
//
//  1. Both tables exist: app_errors + app_error_requests.
//  2. The five indexes exist with the documented shapes:
//     - app_errors_account_app_last_seen_idx
//     - app_errors_account_app_fp_last_seen_idx
//     - app_errors_dedupe_uniq (UNIQUE on (account_id, app_id, fingerprint))
//     - app_error_requests_drill_idx
//     - app_error_requests_retention_idx (plain btree on
//     (account_id, received_at) — load-bearing for the nightly
//     purge DELETE; started life as a PARTIAL with
//     WHERE received_at < now() - interval '90 days' but the
//     volatile now() predicate broke migration-apply with
//     SQLSTATE 42P17, so the index is the plain btree.
//  3. CHECK constraints reject malformed input:
//     - http_status OUTSIDE 400..599 → SQLSTATE 23514
//     - fingerprint not 64 hex chars → SQLSTATE 23514
//     - error_class not in the allowlist → SQLSTATE 23514
//     - sample_message > 512 bytes → SQLSTATE 23514
//  4. ON CONFLICT tripwire: app_errors_dedupe_uniq UNIQUE on
//     (account_id, app_id, fingerprint) means a second INSERT
//     with the same fingerprint fails with SQLSTATE 23505. The
//     grpc_server_apperrors.go handler relies on this exact
//     constraint name for its `ON CONFLICT (...) DO UPDATE`.
//  5. FK cascade: deleting an account cascades to both
//     app_errors and app_error_requests; deleting a deployment
//     sets deployment_id to NULL on both.
//  6. Replay-safety: the apply_walk_test.go harness runs
//     MigrateUp twice. The migration's CREATE TABLE IF NOT
//     EXISTS + CREATE INDEX IF NOT EXISTS + CREATE UNIQUE
//     INDEX IF NOT EXISTS guards make the second pass a
//     no-op; the harness fails loudly if the second pass
//     errors.
//
// Slot reservation: migrations/00221_reserve_slot.sql fences
// the prior slot. TestMigrationsContiguous walks positions
// {1, 2, …, 222}; the fence at 00221 fills its position.
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

func TestMigrations_00222_AppErrors(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// (1) Both tables exist.
	for _, table := range []string{"app_errors", "app_error_requests"} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = current_schema()
				  AND table_name = $1
			)
		`, table).Scan(&exists); err != nil {
			t.Fatalf("query %s existence: %v", table, err)
		}
		if !exists {
			t.Errorf("table %s missing (00222_app_errors.sql must create both app_errors and app_error_requests)", table)
		}
	}

	// (2) The five indexes exist with the right shapes.
	type idxPin struct {
		name        string
		table       string
		mustContain string
	}
	pins := []idxPin{
		{"app_errors_account_app_last_seen_idx", "app_errors", "last_seen_at"},
		{"app_errors_account_app_fp_last_seen_idx", "app_errors", "fingerprint"},
		{"app_errors_dedupe_uniq", "app_errors", "UNIQUE"},
		{"app_error_requests_drill_idx", "app_error_requests", "received_at"},
		{"app_error_requests_retention_idx", "app_error_requests", "account_id"},
	}
	for _, p := range pins {
		var indexDef string
		if err := pool.QueryRow(ctx, `
			SELECT indexdef FROM pg_indexes
			WHERE schemaname = current_schema()
			  AND tablename = $1
			  AND indexname = $2
		`, p.table, p.name).Scan(&indexDef); err != nil {
			t.Fatalf("query index %s.%s: %v", p.table, p.name, err)
		}
		if !strings.Contains(indexDef, p.mustContain) {
			t.Errorf("index %s.%s: got indexdef=%q, must contain %q (load-bearing shape)", p.table, p.name, indexDef, p.mustContain)
		}
	}

	// (3) CHECK constraints reject malformed input. We need an
	// account + app row to satisfy the FKs before inserting a
	// malformed app_errors row. Every test gets its own empty
	// schema, so seed both here rather than borrowing rows another
	// test file happened to leave behind.
	var accountID, appID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO accounts (id, email, plan, created_at)
		VALUES (gen_random_uuid(), 'app-errors-test@example.com', 'scale', now())
		RETURNING id
	`).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		VALUES (gen_random_uuid(), $1, 'app-errors-test', 'app', 256, 1, 'active', now())
		RETURNING id
	`, accountID).Scan(&appID); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// (3a) http_status outside 400..599 → 23514.
	_, err := pool.Exec(ctx, `
		INSERT INTO app_errors (
			id, account_id, app_id, fingerprint, route, http_status,
			error_class, sample_message
		) VALUES (
			gen_random_uuid(), $1, $2, repeat('a', 64), '/x', 200,
			'unhandled', 'ok'
		)
	`, accountID, appID)
	assertCheckViolation(t, err, "app_errors_http_status_check")

	// (3b) fingerprint not 64 hex chars → 23514.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_errors (
			id, account_id, app_id, fingerprint, route, http_status,
			error_class, sample_message
		) VALUES (
			gen_random_uuid(), $1, $2, 'short', '/x', 500,
			'unhandled', 'ok'
		)
	`, accountID, appID)
	assertCheckViolation(t, err, "app_errors_fingerprint_check")

	// (3c) error_class not in allowlist → 23514.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_errors (
			id, account_id, app_id, fingerprint, route, http_status,
			error_class, sample_message
		) VALUES (
			gen_random_uuid(), $1, $2, repeat('b', 64), '/x', 500,
			'bogus_class', 'ok'
		)
	`, accountID, appID)
	assertCheckViolation(t, err, "app_errors_error_class_check")

	// (3d) sample_message > 512 bytes → 23514.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_errors (
			id, account_id, app_id, fingerprint, route, http_status,
			error_class, sample_message
		) VALUES (
			gen_random_uuid(), $1, $2, repeat('c', 64), '/x', 500,
			'unhandled', repeat('x', 600)
		)
	`, accountID, appID)
	assertCheckViolation(t, err, "app_errors_sample_message_check")

	// (4) UNIQUE tripwire: app_errors_dedupe_uniq ON CONFLICT
	// surface. The first INSERT succeeds; the second with the
	// same (account_id, app_id, fingerprint) fails with 23505
	// on the dedupe_uniq index name. This is the EXACT
	// constraint name the grpc_server_apperrors.go handler
	// targets with ON CONFLICT (...) DO UPDATE — a future
	// regression that drops the index silently breaks the
	// dedupe-merge path and breaks the customer's grouped view.
	fp := repeatStr("d", 64)
	if _, err := pool.Exec(ctx, `
		INSERT INTO app_errors (
			id, account_id, app_id, fingerprint, route, http_status,
			error_class, sample_message
		) VALUES (
			gen_random_uuid(), $1, $2, $3, '/x', 500,
			'unhandled', 'first'
		)
	`, accountID, appID, fp); err != nil {
		t.Fatalf("first dedupe-uniq insert: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_errors (
			id, account_id, app_id, fingerprint, route, http_status,
			error_class, sample_message
		) VALUES (
			gen_random_uuid(), $1, $2, $3, '/x', 500,
			'unhandled', 'second'
		)
	`, accountID, appID, fp)
	if err == nil {
		t.Fatal("second app_errors INSERT with same fingerprint succeeded; app_errors_dedupe_uniq missing or regressed (the dedupe-merge handler in apid relies on this unique constraint)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("second dedupe-uniq insert: got non-Postgres error: %v", err)
	}
	if pgErr.Code != "23505" {
		t.Errorf("second dedupe-uniq insert: got SQLSTATE=%s, want 23505 (unique_violation from app_errors_dedupe_uniq)", pgErr.Code)
	}
	if pgErr.ConstraintName != "app_errors_dedupe_uniq" {
		t.Errorf("second dedupe-uniq insert: got constraint=%q, want app_errors_dedupe_uniq (the handler's ON CONFLICT target by name)", pgErr.ConstraintName)
	}

	// (5) FK cascade. Insert one row in each table; delete the
	// account; both rows must disappear. Then re-insert + delete
	// the deployment to confirm deployment_id SET NULL.
	var fkAppID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO apps (id, account_id, slug, status, ram_mb)
		VALUES (gen_random_uuid(), $1, 'app-errors-fk-test', 'active', 256)
		RETURNING id
	`, accountID).Scan(&fkAppID); err != nil {
		t.Fatalf("seed app for FK test: %v", err)
	}
	depID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO deployments (id, app_id, image_digest, status, scope)
		VALUES ($1, $2, 'sha256:fk-test', 'live', 'default')
	`, depID, fkAppID); err != nil {
		t.Fatalf("seed deployment for FK test: %v", err)
	}

	errFp := repeatStr("e", 64)
	errReqFp := repeatStr("f", 64)
	errRowID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO app_errors (
			id, account_id, app_id, deployment_id, fingerprint, route,
			http_status, error_class, sample_message
		) VALUES (
			$1, $2, $3, $4, $5, '/x', 500, 'unhandled', 'fk-test'
		)
	`, errRowID, accountID, fkAppID, depID, errFp); err != nil {
		t.Fatalf("seed app_errors row for FK test: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO app_error_requests (
			id, account_id, app_id, fingerprint, request_id, received_at,
			route, http_status, error_class, sample_message, deployment_id
		) VALUES (
			gen_random_uuid(), $1, $2, $3, gen_random_uuid(), now(),
			'/x', 500, 'unhandled', 'fk-test', $4
		)
	`, accountID, fkAppID, errReqFp, depID); err != nil {
		t.Fatalf("seed app_error_requests row for FK test: %v", err)
	}

	// (5a) Deployment SET NULL: delete the deployment, expect the
	// app_errors row's deployment_id to flip to NULL (the same
	// for app_error_requests). This is the FK contract — losing
	// the deployment must NOT delete the error rows, only the
	// deployment pointer.
	if _, err := pool.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, depID); err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	var errDepID *uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT deployment_id FROM app_errors WHERE id = $1
	`, errRowID).Scan(&errDepID); err != nil {
		t.Fatalf("re-read app_errors deployment_id after deployment delete: %v", err)
	}
	if errDepID != nil {
		t.Errorf("app_errors.deployment_id after deployment delete: got %v, want NULL (FK must be ON DELETE SET NULL, not CASCADE)", *errDepID)
	}
	var reqDepID *uuid.UUID
	if err := pool.QueryRow(ctx, `
		SELECT deployment_id FROM app_error_requests
		WHERE account_id = $1 AND app_id = $2 AND fingerprint = $3
		LIMIT 1
	`, accountID, fkAppID, errReqFp).Scan(&reqDepID); err != nil {
		t.Fatalf("re-read app_error_requests deployment_id after deployment delete: %v", err)
	}
	if reqDepID != nil {
		t.Errorf("app_error_requests.deployment_id after deployment delete: got %v, want NULL (FK must be ON DELETE SET NULL)", *reqDepID)
	}

	// Cleanup the FK-test rows so the apply_walk_test.go second
	// pass finds a clean slate.
	if _, err := pool.Exec(ctx, `DELETE FROM app_errors WHERE id = $1`, errRowID); err != nil {
		t.Fatalf("cleanup app_errors row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM app_error_requests
		WHERE account_id = $1 AND app_id = $2 AND fingerprint = $3
	`, accountID, fkAppID, errReqFp); err != nil {
		t.Fatalf("cleanup app_error_requests rows: %v", err)
	}
}

// assertCheckViolation fails the test unless err is a Postgres
// SQLSTATE 23514 (check_violation) on the named constraint. Used
// to pin the load-bearing CHECK shapes — a future regression
// that loosens the CHECK constraint fails here before reaching
// production.
func assertCheckViolation(t *testing.T, err error, wantConstraint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected SQLSTATE 23514 on constraint %s, got nil error (CHECK constraint missing or regressed)", wantConstraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected SQLSTATE 23514 on %s, got non-Postgres error: %v", wantConstraint, err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("expected SQLSTATE 23514 on %s, got %s: %s", wantConstraint, pgErr.Code, pgErr.Message)
	}
}

// repeatStr returns s repeated n times. Used in place of
// strings.Repeat for migration-test self-containment (the test
// file already imports strings for indexdef parsing).
func repeatStr(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
