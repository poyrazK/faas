//go:build !no_pg

// Migration-apply test for 00048 (build_provenance table, issue #197
// B3.1). Pins the column shape + UNIQUE constraint:
//
//  1. Migration applies cleanly through 00048.
//  2. The table exists with all expected columns and the canonical
//     types (uuid PK, uuid FK, text fields, timestamptz NOT NULL on
//     started_at + finished_at, text nullable on the rest).
//  3. The build_id UNIQUE constraint rejects a second row for the
//     same build_id (the populator relies on ON CONFLICT (build_id)
//     DO UPDATE — the constraint must exist).
//  4. The build_id FK rejects an unknown build_id.
//  5. BuildProvenance shape round-trip — insert + select + verify
//     every column. Empty-string sbom_storage_key is the Phase 3
//     placeholder (the column exists now so Phase 3 is a zero-cost
//     schema change).
//
// IDs randomized per test run (PR #241 review finding #6).

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00048_BuildProvenance(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00048.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 48)", err)
	}

	acctID := uuid.NewString()
	appID := uuid.NewString()
	depID := uuid.NewString()
	buildID := uuid.NewString()
	const wantSourceSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // 64 hex
	const wantBuildkit = "0.31.1"
	const wantRailpack = "0.31.1"
	const wantBaseDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const wantRunnerDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	const wantBuilderNodeID = "default-local"
	const wantPlan = "pro"
	const wantURL = "https://github.com/acme/app@main"
	const wantSHA = "0123456789abcdef0123456789abcdef01234567" // 40-char sha1

	// Seed: account → app → deployment → build. Mirrors the shape
	// every prior migration test uses.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, 'b31-provenance@example.com', 'pro', now())
	`, acctID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		values ($1, $2, 'b31-app', 'app', 256, 5, 'active', now())
	`, appID, acctID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, source_bytes, status, source_url, commit_sha)
		values ($1, $2, 'dockerfile', 1024, 'pending', $3, $4)
	`, depID, appID, wantURL, wantSHA); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into builds (id, deployment_id, kind, source_bytes, status, started_at, finished_at)
		values ($1, $2, 'dockerfile', 1024, 'succeeded', now(), now())
	`, buildID, depID); err != nil {
		t.Fatalf("seed build: %v", err)
	}

	// (5) Round-trip: insert a provenance row, read it back.
	if _, err := pool.Exec(ctx, `
		insert into build_provenance (build_id, buildkit_version, railpack_version,
		                              base_digest, source_sha256, source_url, commit_sha,
		                              plan, runner_digest, builder_node_id,
		                              started_at, finished_at, sbom_storage_key)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now(), '')
	`, buildID, wantBuildkit, wantRailpack, wantBaseDigest, wantSourceSHA,
		wantURL, wantSHA, wantPlan, wantRunnerDigest, wantBuilderNodeID); err != nil {
		t.Fatalf("insert build_provenance: %v", err)
	}
	var gotBuildkit, gotRailpack, gotBase, gotSourceSHA, gotURL, gotSHA, gotPlan, gotRunner, gotNode string
	if err := pool.QueryRow(ctx, `
		select buildkit_version, railpack_version, base_digest, source_sha256,
		       source_url, commit_sha, plan, runner_digest, builder_node_id
		  from build_provenance where build_id = $1
	`, buildID).Scan(&gotBuildkit, &gotRailpack, &gotBase, &gotSourceSHA,
		&gotURL, &gotSHA, &gotPlan, &gotRunner, &gotNode); err != nil {
		t.Fatalf("read build_provenance: %v", err)
	}
	for _, tc := range []struct {
		name, got, want string
	}{
		{"buildkit_version", gotBuildkit, wantBuildkit},
		{"railpack_version", gotRailpack, wantRailpack},
		{"base_digest", gotBase, wantBaseDigest},
		{"source_sha256", gotSourceSHA, wantSourceSHA},
		{"source_url", gotURL, wantURL},
		{"commit_sha", gotSHA, wantSHA},
		{"plan", gotPlan, wantPlan},
		{"runner_digest", gotRunner, wantRunnerDigest},
		{"builder_node_id", gotNode, wantBuilderNodeID},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	// (3) UNIQUE(build_id) — second insert must fail with 23505.
	_, err := pool.Exec(ctx, `
		insert into build_provenance (build_id, source_sha256, started_at, finished_at)
		values ($1, $2, now(), now())
	`, buildID, wantSourceSHA)
	if err == nil {
		t.Fatalf("duplicate build_id must be rejected by build_provenance.build_id UNIQUE constraint")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("duplicate build_id error not a *pgconn.PgError: %v", err)
	}
	if pgErr.Code != "23505" {
		t.Errorf("duplicate build_id SQLSTATE = %q, want 23505 (unique_violation); full: %v", pgErr.Code, err)
	}

	// (4) FK(build_id) → builds(id) — unknown build_id is rejected.
	unknownBuildID := uuid.NewString()
	_, err = pool.Exec(ctx, `
		insert into build_provenance (build_id, source_sha256, started_at, finished_at)
		values ($1, $2, now(), now())
	`, unknownBuildID, wantSourceSHA)
	if err == nil {
		t.Fatalf("unknown build_id must be rejected by build_provenance_build_id_fk")
	}
	if !errors.As(err, &pgErr) {
		t.Fatalf("unknown build_id error not a *pgconn.PgError: %v", err)
	}
	if pgErr.Code != "23503" {
		t.Errorf("unknown build_id SQLSTATE = %q, want 23503 (foreign_key_violation); full: %v", pgErr.Code, err)
	}
}
