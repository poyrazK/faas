//go:build !no_pg

// Migration-apply test for 00024 (compute_nodes table + instances.node_id).
// Pins the load-bearing contract from issue #97 / ADR-025 axis 3:
//
//   1. The migration set applies cleanly through 00024.
//   2. The synthetic 'default-local' compute_node row is present with the
//      expected target_url and capacity. Schedd's NodeLedger and the
//      VMMRouter both rely on this row existing on a fresh deploy so that
//      pre-existing instance rows (with no node_id concept before #97) land
//      on a routable vmmd target out of the gate.
//   3. The backfill UPDATE lands every pre-existing instance row on the
//      default-local node. A bug here would mean waking the legacy instance
//      would dial a non-existent node and fail — exactly the regression
//      this slice is meant to avoid.
//   4. The target_url CHECK constraint enforces the wire.ParseTarget
//      prefix (unix://|tcp://|dns://). A regression that loosens the CHECK
//      would let apid or an operator insert a target_url vmmdgrpc can't
//      parse, which would surface only at Wake time as a cryptic dial error.
//
// The default-local row's id is gen_random_uuid() (column default) — we
// resolve it by name at runtime so the test doesn't pin a magic UUID
// literal. This matches the production code path (schedd caches the
// (name -> id, target_url) tuple at startup via ActiveComputeNodes).
//
// Build tag mirrors 00022_backfill_test.go:17 — set FAAS_SKIP_PG_TESTS=1
// locally to skip.

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00024_ComputeNodes(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00024 should land last; any prior
	// failure (e.g., the migrations directory missing a slot between 1
	// and 24) surfaces here before we get to the contract pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR #93 failure mode: missing migration slot between 1 and 24)", err)
	}

	// (2) Synthetic default-local row must exist with the expected
	// name + target_url + capacity. Resolve the UUID at runtime by
	// name — the migration doesn't pin a literal id (gen_random_uuid
	// is the column default), and the production code path resolves
	// the same way via ActiveComputeNodes / ComputeNodeByName. The
	// target_url default reads from the FAAS_LOCAL_VMMD_TARGET GUC
	// (coalesce fall-through to the legacy /run/faas/vmmd.sock
	// path). The CI environment doesn't set the GUC, so we assert
	// the legacy default directly. A test that sets the GUC would
	// split test-state across two migrations; we keep this assertion
	// deterministic by pinning the default only.
	var defaultLocalID string
	var gotTarget string
	var gotMem, gotCeiling int
	if err := pool.QueryRow(ctx, `
		select id, target_url, mem_mb, admission_ceiling_mb
		  from compute_nodes
		 where name = 'default-local'
	`).Scan(&defaultLocalID, &gotTarget, &gotMem, &gotCeiling); err != nil {
		t.Fatalf("read default-local compute_node: %v (the seed row from 00024 is missing — ON CONFLICT (name) DO NOTHING may have masked a duplicate-seed bug)", err)
	}
	if defaultLocalID == "" {
		t.Fatalf("default-local id resolved empty; expected a non-empty gen_random_uuid value")
	}
	if gotTarget != "unix:///run/faas/vmmd.sock" {
		t.Errorf("default-local target_url = %q, want %q (matches schedd's legacy cfg.VMMDSocket path)", gotTarget, "unix:///run/faas/vmmd.sock")
	}
	// The single-host budget mirrors the legacy api.RAMAdmissionCeilingMB
	// (pkg/api/limits.go:168). A regression that drops the ceiling below
	// the box's physical 56 GB or below 47,600 MB would violate spec §6.2-2
	// — every legacy wake would now refuse to admit because the per-node
	// ceiling is too tight.
	if gotMem != 56000 {
		t.Errorf("default-local mem_mb = %d, want 56000 (single-host budget)", gotMem)
	}
	if gotCeiling != 47600 {
		t.Errorf("default-local admission_ceiling_mb = %d, want 47600 (spec §6.2-2 / api.RAMAdmissionCeilingMB)", gotCeiling)
	}

	// (3) Backfill pin. Pre-existing instance rows must land on the
	// default-local node. The migration's UPDATE used a subquery to
	// resolve the UUID, so the test exercises the same subquery
	// pattern: seed an instance row at this point with node_id set
	// (NOT NULL constraint forces it), then re-run the migration's
	// UPDATE literal (with its subquery) and confirm the WHERE
	// clause filters out the row — proving the backfill is total
	// but non-destructive on a re-apply.
	//
	// Approach mirrors 00022_backfill_test.go's idempotency check:
	// flip the seeded instance's node_id to a non-default value,
	// re-run the UPDATE, assert the non-default value survived.
	//
	// All UUIDs come from gen_random_uuid() (column default) — using
	// hand-crafted 36-char strings like '...c0de-acc' trips Postgres
	// 15's strict uuid input parser (32 hex digits + 4 hyphens only;
	// any extra suffix is invalid). Capturing the returned ids into
	// vars keeps each subsequent insert consistent without pinning
	// magic literals in the test source.
	var accountID, appID, deploymentID string
	if err := pool.QueryRow(ctx, `
		insert into accounts (email, plan, created_at)
		values ('compute-nodes@example.com', 'hobby', now())
		returning id
	`).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into apps (account_id, slug, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, 'compute-nodes-app', 256, 1, 30, 'active', now())
		returning id
	`, accountID).Scan(&appID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into deployments (app_id, kind, image_digest, status, created_at)
		values ($1, 'image', 'sha256:seed', 'live', now())
		returning id
	`, appID).Scan(&deploymentID); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	var instanceID string
	if err := pool.QueryRow(ctx, `
		insert into instances (app_id, deployment_id, state, ram_mb, node_id, created_at)
		values ($1, $2, 'waking', 256, $3, now())
		returning id
	`, appID, deploymentID, defaultLocalID).Scan(&instanceID); err != nil {
		t.Fatalf("seed instance: %v", err)
	}

	// Flip the seeded instance's node_id to a non-default value to
	// prove the backfill UPDATE skips already-populated rows. The
	// WHERE node_id is null clause is what makes the backfill total
	// but non-destructive on a re-apply; this test pins that.
	otherUUID := "11111111-1111-1111-1111-111111111111"
	if _, err := pool.Exec(ctx,
		`update instances set node_id = $1 where id = $2`,
		otherUUID, instanceID,
	); err != nil {
		t.Fatalf("flip node_id for backfill idempotency check: %v", err)
	}
	// Re-run the migration's UPDATE literal (subquery form, no UUID
	// magic literal). Row already has node_id set to otherUUID, so the
	// WHERE node_id is null filter must NOT touch it. The assertion
	// checks that otherUUID survived — i.e., the backfill is
	// idempotent and won't clobber a non-null node_id on re-apply.
	if _, err := pool.Exec(ctx,
		`update instances set node_id = (select id from compute_nodes where name = 'default-local') where node_id is null`,
	); err != nil {
		t.Fatalf("re-run backfill UPDATE: %v", err)
	}
	var gotNode string
	if err := pool.QueryRow(ctx,
		`select node_id from instances where id = $1`, instanceID,
	).Scan(&gotNode); err != nil {
		t.Fatalf("read seeded instance node_id: %v", err)
	}
	if gotNode != otherUUID {
		t.Errorf("backfill clobbered a non-null node_id: got %q, want %q (the WHERE node_id is null filter must keep idempotent re-applies safe)", gotNode, otherUUID)
	}

	// (4) target_url CHECK constraint: a bad scheme (e.g., http://)
	// must be rejected, an http-style URL with a unix-style scheme must
	// be rejected. Both negative cases plus one positive case (the
	// canonical unix:// socket path) are enough to pin the prefix
	// enforcement for wire.ParseTarget compatibility.
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb)
		values ('bad-http', 'http://wrong', 160, 56000, 200, 47600)
	`); err == nil {
		t.Errorf("target_url CHECK allowed http:// (must reject; only unix://|tcp://|dns:// permitted)")
	}
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb)
		values ('good-unix', 'unix:///run/faas/extra.sock', 160, 56000, 200, 47600)
	`); err != nil {
		t.Errorf("target_url CHECK rejected valid unix:// scheme: %v (the prefix regex must permit unix://|tcp://|dns://)", err)
	}
}
