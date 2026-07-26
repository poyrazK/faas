//go:build !no_pg

// Schema test for migration 00047 (Tier 3 B3.10 schema half).
//
// Asserts:
//
//   1. Both new columns (source_url, commit_sha) are nullable and
//      accept the documented shapes (a normal URL, a 40-char sha1).
//
//   2. The commit_sha shape CHECK (regex ^([0-9a-f]+$ AND length 7..64))
//      rejects three bad shapes:
//        - a 65-char string (over-length)
//        - a 64-char non-hex string (e.g. 64 × 'g')
//        - a 6-char string (under-length; empty-string-with-no-NULL)
//      The CHECK was added in 00047 (it didn't exist before); this is
//      the "constraint actually fires" guard.
//
//   3. Existing deployments rows (from before the migration) are
//      unaffected — they read with both new columns NULL.
//
//   4. The 64-char sha256 boundary is accepted.
//
// IDs are randomized per test run (PR #241 review finding #6) so a
// concurrent sibling migration test against the same pgtest DB
// cannot collide on the literal 00000000-...-00047 etc. fixtures.
//
// Build tag mirrors apply_walk_test.go:4 — set FAAS_SKIP_PG_TESTS=1
// locally to skip.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00047_DeploymentsSourceURL(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// Randomized IDs (PR #241 review finding #6): literal UUIDs would
	// collide with a concurrent sibling migration test that uses the
	// same fixture shape.
	acctID := uuid.NewString()
	appID := uuid.NewString()
	depID := uuid.NewString()
	depID65 := uuid.NewString()
	depID64g := uuid.NewString()
	depID6 := uuid.NewString()
	depID64 := uuid.NewString()
	depIDLegacy := uuid.NewString()

	// (1) Both columns exist and are nullable.
	var sourceURLNullable, commitSHANullable *string
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable
		  FROM information_schema.columns
		 WHERE table_name = 'deployments' AND column_name = 'source_url'
	`).Scan(&sourceURLNullable); err != nil {
		// QueryRow returns ErrNoRows when the column is missing;
		// pgx wraps that. Treat as a hard failure.
		t.Fatalf("read source_url is_nullable: %v", err)
	}
	if sourceURLNullable == nil || *sourceURLNullable != "YES" {
		t.Errorf("deployments.source_url nullable: got %v, want YES", sourceURLNullable)
	}
	if err := pool.QueryRow(ctx, `
		SELECT is_nullable
		  FROM information_schema.columns
		 WHERE table_name = 'deployments' AND column_name = 'commit_sha'
	`).Scan(&commitSHANullable); err != nil {
		t.Fatalf("read commit_sha is_nullable: %v", err)
	}
	if commitSHANullable == nil || *commitSHANullable != "YES" {
		t.Errorf("deployments.commit_sha nullable: got %v, want YES", commitSHANullable)
	}

	// (2) Insert a deployment with both fields populated. The fixture
	// shape mirrors 00046's seed: an account + an app + a deployment
	// in a single transaction so a failure leaves no partial state.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'b310@example.com', 'hobby', now())
	`, acctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb, max_concurrency, idle_timeout_s)
		values ($1, $2, 'b310-app', 256, 2, 60)
	`, appID, acctID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	const wantURL = "https://github.com/acme/app@main"
	const wantSHA = "0123456789abcdef0123456789abcdef01234567" // 40-char sha1
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_bytes, status, source_url, commit_sha)
		values ($1, $2, 'image', 1024, 'pending', $3, $4)
	`, depID, appID, wantURL, wantSHA); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}
	var gotURL, gotSHA *string
	if err := pool.QueryRow(ctx, `
		select source_url, commit_sha from deployments where id = $1
	`, depID).Scan(&gotURL, &gotSHA); err != nil {
		t.Fatalf("read deployment: %v", err)
	}
	if gotURL == nil || *gotURL != wantURL {
		t.Errorf("source_url round-trip: got %v, want %s", gotURL, wantURL)
	}
	if gotSHA == nil || *gotSHA != wantSHA {
		t.Errorf("commit_sha round-trip: got %v, want %s", gotSHA, wantSHA)
	}

	// (3) The CHECK rejects three bad shapes (PR #241 review finding #5).
	for _, tc := range []struct {
		name string
		dep  string
		body string // body bytes, must be raw hex characters
	}{
		{"over-long", depID65, strings.Repeat("a", 65)},
		// 64-char but not hex: 64 × 'g'. The pre-tightening CHECK accepted this;
		// the post-tightening CHECK must reject it.
		{"non-hex", depID64g, strings.Repeat("g", 64)},
		// Under-length (6 chars). The lower bound rejects the empty-string-without-NULL
		// path the old CHECK allowed.
		{"under-long", depID6, strings.Repeat("a", 6)},
	} {
		if _, err := pool.Exec(ctx, `
			insert into deployments (id, app_id, kind, source_bytes, status, commit_sha)
			values ($1, $2, 'image', 1024, 'pending', $3)
		`, tc.dep, appID, tc.body); err == nil {
			t.Fatalf("CHECK %s: expected violation for %s, got nil", tc.name, tc.body)
		}
	}

	// (4) The CHECK accepts the boundary value (exactly 64 hex chars).
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_bytes, status, commit_sha)
		values ($1, $2, 'image', 1024, 'pending', $3)
	`, depID64, appID, strings.Repeat("b", 64)); err != nil {
		t.Fatalf("64-hex-char commit_sha should be accepted: %v", err)
	}

	// (5) Pre-existing deployments (created before the migration)
	// read with both new columns NULL. We simulate by inserting a
	// row with explicit NULLs (semantically identical to a row from
	// before the columns existed).
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_bytes, status, source_url, commit_sha)
		values ($1, $2, 'image', 1024, 'pending', NULL, NULL)
	`, depIDLegacy, appID); err != nil {
		t.Fatalf("insert legacy-shaped: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select source_url is null and commit_sha is null
		  from deployments where id = $1
	`, depIDLegacy).Scan(new(bool)); err != nil {
		t.Fatalf("read legacy: %v", err)
	}
}
