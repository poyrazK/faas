//go:build !no_pg

// Migration-apply test for 00329_consumer_keys.sql
// (ADR-120 / issue #975 item #5).
//
// Pins:
//
//  1. Migration set applies cleanly through 00329 (no goose
//     duplicate-version panic). Slot 00329 was picked as the next
//     free slot on origin/main after PR #999 (00326), PR #990
//     (00327), and PR #991 (00326-00328). Rebuilt via
//     cherry-pick-rebuild from origin/main after the original
//     branch drifted past 4 rebump cycles (00305 → 00320 → 00321
//     → 00325 → 00329). This PR pushes to 00329 (Strategy A —
//     high end, robust against most merge orders). Re-verify with
//     scripts/ci/check_migration_slots.sh immediately before push.
//  2. The table is present with the 12 expected columns (positive
//     shape — ADR-120 §D1).
//  3. All 6 CHECK constraints landed with the expected names
//     (defense-in-depth — apid write path is the canonical gate,
//     the DB CHECKs are the floor).
//  4. The composite (app_id, prefix) hot-path index landed
//     (gateway-side lookup per ADR-120 §D1).
//  5. The UNIQUE (account_id, app_id, name) index landed
//     (the user-visible identity per ADR-120 §D1).
//  6. Closed-vocab scope rejection: insert with scope='superadmin'
//     must fail with SQLSTATE 23514 (defends the closed-set
//     contract — a typo at the apid handler is rejected by the DB
//     if it slips past the apid validator).
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

// consumerKeyExpectedColumns are the 12 columns the migration must
// add. Adding a column to 00329 without updating this list is a
// load-bearing failure mode — downstream consumers (PgStore,
// MemStore, OpenAPI, SDKs) all key off this shape.
var consumerKeyExpectedColumns = []string{
	"id",
	"account_id",
	"app_id",
	"name",
	"prefix",
	"hashed_secret",
	"scopes",
	"expires_at",
	"last_used_at",
	"revoked_at",
	"created_at",
	"updated_at",
}

// consumerKeyExpectedConstraints is the floor the migration must
// leave behind. The CHECK names are pinned because PR #5-B's apid
// write path relies on a typo here failing fast (and the
// pgstore_test cases rely on the SQLSTATE on a CHECK violation).
var consumerKeyExpectedConstraints = []string{
	"consumer_keys_name_len_chk",
	"consumer_keys_prefix_len_chk",
	"consumer_keys_hashed_secret_len_chk",
	"consumer_keys_scopes_vocab_chk",
	"consumer_keys_expires_after_created_chk",
	"consumer_keys_revoked_state_chk",
}

// consumerKeyExpectedIndexes pins the indexes PR #5-C's gateway
// middleware reads against. (app_id, prefix) is the hot-path
// composite — every inbound request with a `ck_<prefix>_<secret>`
// header narrows via this index before the hash compare.
var consumerKeyExpectedIndexes = []string{
	"consumer_keys_unique_name",
	"consumer_keys_app_prefix_idx",
	"consumer_keys_app_idx",
}

