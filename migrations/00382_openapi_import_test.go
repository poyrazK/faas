//go:build !no_pg

// Migration-apply test for 00378_openapi_import.sql
// (ADR-126 / issue #975 item #2).
//
// Pins:
//
//  1. Migration set applies cleanly through 00378 (no goose
//     duplicate-version panic). Slot 00378 was picked as the
//     next free slot on origin/main after PR #1041's
//     compute_nodes_active_unique (00376) +
//     instances_wake_attempt_active_unique (00377). Re-verify
//     with scripts/ci/check_migration_slots.sh immediately
//     before push.
//  2. The table is present with the 10 expected columns
//     (positive shape — ADR-126 §D1).
//  3. All 6 CHECK constraints landed with the expected names
//     (defense-in-depth — apid write path is the canonical
//     gate, the DB CHECKs are the floor).
//  4. The 1 secondary index landed (account_id).
//  5. byte_size CHECK rejects 0 and 262145 (the SQL floor on
//     the per-doc byte cap).
//  6. endpoint_count CHECK rejects 51 (the SQL floor on the
//     per-doc operation cap).
//  7. source CHECK rejects 'cold_boot' (closed-vocab contract
//     — cold-boot captures belong in deployment_openapi_docs
//     from item #1, not here).
//  8. openapi_version CHECK rejects '3.2.0' (closed-vocab
//     contract on the accepted meta-schema versions).
//  9. Replay safety: re-running db.MigrateUp is a no-op. The
//     IF NOT EXISTS / DROP TRIGGER IF EXISTS / CREATE OR
//     REPLACE FUNCTION carve-outs are the load-bearing pieces.

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// openapiImportExpectedColumns are the 10 columns the migration
// must add. Adding a column to 00378 without updating this list
// is a load-bearing failure mode — downstream consumers
// (PgStore, MemStore, OpenAPI, SDKs) all key off this shape.
var openapiImportExpectedColumns = []string{
	"app_id",
	"account_id",
	"doc",
	"doc_sha256",
	"byte_size",
	"endpoint_count",
	"source",
	"openapi_version",
	"captured_at",
	"updated_at",
}

// openapiImportExpectedConstraints is the floor the migration
// must leave behind. The CHECK names are pinned because the
// apid write path relies on a typo here failing fast (and the
// pgstore tests rely on the SQLSTATE on a CHECK violation).
var openapiImportExpectedConstraints = []string{
	"app_openapi_docs_byte_size_chk",
	"app_openapi_docs_endpoint_count_chk",
	"app_openapi_docs_source_vocab_chk",
	"app_openapi_docs_openapi_version_vocab_chk",
	"app_openapi_docs_sha256_len_chk",
	"app_openapi_docs_captured_before_updated_chk",
}

// openapiImportExpectedIndexes pins the index the apid quota
// gate (count by account_id) reads against.
var openapiImportExpectedIndexes = []string{
	"app_openapi_docs_account_id_idx",
}

