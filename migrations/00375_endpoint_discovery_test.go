//go:build !no_pg

// Migration-apply test for 00375_endpoint_discovery.sql
// (ADR-122 / issue #975 item #1).
//
// Pins:
//
//  1. Migration set applies cleanly through 00375 (no goose
//     duplicate-version panic). Slot 00375 was picked as the
//     next free slot on origin/main after the rebump chain:
//     00332 fenced by PR #1008 edge_rules_kind_cache, then
//     00347 fenced by PR #1017 alert_presets review-fix
//     (which owns 00372 max), then 00364 fenced by PR #1024
//     ADR-124 deployment queue controls, then 00374 fenced
//     by PR #1006 deployment_audit_backfill_90d. Slot 00375
//     is the next free slot. Re-verify with
//     scripts/ci/check_migration_slots.sh immediately before push.
//  2. The table is present with the 9 expected columns (positive
//     shape — ADR-122 §D1).
//  3. All 4 CHECK constraints landed with the expected names
//     (defense-in-depth — apid write path is the canonical gate,
//     the DB CHECKs are the floor).
//  4. The 2 secondary indexes landed (account_id, app_id).
//  5. byte_size CHECK rejects 0 and 131073 (the SQL floor on
//     the per-doc byte cap).
//  6. source CHECK rejects 'bogus' (closed-vocab contract —
//     guards the cold_boot/manual_upload enum).
//  7. Replay safety: re-running db.MigrateUp is a no-op. The
//     IF NOT EXISTS / DROP TRIGGER IF EXISTS / CREATE OR REPLACE
//     FUNCTION carve-outs are the load-bearing pieces.

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

// endpointDiscoveryExpectedColumns are the 9 columns the migration
// must add. Adding a column to 00375 without updating this list is a
// load-bearing failure mode — downstream consumers (PgStore,
// MemStore, OpenAPI, SDKs) all key off this shape.
var endpointDiscoveryExpectedColumns = []string{
	"deployment_id",
	"account_id",
	"app_id",
	"doc",
	"doc_sha256",
	"byte_size",
	"source",
	"truncated",
	"captured_at",
	"updated_at",
}

// endpointDiscoveryExpectedConstraints is the floor the migration
// must leave behind. The CHECK names are pinned because the apid
// write path relies on a typo here failing fast (and the pgstore
// tests rely on the SQLSTATE on a CHECK violation).
var endpointDiscoveryExpectedConstraints = []string{
	"deployment_openapi_docs_byte_size_chk",
	"deployment_openapi_docs_source_vocab_chk",
	"deployment_openapi_docs_sha256_len_chk",
	"deployment_openapi_docs_captured_before_updated_chk",
}

// endpointDiscoveryExpectedIndexes pins the indexes the apid
// quota gate (count by account_id) and the per-app list page
// (account_id, app_id) read against.
var endpointDiscoveryExpectedIndexes = []string{
	"deployment_openapi_docs_account_id_idx",
	"deployment_openapi_docs_app_id_idx",
}

