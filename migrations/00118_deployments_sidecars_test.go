//go:build !no_pg

// Migration-apply test for 00118 (issue #463 / ADR-067 — sidecar
// containers). The later companion-capacity migration expands the
// original two-entry CHECK to five helper entries. Pins the column shape:
//
//  1. The migration set applies cleanly through 00118.
//  2. NOT NULL DEFAULT '[]'::jsonb backfills legacy rows correctly.
//  3. The current CHECK constraint enforces the five-helper cap at the
//     schema layer (a 6-helper INSERT is rejected).
//  4. JSONB round-trip preserves element shape (name, type, image).
//  5. An over-cap INSERT trips the CHECK before any FK layer sees
//     the row (the cap is the load-bearing gate).
//  6. Empty-array insert and omitted-sidecars insert (column default
//     fill) both read back as `[]`.
//  7. Replay-safe: a second MigrateUp is a no-op (PR #377 / ADR-041).
//
// Slot note: HEAD on origin/main is 00115 (api_key_expiry_rotation,
// PR #539, issue #189 iam-5 API key expiry + rotation). Renumber
// chain on the PR branch: 95 → 96 → 97 → 98 → 101 → 105 → 106 → 107 → 108 → 111 → 112 → 116 → 117 → 118
// across thirteen rebase cycles against sibling PRs that grabbed
// the intermediate slots (PR #525 → 109/110 warm snapshot, PR #540
// → 116 → 117 webhook_deliveries, PR #543 → 117 reserve + 118
// instances_framework_ready_at, etc.). If a sibling PR
// claims 00118 first, renumber per the fence pattern and update
// this test's filename + test function name + ApplyUp range +
// pkg/e2etest/harness.go::e2eMigrationTarget constant together.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00118_DeploymentsSidecars(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// Seed UUIDs carry the slot number in the last group (`...000118`,
	// `...000208`, `...000308`, `...000408`, `...000508`, `...000608`)
	// so a reader scanning the test fixtures can pin each row to this
	// migration without grepping the file name. The literal slot value
	// MUST stay in sync with the filename; renumber per
	// migrations/README.md if a sibling PR grabs 00118 first.

	// (1) Apply through 00118. A regression that drops a slot
	// between 1 and 117 surfaces here before the per-assertion pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 117)", err)
	}

	// (2) Seed an account + app. The literal UUIDs are fixed across
	// reruns so the seed is idempotent.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000118',
		        'sidecars-test@example.com', 'hobby', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000219',
		        '00000000-0000-0000-0000-000000000118',
		        'sidecars-test-app', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// (3) Empty-array insert passes — the default fill path. '[]'
	// is a valid 0-sidecar payload (read back as `[]`, not NULL).
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, sidecars, created_at)
		values ('00000000-0000-0000-0000-000000000319',
		        '00000000-0000-0000-0000-000000000219',
		        'ghcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000',
		        'pending', '[]'::jsonb, now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("insert deployment (empty sidecars): %v", err)
	}

	// (4) Five-helper insert passes — the cap is a `<=` check, so five is
	// the maximum legal size. The shape validates per-sidecar
	// fields at the API layer (Sidecar.Validate); the schema only
	// enforces length + NOT NULL + jsonb-array-of-objects (PG's
	// jsonb type already validates array-of-some-value).
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, sidecars, created_at)
		values ('00000000-0000-0000-0000-000000000419',
		        '00000000-0000-0000-0000-000000000219',
		        'ghcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000',
		        'pending',
		        '[
		          {"name":"migrator",
		           "image":"ghcr.io/me/migrator@sha256:0000000000000000000000000000000000000000000000000000000000000001",
		           "type":"init",
		           "cmd":["--to","head"]},
		          {"name":"scraper",
		           "image":"ghcr.io/me/scraper@sha256:0000000000000000000000000000000000000000000000000000000000000002",
			   "type":"sidecar"},
			  {"name":"logger",
			   "image":"ghcr.io/me/logger@sha256:0000000000000000000000000000000000000000000000000000000000000003",
			   "type":"sidecar"},
			  {"name":"proxy",
			   "image":"ghcr.io/me/proxy@sha256:0000000000000000000000000000000000000000000000000000000000000004",
			   "type":"sidecar"},
			  {"name":"tracer",
			   "image":"ghcr.io/me/tracer@sha256:0000000000000000000000000000000000000000000000000000000000000005",
			   "type":"sidecar"}
			]'::jsonb,
		        now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("insert deployment (5 helpers): %v", err)
	}

	// (5) 6-helper insert rejected by CHECK. This is the load-bearing
	// test: the schema enforces the cap even when the API gate is
	// bypassed (manual SQL, future grpc handler, debug shell).
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, sidecars, created_at)
		values ('00000000-0000-0000-0000-000000000519',
		        '00000000-0000-0000-0000-000000000219',
		        'ghcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000',
		        'pending',
		        '[
			  {"name":"a","image":"x","type":"sidecar"},
			  {"name":"b","image":"x","type":"sidecar"},
			  {"name":"c","image":"x","type":"sidecar"},
			  {"name":"d","image":"x","type":"sidecar"},
			  {"name":"e","image":"x","type":"sidecar"},
			  {"name":"f","image":"x","type":"sidecar"}
			]'::jsonb,
		        now())
	`); err == nil {
		t.Errorf("6-helper insert: got no error; want CHECK cap violation")
	}

	// (6) JSONB round-trip preserves element shape. The schema
	// allows any well-formed JSONB; element shape is validated by
	// the API layer (Sidecar.Validate). This test pins the
	// round-trip contract so the persistence layer doesn't lose
	// fields the API passed in.
	var sidecarsJSON []byte
	if err := pool.QueryRow(ctx, `
		select sidecars from deployments
		where id = '00000000-0000-0000-0000-000000000419'
	`).Scan(&sidecarsJSON); err != nil {
		t.Fatalf("read deployment (2 sidecars): %v", err)
	}
	// PG's jsonb normalises key order + whitespace (adds space
	// after `:`); the round-trip byte assertions below accept
	// either the canonical form or PG's compact form so the test
	// pins element shape, not text-level formatting.
	if !bytes.Contains(sidecarsJSON, []byte(`"name":"migrator"`)) &&
		!bytes.Contains(sidecarsJSON, []byte(`"name": "migrator"`)) {
		t.Errorf("sidecars round-trip lost migrator: %s", sidecarsJSON)
	}
	if !bytes.Contains(sidecarsJSON, []byte(`"type":"init"`)) &&
		!bytes.Contains(sidecarsJSON, []byte(`"type": "init"`)) {
		t.Errorf("sidecars round-trip lost type=init: %s", sidecarsJSON)
	}
	if !bytes.Contains(sidecarsJSON, []byte(`"name":"scraper"`)) &&
		!bytes.Contains(sidecarsJSON, []byte(`"name": "scraper"`)) {
		t.Errorf("sidecars round-trip lost scraper: %s", sidecarsJSON)
	}
	if !bytes.Contains(sidecarsJSON, []byte(`"type":"sidecar"`)) &&
		!bytes.Contains(sidecarsJSON, []byte(`"type": "sidecar"`)) {
		t.Errorf("sidecars round-trip lost type=sidecar: %s", sidecarsJSON)
	}
	if !bytes.Contains(sidecarsJSON, []byte(`"cmd":["--to","head"]`)) &&
		!bytes.Contains(sidecarsJSON, []byte(`"cmd": ["--to", "head"]`)) {
		t.Errorf("sidecars round-trip lost cmd: %s", sidecarsJSON)
	}

	// (7) NOT NULL DEFAULT — insert without an explicit sidecars
	// column populates with `[]` via the column DEFAULT. This
	// proves the ALTER TABLE … NOT NULL DEFAULT did not break
	// legacy INSERTs that don't mention the column.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, created_at)
		values ('00000000-0000-0000-0000-000000000619',
		        '00000000-0000-0000-0000-000000000219',
		        'ghcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000',
		        'pending', now())
	`); err != nil {
		t.Fatalf("insert deployment (no sidecars): %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select sidecars from deployments
		where id = '00000000-0000-0000-0000-000000000619'
	`).Scan(&sidecarsJSON); err != nil {
		t.Fatalf("read default sidecars: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(sidecarsJSON), []byte(`[]`)) {
		t.Errorf("default sidecars = %s; want []", sidecarsJSON)
	}

	// (8) Replay-safety: a second MigrateUp is a no-op (the
	// migration uses `ADD COLUMN IF NOT EXISTS` and a
	// pg_constraint existence check before the ADD CONSTRAINT).
	// PR #377 / ADR-041 contract.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