func TestMigrations_00378_OpenAPIImport(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00378.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 00377 instances_wake_attempt_active_unique and 00378 openapi_import)", err)
	}

	// (2) Positive shape — table present with 10 expected columns.
	for _, col := range openapiImportExpectedColumns {
		var n int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM information_schema.columns
			 WHERE table_schema = 'public'
			   AND table_name = 'app_openapi_docs'
			   AND column_name = $1`, col).Scan(&n)
		if err != nil {
			t.Fatalf("query column %s: %v", col, err)
		}
		if n == 0 {
			t.Errorf("app_openapi_docs.%s missing (regression: column was renamed/dropped from the migration)", col)
		}
	}

	// (3) All CHECK constraints landed.
	for _, c := range openapiImportExpectedConstraints {
		var n int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM pg_constraint c
			  JOIN pg_namespace n ON n.oid = c.connamespace
			 WHERE c.conname = $1
			   AND n.nspname = current_schema()`, c).Scan(&n)
		if err != nil {
			t.Fatalf("query constraint %s: %v", c, err)
		}
		if n == 0 {
			t.Errorf("app_openapi_docs constraint %s missing (migration must define all 6 named CHECKs)", c)
		}
	}

	// (4) Indexes landed.
	for _, idx := range openapiImportExpectedIndexes {
		var n int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM pg_indexes
			 WHERE schemaname = 'public'
			   AND indexname = $1`, idx).Scan(&n)
		if err != nil {
			t.Fatalf("query index %s: %v", idx, err)
		}
		if n == 0 {
			t.Errorf("app_openapi_docs index %s missing (migration must create the account_id secondary index)", idx)
		}
	}

	// Seed parent rows. The migration only checks column/CHECK
	// presence; the parent-row seeding is just enough to insert
	// candidate rows for the byte_size / endpoint_count / source /
	// version floor tests.
	accountID := "00000000-0000-0000-0000-000000378a01"
	appID := "00000000-0000-0000-0000-000000378a02"
	_, _ = pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1::uuid, 'oimport-acct@example.com', 'hobby') ON CONFLICT (id) DO NOTHING`, accountID)
	_, _ = pool.Exec(ctx, `INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s) VALUES ($1::uuid, $2::uuid, 'oimport-app', 'app', 256, 2, 60) ON CONFLICT (id) DO NOTHING`, appID, accountID)

	docJSON := []byte(`{"openapi":"3.1.0","info":{"title":"sentinel","version":"1.0.0"},"paths":{}}`)
	sha256 := make([]byte, 32)
	for i := range sha256 {
		sha256[i] = byte(i)
	}

	// (5a) byte_size = 0 must fail.
	_, err := pool.Exec(ctx, `
		INSERT INTO app_openapi_docs (
			app_id, account_id, doc, doc_sha256, byte_size, endpoint_count, source, openapi_version
		) VALUES (
			$1::uuid, $2::uuid, $3::jsonb, $4::bytea, 0, 0, 'manual_import', '3.1.0'
		)`, appID, accountID, docJSON, sha256)
	if err == nil {
		t.Error("byte_size=0 should trip app_openapi_docs_byte_size_chk CHECK (regression: the CHECK was widened)")
	} else {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code != "23514" {
			t.Errorf("expected SQLSTATE 23514 check_violation on byte_size=0, got %s", pgErr.Code)
		}
		if !strings.Contains(string(errStringBytes(err)), "app_openapi_docs_byte_size_chk") {
			t.Errorf("expected violation of app_openapi_docs_byte_size_chk, got %v", err)
		}
	}

	// (5b) byte_size = 262145 (one byte above the cap) must fail.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_openapi_docs (
			app_id, account_id, doc, doc_sha256, byte_size, endpoint_count, source, openapi_version
		) VALUES (
			$1::uuid, $2::uuid, $3::jsonb, $4::bytea, 262145, 0, 'manual_import', '3.1.0'
		)`, appID, accountID, docJSON, sha256)
	if err == nil {
		t.Error("byte_size=262145 should trip app_openapi_docs_byte_size_chk CHECK (regression: the cap was widened)")
	} else {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code != "23514" {
			t.Errorf("expected SQLSTATE 23514 check_violation on byte_size=262145, got %s", pgErr.Code)
		}
	}

	// (5c) byte_size = 262144 (exactly the cap) must SUCCEED. This
	// is the load-bearing case — the abuse-surface cap is 256 KiB
	// and the SQL CHECK must accommodate the maximum.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_openapi_docs (
			app_id, account_id, doc, doc_sha256, byte_size, endpoint_count, source, openapi_version
		) VALUES (
			$1::uuid, $2::uuid, $3::jsonb, $4::bytea, 262144, 0, 'manual_import', '3.1.0'
		)`, appID, accountID, docJSON, sha256)
	if err != nil {
		t.Fatalf("byte_size=262144 (max) should succeed: %v (regression: the cap was narrowed)", err)
	}

	// (6) endpoint_count = 51 (one above the cap) must fail.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_openapi_docs (
			app_id, account_id, doc, doc_sha256, byte_size, endpoint_count, source, openapi_version
		) VALUES (
			$1::uuid, $2::uuid, $3::jsonb, $4::bytea, 1024, 51, 'manual_import', '3.1.0'
		)`, appID, accountID, docJSON, sha256)
	if err == nil {
		t.Error("endpoint_count=51 should trip app_openapi_docs_endpoint_count_chk CHECK (regression: the cap was widened)")
	} else {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code != "23514" {
			t.Errorf("expected SQLSTATE 23514 check_violation on endpoint_count=51, got %s", pgErr.Code)
		}
		if !strings.Contains(string(errStringBytes(err)), "app_openapi_docs_endpoint_count_chk") {
			t.Errorf("expected violation of app_openapi_docs_endpoint_count_chk, got %v", err)
		}
	}

	// Clean up the sentinel row so the test is idempotent if rerun.
	_, _ = pool.Exec(ctx, `DELETE FROM app_openapi_docs WHERE app_id = $1::uuid`, appID)

	// (7) source CHECK rejects 'cold_boot'. The closed vocabulary
	// is IN ('manual_import') — cold-boot captures belong in
	// deployment_openapi_docs from item #1, not here.
	_, err = pool.Exec(ctx, `
		INSERT INTO app_openapi_docs (
			app_id, account_id, doc, doc_sha256, byte_size, endpoint_count, source, openapi_version
		) VALUES (
			$1::uuid, $2::uuid, $3::jsonb, $4::bytea, 1024, 0, 'cold_boot', '3.1.0'
		)`, appID, accountID, docJSON, sha256)
	if err == nil {
		t.Fatal("expected closed-vocab CHECK to reject source='cold_boot' (regression: cold-boot captures must go to deployment_openapi_docs, not app_openapi_docs)")
	}
	var vocabErr *pgconn.PgError
	if !errors.As(err, &vocabErr) {
		t.Fatalf("expected pgconn.PgError, got %T: %v", err, err)
	}
	if vocabErr.Code != "23514" {
		t.Errorf("expected SQLSTATE 23514 check_violation, got %s (closed vocabulary contract)", vocabErr.Code)
	}
	if !strings.Contains(vocabErr.ConstraintName, "app_openapi_docs_source_vocab_chk") {
		t.Errorf("expected violation of app_openapi_docs_source_vocab_chk, got %q", vocabErr.ConstraintName)
	}

	// (8) openapi_version CHECK rejects '3.2.0'. The closed
	// vocabulary is the seven accepted meta-schema versions
	// (3.0.0-3.0.4 and 3.1.0-3.1.1).
	_, err = pool.Exec(ctx, `
		INSERT INTO app_openapi_docs (
			app_id, account_id, doc, doc_sha256, byte_size, endpoint_count, source, openapi_version
		) VALUES (
			$1::uuid, $2::uuid, $3::jsonb, $4::bytea, 1024, 0, 'manual_import', '3.2.0'
		)`, appID, accountID, docJSON, sha256)
	if err == nil {
		t.Fatal("expected closed-vocab CHECK to reject openapi_version='3.2.0' (regression: the CHECK was widened)")
	}
	var verErr *pgconn.PgError
	if !errors.As(err, &verErr) {
		t.Fatalf("expected pgconn.PgError, got %T: %v", err, err)
	}
	if verErr.Code != "23514" {
		t.Errorf("expected SQLSTATE 23514 check_violation, got %s (openapi_version closed vocabulary contract)", verErr.Code)
	}
	if !strings.Contains(verErr.ConstraintName, "app_openapi_docs_openapi_version_vocab_chk") {
		t.Errorf("expected violation of app_openapi_docs_openapi_version_vocab_chk, got %q", verErr.ConstraintName)
	}

	// Clean up sentinel rows so the test is idempotent if rerun.
	_, _ = pool.Exec(ctx, `DELETE FROM app_openapi_docs WHERE app_id = $1::uuid`, appID)
	_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1::uuid`, appID)
	_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1::uuid`, accountID)

	// (9) Replay safety: re-running db.MigrateUp is a no-op. The
	// IF NOT EXISTS / DROP TRIGGER IF EXISTS / CREATE OR REPLACE
	// FUNCTION carve-outs make the up idempotent on an
	// already-applied schema. Without them, the second MigrateUp
	// would 42P07 on the CREATE TABLE.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (migration must be replay-safe — IF NOT EXISTS + DROP TRIGGER IF EXISTS are the load-bearing carve-outs)", err)
	}
}