func TestMigrations_00375_EndpointDiscovery(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00375.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 00374 deployment_audit_backfill_90d and 00375 endpoint_discovery)", err)
	}

	// (2) Positive shape — table present with 9 expected columns.
	for _, col := range endpointDiscoveryExpectedColumns {
		var n int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM information_schema.columns
			 WHERE table_schema = current_schema()
			   AND table_name = 'deployment_openapi_docs'
			   AND column_name = $1`, col).Scan(&n)
		if err != nil {
			t.Fatalf("query column %s: %v", col, err)
		}
		if n == 0 {
			t.Errorf("deployment_openapi_docs.%s missing (regression: column was renamed/dropped from the migration)", col)
		}
	}

	// (3) All CHECK constraints landed.
	for _, c := range endpointDiscoveryExpectedConstraints {
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
			t.Errorf("deployment_openapi_docs constraint %s missing (migration must define all 4 named CHECKs)", c)
		}
	}

	// (4) Indexes landed.
	for _, idx := range endpointDiscoveryExpectedIndexes {
		var n int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM pg_indexes
			 WHERE schemaname = current_schema()
			   AND indexname = $1`, idx).Scan(&n)
		if err != nil {
			t.Fatalf("query index %s: %v", idx, err)
		}
		if n == 0 {
			t.Errorf("deployment_openapi_docs index %s missing (migration must create both secondary indexes)", idx)
		}
	}

	// (5) byte_size CHECK rejects 0 and 131073. The pin is on the
	// SQL floor — the apid layer adds per-plan upper bounds
	// (OpenAPIDocMaxBytes), but the SQL CHECK is the foundation.
	seedDeploymentIDSentinel := func(t *testing.T, byteSize int) (string, error) {
		t.Helper()
		deploymentID := "00000000-0000-0000-0000-0000003758d1"
		accountID := "00000000-0000-0000-0000-0000003758a1"
		appID := "00000000-0000-0000-0000-0000003758b1"
		_, _ = pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1::uuid, 'ediscovery-acct@example.com', 'hobby') ON CONFLICT (id) DO NOTHING`, accountID)
		_, _ = pool.Exec(ctx, `INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s) VALUES ($1::uuid, $2::uuid, 'ediscovery-app', 'app', 256, 2, 60) ON CONFLICT (id) DO NOTHING`, appID, accountID)
		_, err := pool.Exec(ctx, `
			INSERT INTO deployments (id, app_id, image_digest, status)
			VALUES ($1::uuid, $3::uuid, 'sha256:0000000000000000000000000000000000000000000000000000000000000000', 'live')
			ON CONFLICT (id) DO NOTHING`,
			deploymentID, accountID, appID)
		if err != nil {
			return "", err
		}
		return deploymentID, nil
	}
	deploymentID, err := seedDeploymentIDSentinel(t, 0)
	if err != nil {
		t.Fatalf("seed parent rows: %v", err)
	}
	accountID := "00000000-0000-0000-0000-0000003758a1"
	appID := "00000000-0000-0000-0000-0000003758b1"
	docJSON := []byte(`{"openapi":"3.1.0","info":{"title":"sentinel","version":"1.0.0"},"paths":{}}`)
	sha256 := make([]byte, 32)
	for i := range sha256 {
		sha256[i] = byte(i)
	}
	// 5a: byte_size = 0 must fail.
	_, err = pool.Exec(ctx, `
		INSERT INTO deployment_openapi_docs (
			deployment_id, account_id, app_id, doc, doc_sha256, byte_size, source
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::jsonb, $5::bytea, 0, 'manual_upload'
		)`, deploymentID, accountID, appID, docJSON, sha256)
	if err == nil {
		t.Error("byte_size=0 should trip deployment_openapi_docs_byte_size_chk CHECK (regression: the CHECK was widened)")
	} else {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code != "23514" {
			t.Errorf("expected SQLSTATE 23514 check_violation on byte_size=0, got %s", pgErr.Code)
		}
		if !strings.Contains(string(errStringBytes(err)), "deployment_openapi_docs_byte_size_chk") {
			t.Errorf("expected violation of deployment_openapi_docs_byte_size_chk, got %v", err)
		}
	}
	// 5b: byte_size = 131073 (one byte above the cap) must fail.
	_, err = pool.Exec(ctx, `
		INSERT INTO deployment_openapi_docs (
			deployment_id, account_id, app_id, doc, doc_sha256, byte_size, source
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::jsonb, $5::bytea, 131073, 'manual_upload'
		)`, deploymentID, accountID, appID, docJSON, sha256)
	if err == nil {
		t.Error("byte_size=131073 should trip deployment_openapi_docs_byte_size_chk CHECK (regression: the cap was widened)")
	} else {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code != "23514" {
			t.Errorf("expected SQLSTATE 23514 check_violation on byte_size=131073, got %s", pgErr.Code)
		}
	}
	// 5c: byte_size = 131072 (exactly the cap) must SUCCEED. This is
	// the load-bearing case — the wire-format bump allows up to 128 KiB
	// and the SQL CHECK must accommodate the maximum.
	_, err = pool.Exec(ctx, `
		INSERT INTO deployment_openapi_docs (
			deployment_id, account_id, app_id, doc, doc_sha256, byte_size, source
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::jsonb, $5::bytea, 131072, 'manual_upload'
		)`, deploymentID, accountID, appID, docJSON, sha256)
	if err != nil {
		t.Fatalf("byte_size=131072 (max) should succeed: %v (regression: the cap was narrowed)", err)
	}
	// Clean up the sentinel row so the test is idempotent if rerun.
	_, _ = pool.Exec(ctx, `DELETE FROM deployment_openapi_docs WHERE deployment_id = $1::uuid`, deploymentID)
	_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1::uuid`, appID)
	_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1::uuid`, accountID)

	// (6) source CHECK rejects 'bogus'. The closed vocabulary is
	// IN ('cold_boot', 'manual_upload'); a typo at the apid handler
	// gate is rejected by the DB if it slips past.
	// Re-seed parent rows for the bogus-source test.
	bogusDeploymentID := "00000000-0000-0000-0000-0000003758d2"
	_, _ = pool.Exec(ctx, `INSERT INTO deployments (id, app_id, image_digest, status) VALUES ($1::uuid, $3::uuid, 'sha256:0000000000000000000000000000000000000000000000000000000000000000', 'live') ON CONFLICT (id) DO NOTHING`, bogusDeploymentID, accountID, appID)
	_, err = pool.Exec(ctx, `
		INSERT INTO deployment_openapi_docs (
			deployment_id, account_id, app_id, doc, doc_sha256, byte_size, source
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::jsonb, $5::bytea, 1024, 'bogus'
		)`, bogusDeploymentID, accountID, appID, docJSON, sha256)
	if err == nil {
		t.Fatal("expected closed-vocab CHECK to reject source='bogus' (regression: the CHECK was widened to admit non-vocab sources)")
	}
	var vocabErr *pgconn.PgError
	if !errors.As(err, &vocabErr) {
		t.Fatalf("expected pgconn.PgError, got %T: %v", err, err)
	}
	if vocabErr.Code != "23514" {
		t.Errorf("expected SQLSTATE 23514 check_violation, got %s (closed vocabulary contract)", vocabErr.Code)
	}
	if !strings.Contains(vocabErr.ConstraintName, "deployment_openapi_docs_source_vocab_chk") {
		t.Errorf("expected violation of deployment_openapi_docs_source_vocab_chk, got %q", vocabErr.ConstraintName)
	}
	// Clean up sentinel rows so the test is idempotent if rerun.
	_, _ = pool.Exec(ctx, `DELETE FROM deployments WHERE id = $1::uuid`, bogusDeploymentID)
	_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1::uuid`, appID)
	_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1::uuid`, accountID)

	// (7) Replay safety: re-running db.MigrateUp is a no-op.
	// The IF NOT EXISTS / DROP TRIGGER IF EXISTS / CREATE OR REPLACE
	// FUNCTION carve-outs make the up idempotent on an already-applied
	// schema. Without them, the second MigrateUp would 42P07 on the
	// CREATE TABLE.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (migration must be replay-safe — IF NOT EXISTS + DROP TRIGGER IF EXISTS are the load-bearing carve-outs)", err)
	}
}

// errStringBytes is a small helper that returns the string form of an
// error for substring contains; avoids an import cycle on the
// strings pkg for the test file.
func errStringBytes(err error) []byte {
	if err == nil {
		return nil
	}
	return []byte(err.Error())
}
