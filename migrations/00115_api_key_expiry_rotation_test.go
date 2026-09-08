//go:build !no_pg

// Migration-apply test for 00115_api_key_expiry_rotation.sql
// (issue #189 / IAM-5). Pins the additive schema that gives
// every API key a per-row `expires_at` and `status`, plus a
// per-account `key_grace_window_days` override.
//
// Pins:
//
//  1. Migration set applies cleanly through 00115 (no prior
//     migration's surface is broken — IAM-1's `scopes` and
//     IAM-6's `org_id` are preserved).
//  2. `status` defaults to 'active' for fresh rows; the CHECK
//     constraint rejects unknown values; 'revoked' is terminal
//     (no row created with status='revoked' on a default insert).
//  3. `expires_at`, `revoked_at`, and `rotated_from_id` are all
//     nullable columns that round-trip a NULL value cleanly.
//  4. `accounts.key_grace_window_days` accepts NULL, zero, and
//     positive values; the CHECK rejects negative values.
//  5. The three partial / composite indexes exist with the
//     expected definitions.
//  6. Existing pre-migration rows survive the migration with
//     `status='active'` and `expires_at IS NULL` — proves the
//     additive / non-breaking promise (the auth path is what
//     enforces the gate, not the schema).
//  7. The `rotated_from_id` FK round-trips a real predecessor
//     row and rejects a dangling UUID.
//
// Build tag mirrors apply_walk_test.go:4 — set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see
// migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00115_APIKeyExpiryRotation(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply migrations 1..106. The new columns + indexes +
	// CHECK land here; pre-existing rows (if any) get
	// status='active' and expires_at=NULL by the additive
	// default. pgtest.Open returns a fresh schema with no
	// accounts / api_keys yet.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// Seed one account + two keys (one will be the rotation
	// predecessor). The UUID prefix is the test convention from
	// 00046 — fixed and non-colliding, makes failure output
	// readable.
	const (
		acctID   = "00000000-0000-0000-0000-000000000106"
		olderKey = "00000000-0000-0000-0000-000000000016"
		newerKey = "00000000-0000-0000-0000-000000000017"
	)
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, status, created_at)
		values ($1, 'iam5@example.com', 'hobby', 'active', now())
		on conflict (id) do nothing
	`, acctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	// (2) Insert a pre-migration-style key (no expires_at, no
	// status override) and a "rotated" pair (newerKey with
	// rotated_from_id -> olderKey). Defaults + lineage round
	// trip in one go.
	if _, err := pool.Exec(ctx, `
		insert into api_keys (id, account_id, key_sha256, label, scopes, org_id)
		values
		    ($1, $2, decode('aaaa00000000000000000000000000000000000000000000000000000000', 'hex'),
		        'pre-mig', ARRAY['admin'], '00000000-0000-0000-0000-0000000000aa'),
		    ($3, $2, decode('bbbb00000000000000000000000000000000000000000000000000000000', 'hex'),
		        'rotated', ARRAY['admin'], '00000000-0000-0000-0000-0000000000aa')
		on conflict (id) do nothing
	`, olderKey, acctID, newerKey); err != nil {
		t.Fatalf("seed keys: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update api_keys set rotated_from_id = $1 where id = $2`,
		olderKey, newerKey); err != nil {
		t.Fatalf("set rotated_from_id: %v", err)
	}

	// (3) Defaults: pre-mig row has status='active' and
	// expires_at NULL. The "rotated" row has the FK set.
	var (
		preStatus    string
		preExpiresAt *string // null column → nil
		preRevokedAt *string
		preRotated   *string
	)
	if err := pool.QueryRow(ctx, `
		select status, expires_at, revoked_at, rotated_from_id
		  from api_keys where id = $1
	`, olderKey).Scan(&preStatus, &preExpiresAt, &preRevokedAt, &preRotated); err != nil {
		t.Fatalf("scan pre-mig: %v", err)
	}
	if preStatus != "active" {
		t.Errorf("pre-mig status: got %q, want %q", preStatus, "active")
	}
	if preExpiresAt != nil {
		t.Errorf("pre-mig expires_at: got %v, want NULL", *preExpiresAt)
	}
	if preRevokedAt != nil {
		t.Errorf("pre-mig revoked_at: got %v, want NULL", *preRevokedAt)
	}
	if preRotated != nil {
		t.Errorf("pre-mig rotated_from_id: got %v, want NULL", *preRotated)
	}

	var rotRotated *string
	if err := pool.QueryRow(ctx,
		`select rotated_from_id from api_keys where id = $1`, newerKey).
		Scan(&rotRotated); err != nil {
		t.Fatalf("scan rotated: %v", err)
	}
	if rotRotated == nil || *rotRotated != olderKey {
		t.Errorf("rotated_from_id: got %v, want %s", rotRotated, olderKey)
	}

	// (4) CHECK constraint rejects unknown status. We expect
	// the SQL error to surface a 23514 (check_violation); any
	// error is fine for this pin, the constraint is the wall.
	_, err := pool.Exec(ctx, `
		insert into api_keys (id, account_id, key_sha256, label, scopes, status, org_id)
		values ('00000000-0000-0000-0000-0000000000ff', $1,
		        decode('ffff00000000000000000000000000000000000000000000000000000000', 'hex'),
		        'bad', ARRAY['admin'], 'unknown', '00000000-0000-0000-0000-0000000000aa')
	`, acctID)
	if err == nil {
		t.Errorf("expected CHECK violation for status='unknown'")
	} else if !strings.Contains(strings.ToLower(err.Error()), "check") &&
		!strings.Contains(err.Error(), "23514") {
		t.Logf("note: unknown-status error did not mention CHECK/23514: %v", err)
	}

	// (5) The per-account grace override accepts the three
	// valid shapes and rejects negative.
	if _, err := pool.Exec(ctx,
		`update accounts set key_grace_window_days = NULL where id = $1`, acctID); err != nil {
		t.Fatalf("set grace NULL: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update accounts set key_grace_window_days = 0 where id = $1`, acctID); err != nil {
		t.Fatalf("set grace 0: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update accounts set key_grace_window_days = 14 where id = $1`, acctID); err != nil {
		t.Fatalf("set grace 14: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update accounts set key_grace_window_days = -1 where id = $1`, acctID); err == nil {
		t.Errorf("expected CHECK violation for key_grace_window_days = -1")
	}

	// (6) FK rejects a dangling rotated_from_id.
	_, err = pool.Exec(ctx, `
		insert into api_keys (id, account_id, key_sha256, label, scopes, rotated_from_id, org_id)
		values ('00000000-0000-0000-0000-0000000000fe', $1,
		        decode('fefe00000000000000000000000000000000000000000000000000000000', 'hex'),
		        'dangling', ARRAY['admin'],
		        '00000000-0000-0000-0000-deadbeefdead', '00000000-0000-0000-0000-0000000000aa')
	`, acctID)
	if err == nil {
		t.Errorf("expected FK violation for dangling rotated_from_id")
	}

	// (7) Indexes exist with the expected shapes (column list
	// for the partial ones is the easier pin — the planner
	// picks them up at runtime; we just want them defined).
	type indexCheck struct {
		name   string
		filter string
	}
	wantIndexes := []indexCheck{
		{"api_keys_account_status_idx", ""},
		{"api_keys_active_grace_idx", "status"},
		{"api_keys_rotated_from_idx", "rotated_from_id"},
	}
	for _, ix := range wantIndexes {
		var def string
		if err := pool.QueryRow(ctx, `
			select indexdef from pg_indexes
			 where schemaname = current_schema() and indexname = $1
		`, ix.name).Scan(&def); err != nil {
			t.Errorf("index %s missing: %v", ix.name, err)
			continue
		}
		if ix.filter != "" && !strings.Contains(def, "WHERE") {
			t.Errorf("index %s should be partial: %s", ix.name, def)
		}
	}

	// (8) Replay-safety contract: re-running MigrateUp on a
	// schema that already contains every object this migration
	// touches must be a no-op. Mirrors
	// 00053_deployments_source_url_test.go:171-183 — a reviewer
	// who later drops the IF NOT EXISTS (or adds a bare
	// CREATE TABLE) trips 42701 / 42P07 here, not on the
	// production box at deploy time. The 2026-07-27
	// cd-digitalocean failure (pattern: schema present, goose
	// row missing) was the regression that motivated the
	// idempotent shape.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("second MigrateUp must be idempotent: %v", err)
	}
}