func TestMigrations_00329_ConsumerKeys(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00329.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 00328 PR #991 preview_destroy_commented_at and 00329 consumer_keys)", err)
	}

	// (2) Positive shape — table present with 12 expected columns.
	for _, col := range consumerKeyExpectedColumns {
		var n int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM information_schema.columns
			 WHERE table_schema = current_schema()
			   AND table_name = 'consumer_keys'
			   AND column_name = $1`, col).Scan(&n)
		if err != nil {
			t.Fatalf("query column %s: %v", col, err)
		}
		if n == 0 {
			t.Errorf("consumer_keys.%s missing (regression: column was renamed/dropped from the migration)", col)
		}
	}

	// (3) All CHECK constraints landed.
	for _, c := range consumerKeyExpectedConstraints {
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
			t.Errorf("consumer_keys constraint %s missing (migration must define all 6 named CHECKs)", c)
		}
	}

	// (4) + (5) Indexes landed.
	for _, idx := range consumerKeyExpectedIndexes {
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
			t.Errorf("consumer_keys index %s missing (migration must create all 3 indexes — UNIQUE name + composite (app_id, prefix) + app_id)", idx)
		}
	}

	// (4b) The (app_id, prefix) index MUST be UNIQUE — the gateway's
	// hot-path lookup (ConsumerKeyByAppAndPrefix) expects exactly one
	// row per (app_id, prefix). A non-UNIQUE index would let a
	// birthday-bound prefix collision silently collapse the lookup
	// to "first row found by the planner" and the constant-time hash
	// compare would run against the wrong key's bytes — a spurious
	// 401 with no log signal. Code-review finding #1.
	var isUnique bool
	err := pool.QueryRow(ctx, `
		SELECT indisunique
		  FROM pg_index i
		  JOIN pg_class c ON c.oid = i.indexrelid
		 WHERE c.relname = 'consumer_keys_app_prefix_idx'`).Scan(&isUnique)
	if err != nil {
		t.Fatalf("query indisunique: %v", err)
	}
	if !isUnique {
		t.Error("consumer_keys_app_prefix_idx is NOT UNIQUE — the gateway's hot-path lookup will silently pick the wrong row on a prefix collision (code-review finding #1)")
	}
	// Belt-and-braces: try inserting two rows with the same (app_id, prefix)
	// and assert the second trips 23505. Skipped if the parent FK floor
	// isn't satisfiable (the foreach test seed sets up the rows).
	var dupAccountID = "00000000-0000-0000-0000-0000003308aa"
	var dupAppID = "00000000-0000-0000-0000-0000003308bb"
	insertDup := func(prefix string) error {
		_, err := pool.Exec(ctx, `
			INSERT INTO consumer_keys (account_id, app_id, name, prefix, hashed_secret, scopes)
			VALUES ($1::uuid, $2::uuid, 'dup-unique-test-' || $3, $3,
			        decode('0000000000000000000000000000000000000000000000000000000000000000', 'hex'),
			        ARRAY['read']::text[])`, dupAccountID, dupAppID, prefix)
		return err
	}
	// Insert parent rows for the dup test (the parent's accounts/apps
	// FK floors must be satisfied — the migration test doesn't seed via
	// Store, so we INSERT directly).
	_, _ = pool.Exec(ctx, `INSERT INTO accounts (id, email, plan) VALUES ($1::uuid, 'dup-unique-acct@example.com', 'hobby') ON CONFLICT (id) DO NOTHING`, dupAccountID)
	_, _ = pool.Exec(ctx, `INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s) VALUES ($1::uuid, $2::uuid, 'dup-unique-app', 'app', 256, 2, 60) ON CONFLICT (id) DO NOTHING`, dupAppID, dupAccountID)
	if err := insertDup("dup1234"); err != nil {
		t.Fatalf("first INSERT (sentinel) failed: %v (need real parent rows for the FK floor)", err)
	}
	err = insertDup("dup1234")
	if err == nil {
		t.Fatal("duplicate (app_id, prefix) INSERT did NOT trip the UNIQUE index — additive collision, gateway would silently pick wrong row")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected pgconn.PgError on duplicate, got %T: %v", err, err)
	}
	if pgErr.Code != "23505" {
		t.Errorf("expected SQLSTATE 23505 unique_violation on duplicate (app_id, prefix), got %s", pgErr.Code)
	}
	// Clean up the sentinel row so the test is idempotent if rerun.
	_, _ = pool.Exec(ctx, `DELETE FROM consumer_keys WHERE account_id = $1::uuid`, dupAccountID)

	// (6) Closed-vocab scope rejection. INSERT with scope='superadmin'
	// must fail with SQLSTATE 23514 (check_violation). The CHECK
	// consumer_keys_scopes_vocab_chk is the floor — apid handlers
	// validate at the write boundary, but a hand-rolled INSERT must
	// also be rejected.
	var accountID = "00000000-0000-0000-0000-0000003308aa"
	var appID = "00000000-0000-0000-0000-0000003308bb"
	dummyErr := error(nil)
	if _, err := pool.Exec(ctx, `
		INSERT INTO consumer_keys (account_id, app_id, name, prefix, hashed_secret, scopes)
		VALUES (
		  $1::uuid,
		  $2::uuid,
		  'bad-scope-test',
		  'deadbeef',
		  decode('0000000000000000000000000000000000000000000000000000000000000000', 'hex'),
		  ARRAY['superadmin']::text[]
		)`, accountID, appID); err != nil {
		dummyErr = err
	}
	if dummyErr == nil {
		t.Fatal("expected closed-vocab CHECK to reject scope='superadmin' (regression: the CHECK was widened to admit non-vocab scopes)")
	}
	var vocabErr *pgconn.PgError
	if !errors.As(dummyErr, &vocabErr) {
		t.Fatalf("expected pgconn.PgError, got %T: %v", dummyErr, dummyErr)
	}
	if vocabErr.Code != "23514" {
		t.Errorf("expected SQLSTATE 23514 check_violation, got %s (closed vocabulary contract)", vocabErr.Code)
	}
	if !strings.Contains(vocabErr.ConstraintName, "consumer_keys_scopes_vocab_chk") {
		t.Errorf("expected violation of consumer_keys_scopes_vocab_chk, got %q", vocabErr.ConstraintName)
	}

	// (7) Replay safety: re-running db.MigrateUp is a no-op.
	// The IF NOT EXISTS / DROP TRIGGER IF EXISTS / CREATE OR REPLACE
	// FUNCTION carve-outs make the up idempotent on an already-applied
	// schema. Without them, the second MigrateUp would 42P07 on the
	// CREATE TABLE.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (migration must be replay-safe — IF NOT EXISTS + DROP TRIGGER IF EXISTS are the load-bearing carve-outs)", err)
	}
}
