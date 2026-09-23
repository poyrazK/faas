//go:build !no_pg

// Migration-apply test for 00127 (issue #463 / ADR-069 / PR-B —
// per-workload filesystem handle for sidecars). Pins the
// `deployment_sidecar_layers` table shape:
//
//  1. The migration set applies cleanly through 00127.
//  2. The new table is reachable from the FK side: a deployment row
//     can carry a sidecar layer row.
//  3. Unique PK `(deployment_id, sidecar_name)` rejects dup
//     (deployment_id, sidecar_name) — the load-bearing
//     uniqueness for imaged's UPSERT path.
//  4. ON DELETE CASCADE — delete the deployment, the sidecar layer
//     row goes with it (matches the project-wide "writer of last
//     resort" cleanup discipline).
//  5. The storage_key index exists (query planner can use it).
//  6. Replay-safety: a second MigrateUp is a no-op (PR #377 / ADR-041).
//
// Slot note: PR-B originally claimed 00119; main's recent merges
// added 00119/00120/00121 reservation fences + 00122 framework_ready_at
// (PR #470-FU-B) + 00123 compute_nodes_vcpu_budget, then PR #547
// (open) added 00124_reserve_slot + 00125_warm_hint + 00126_pg_ratelimit,
// so this migration renumbered to 00127 — past PR #547's 00126
// ceiling. Bump filename + test function name + ApplyUp range +
// pkg/e2etest/harness.go::e2eMigrationTarget together if the slot
// changes again.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00127_DeploymentsSidecarLayers(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// Seed UUIDs carry the slot number in the last group
	// (`...000119`, `...000219`, `...000319`, `...000419`) so a
	// reader scanning the test fixtures can pin each row to this
	// migration without grepping the file name. The literal slot
	// value MUST stay in sync with the filename; renumber per
	// migrations/README.md if a sibling PR grabs 00119 first.

	// (1) Apply through 00127. A regression that drops a slot
	// between 1 and 126 surfaces here before the per-assertion
	// pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 126)", err)
	}

	// (2) Seed an account + app + deployments, reusing the
	// PR-A fixture UUIDs (000118 / 000219) so this test is
	// idempotent alongside the 00118 test. The deployment UUID
	// `000319` is the sidecar-layer test fixture.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000118',
		        'sidecars-layers-test@example.com', 'hobby', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000219',
		        '00000000-0000-0000-0000-000000000118',
		        'sidecars-layers-test-app', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, sidecars, created_at)
		values ('00000000-0000-0000-0000-000000000319',
		        '00000000-0000-0000-0000-000000000219',
		        'ghcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000',
		        'pending', '[]'::jsonb, now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	// (3) Two distinct sidecars on one deployment — happy path.
	// This is the imaged-side UPSERT pattern (issue #463 / PR-A
	// contract): per-sidecar row keyed by (deployment_id, name).
	if _, err := pool.Exec(ctx, `
		insert into deployment_sidecar_layers
		    (deployment_id, sidecar_name, storage_key, bytes, content_digest)
		values
		    ('00000000-0000-0000-0000-000000000319',
		     'migrator',
		     'apps/sidecars-layers-test-app/00000000-0000-0000-0000-000000000319-migrator.ext4',
		     1048576,
		     'sha256:0000000000000000000000000000000000000000000000000000000000000001'),
		    ('00000000-0000-0000-0000-000000000319',
		     'scraper',
		     'apps/sidecars-layers-test-app/00000000-0000-0000-0000-000000000319-scraper.ext4',
		     524288,
		     'sha256:0000000000000000000000000000000000000000000000000000000000000002')
	`); err != nil {
		t.Fatalf("insert sidecar layers: %v", err)
	}

	// (4) PK uniqueness rejection. A duplicate (deployment_id,
	// sidecar_name) must fail. This pins the imaged-UPSERT
	// semantics: the same sidecar name rebuilding its layer
	// hits ON CONFLICT DO UPDATE in production code, not a
	// partial second insert.
	if _, err := pool.Exec(ctx, `
		insert into deployment_sidecar_layers
		    (deployment_id, sidecar_name, storage_key, bytes, content_digest)
		values
		    ('00000000-0000-0000-0000-000000000319',
		     'migrator',
		     'apps/dup.ext4', 1024, 'sha256:0000000000000000000000000000000000000000000000000000000000000099')
	`); err == nil {
		t.Errorf("duplicate (deployment_id, sidecar_name): got no error; want PK violation")
	}

	// (5) Reading back works and shape is preserved. The two
	// distinct sidecars must round-trip with their original
	// (storage_key, bytes, content_digest). A regression that
	// silently drops a column would surface here.
	var gotMigrator, gotScraper string
	if err := pool.QueryRow(ctx, `
		select storage_key from deployment_sidecar_layers
		where deployment_id = '00000000-0000-0000-0000-000000000319'
		  and sidecar_name = 'migrator'
	`).Scan(&gotMigrator); err != nil {
		t.Fatalf("read migrator: %v", err)
	}
	if gotMigrator != "apps/sidecars-layers-test-app/00000000-0000-0000-0000-000000000319-migrator.ext4" {
		t.Errorf("migrator storage_key round-trip: got %q", gotMigrator)
	}
	if err := pool.QueryRow(ctx, `
		select storage_key from deployment_sidecar_layers
		where deployment_id = '00000000-0000-0000-0000-000000000319'
		  and sidecar_name = 'scraper'
	`).Scan(&gotScraper); err != nil {
		t.Fatalf("read scraper: %v", err)
	}
	if gotScraper != "apps/sidecars-layers-test-app/00000000-0000-0000-0000-000000000319-scraper.ext4" {
		t.Errorf("scraper storage_key round-trip: got %q", gotScraper)
	}

	// (6) FK ON DELETE CASCADE — add a fresh deployment (so we
	// don't trip the round-trip reads above), attach one sidecar
	// layer, delete the deployment, and assert the row in
	// deployment_sidecar_layers goes with it. This is the
	// safety-net for imaged's cleanupAppFiles walk — the FK
	// cascade catches what the explicit DELETE misses on
	// partial-failure paths.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, created_at)
		values ('00000000-0000-0000-0000-000000000419',
		        '00000000-0000-0000-0000-000000000219',
		        'ghcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000',
		        'pending', now())
	`); err != nil {
		t.Fatalf("seed cascade deployment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployment_sidecar_layers
		    (deployment_id, sidecar_name, storage_key, bytes, content_digest)
		values
		    ('00000000-0000-0000-0000-000000000419',
		     'will-cascade',
		     'apps/cascade.ext4', 4096,
		     'sha256:0000000000000000000000000000000000000000000000000000000000000042')
	`); err != nil {
		t.Fatalf("insert cascade layer: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		delete from deployments where id = '00000000-0000-0000-0000-000000000419'
	`); err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	var cascadeCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from deployment_sidecar_layers
		where deployment_id = '00000000-0000-0000-0000-000000000419'
	`).Scan(&cascadeCount); err != nil {
		t.Fatalf("count after cascade: %v", err)
	}
	if cascadeCount != 0 {
		t.Errorf("FK cascade did not remove sidecar layer row: count = %d, want 0", cascadeCount)
	}

	// (7) Index presence. We don't EXPLAIN — just confirm the
	// index was created (it appears in pg_indexes). A regression
	// that drops the index would only surface at scale, but
	// pinning the name here documents the contract for future
	// readers and catches a hand-DELETE on the index.
	var idxCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		where schemaname = current_schema()
		  and tablename = 'deployment_sidecar_layers'
		  and indexname = 'deployment_sidecar_layers_storage_key_idx'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("count pg_indexes: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("deployment_sidecar_layers_storage_key_idx missing (count = %d, want 1)", idxCount)
	}

	// (8) Replay-safety: a second MigrateUp is a no-op (the
	// migration uses `CREATE TABLE IF NOT EXISTS` + `CREATE
	// INDEX IF NOT EXISTS`). PR #377 / ADR-041 contract.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}

	// (9) The per-deployment five-row cap is enforced by a BEFORE
	// INSERT OR UPDATE trigger. Insert five rows, upsert one at the cap,
	// then attempt a sixth — it must fail with check_violation (SQLSTATE
	// 23514). This pins both bounded growth and the rebuild/upsert path.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, sidecars, created_at)
		values ('00000000-0000-0000-0000-000000000519',
		        '00000000-0000-0000-0000-000000000219',
		        'ghcr.io/foo/bar@sha256:0000000000000000000000000000000000000000000000000000000000000000',
		        'pending', '[]'::jsonb, now())
	`); err != nil {
		t.Fatalf("seed cap-test deployment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployment_sidecar_layers
		    (deployment_id, sidecar_name, storage_key, bytes, content_digest)
		values
		    ('00000000-0000-0000-0000-000000000519', 'cap-sidecar-1', 'apps/cap-1.ext4', 1024, 'sha256:0000000000000000000000000000000000000000000000000000000000000c01'),
		    ('00000000-0000-0000-0000-000000000519', 'cap-sidecar-2', 'apps/cap-2.ext4', 1024, 'sha256:0000000000000000000000000000000000000000000000000000000000000c02'),
		    ('00000000-0000-0000-0000-000000000519', 'cap-sidecar-3', 'apps/cap-3.ext4', 1024, 'sha256:0000000000000000000000000000000000000000000000000000000000000c03'),
		    ('00000000-0000-0000-0000-000000000519', 'cap-sidecar-4', 'apps/cap-4.ext4', 1024, 'sha256:0000000000000000000000000000000000000000000000000000000000000c04'),
		    ('00000000-0000-0000-0000-000000000519', 'cap-sidecar-5', 'apps/cap-5.ext4', 1024, 'sha256:0000000000000000000000000000000000000000000000000000000000000c05')
	`); err != nil {
		t.Fatalf("insert five rows under the cap: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployment_sidecar_layers
		    (deployment_id, sidecar_name, storage_key, bytes, content_digest)
		values
		    ('00000000-0000-0000-0000-000000000519', 'cap-sidecar-1', 'apps/cap-1-rebuilt.ext4', 2048, 'sha256:0000000000000000000000000000000000000000000000000000000000000c11')
		on conflict (deployment_id, sidecar_name) do update
		set storage_key = excluded.storage_key, bytes = excluded.bytes, content_digest = excluded.content_digest
	`); err != nil {
		t.Fatalf("upsert existing layer at cap: %v", err)
	}
	var err error
	_, err = pool.Exec(ctx, `
		insert into deployment_sidecar_layers
		    (deployment_id, sidecar_name, storage_key, bytes, content_digest)
		values
		    ('00000000-0000-0000-0000-000000000519',
			     'cap-sidecar-6',
			     'apps/cap-6.ext4', 1024,
			     'sha256:0000000000000000000000000000000000000000000000000000000000000c06')
	`)
	if err == nil {
		t.Errorf("sixth sidecar row on one deployment: got no error; want trigger cap rejection (SQLSTATE 23514)")
	} else if !strings.Contains(err.Error(), "exceeds the 5-row cap") &&
		!strings.Contains(err.Error(), "check_violation") {
		t.Errorf("sixth sidecar row: got %v; want trigger rejection citing the 5-row cap", err)
	}
}
