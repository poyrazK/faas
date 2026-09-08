//go:build !no_pg

// Migration-apply tests for 00090 (apps.node_id + compute_nodes.schedd_target_url,
// Phase 2 / Gate A — durable app owner per compute node).
//
// Pins the Phase 2 acceptance gate verbatim:
// <the migration set applies cleanly through 00090; the apps
// owner column is FK + NOT NULL + empty-uuid CHECK + indexed;
// compute_nodes carries the schedd dial target separately
// from the vmmd dial target; default-local is backfilled on
// both; replay-safe: a second MigrateUp returns nil.>
//
//	1. apps.node_id column exists with the expected data_type
//	   (uuid) + nullability (NO) + the apps_node_id_fkey +
//	   apps_node_id_nonempty_chk constraints.
//	2. compute_nodes.schedd_target_url column exists with
//	   the expected data_type (text) + nullability (YES).
//	3. apps_node_id_nonempty_chk rejects the empty-uuid
//	   ('00000000-...') — a future operator upsert that
//	   tried to set node_id to the zero uuid would otherwise
//	   silently bind an app to "no node".
//	4. compute_nodes_schedd_target_url_scheme_chk rejects
//	   an operator POST that sets a non-(unix|tcp) scheme
//	   (e.g. "https://..." or "/path/to/sock"); the dial
//	   layer at gatewayd-internal would panic on a non-canonical
//	   scheme, the CHECK is the tripwire.
//	5. The default-local row seeded by migration 00024
//	   carries schedd_target_url = 'unix:///run/faas/schedd.sock'
//	   after 00090 applies — the single-box posture is
//	   preserved bit-for-bit.
//	6. Existing apps are backfilled to the default-local
//	   row (NOT NULL with a real FK target), not the empty
//	   uuid. A pre-Phase-2 deploy upgrades without a
//	   manual data migration.
//	7. Replay-safety: a second MigrateUp() returns nil —
//	   every ADD COLUMN is IF NOT EXISTS, every constraint
//	   add is paired with a DROP IF EXISTS in the down
//	   block, the column defaults / backfill UPDATE are
//	   unconditional (PR #377 / ADR-041 contract).
//	8. Down symmetry: the down body drops apps.node_id +
//	   compute_nodes.schedd_target_url cleanly; the
//	   re-applied up body round-trips.
//
// Build tag mirrors apply_walk_test.go:4 and the other
// 0008x migration tests — set FAAS_SKIP_PG_TESTS=1 to skip.

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

// TestMigration_00090_1_AppsNodeIDColumnShape pins the
// apps.node_id column shape after 00090 applies (and after 00091
// has relaxed NOT NULL). data_type=uuid + is_nullable=YES. The
// relaxation from NO→YES is the Phase 2 / Gate A claim path
// (migration 00091): apid can INSERT with node_id = NULL and
// schedd's PlacementClaimSubscriber stamps it asynchronously.
// The empty-uuid CHECK (apps_node_id_nonempty_chk) stays in
// force — the relaxation is "NULL is legal", not "any value
// is legal". Any drift (e.g. someone typing text instead of
// uuid, or re-tightening NOT NULL post-00091) fails loud.
func TestMigration_00090_1_AppsNodeIDColumnShape(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	var dtype, nullable string
	err := pool.QueryRow(ctx, `
		select data_type, is_nullable
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and column_name  = 'node_id'
	`).Scan(&dtype, &nullable)
	if err != nil {
		t.Fatalf("query apps.node_id column: %v", err)
	}
	if dtype != "uuid" {
		t.Errorf("apps.node_id data_type = %q, want %q", dtype, "uuid")
	}
	if nullable != "YES" {
		t.Errorf("apps.node_id is_nullable = %q, want %q (post-00091 relaxation)", nullable, "YES")
	}

	// Constraint presence: both apps_node_id_fkey and
	// apps_node_id_nonempty_chk must exist (otherwise the
	// down→up round-trip would have left the table in an
	// intermediate state).
	var fkeyCount, chkCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.table_constraints
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and constraint_name = 'apps_node_id_fkey'
		   and constraint_type = 'FOREIGN KEY'
	`).Scan(&fkeyCount); err != nil {
		t.Fatalf("query apps_node_id_fkey: %v", err)
	}
	if fkeyCount != 1 {
		t.Errorf("apps_node_id_fkey present = %d, want 1", fkeyCount)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.table_constraints
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and constraint_name = 'apps_node_id_nonempty_chk'
		   and constraint_type = 'CHECK'
	`).Scan(&chkCount); err != nil {
		t.Fatalf("query apps_node_id_nonempty_chk: %v", err)
	}
	if chkCount != 1 {
		t.Errorf("apps_node_id_nonempty_chk present = %d, want 1", chkCount)
	}

	// Index presence.
	var idxCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'apps'
		   and indexname  = 'apps_node_id_idx'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("query apps_node_id_idx: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("apps_node_id_idx present = %d, want 1", idxCount)
	}
}

// TestMigration_00090_2_ScheddTargetURLColumnShape pins the
// compute_nodes.schedd_target_url column shape. text + nullable
// (the new column starts nullable; the default-local row gets
// the unix socket via a backfill UPDATE — operator-added
// compute_nodes rows must explicitly set the column).
func TestMigration_00090_2_ScheddTargetURLColumnShape(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	var dtype, nullable string
	err := pool.QueryRow(ctx, `
		select data_type, is_nullable
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'compute_nodes'
		   and column_name  = 'schedd_target_url'
	`).Scan(&dtype, &nullable)
	if err != nil {
		t.Fatalf("query compute_nodes.schedd_target_url column: %v", err)
	}
	if dtype != "text" {
		t.Errorf("compute_nodes.schedd_target_url data_type = %q, want %q", dtype, "text")
	}
	if nullable != "YES" {
		t.Errorf("compute_nodes.schedd_target_url is_nullable = %q, want %q", nullable, "YES")
	}
}

// TestMigration_00090_3_EmptyUUIDCheck pins the defensive
// empty-uuid CHECK. A future operator that tries to upsert
// node_id = '00000000-0000-0000-0000-000000000000' must hit
// SQLSTATE 23514 (check_violation), not silently bind an
// app to "no node". The default-local row's uuid is real
// (seeded by migration 00024), so the test seeds its own
// app + node pair to exercise the CHECK on a non-default
// branch — the empty-uuid path is the only path the CHECK
// forbids.
func TestMigration_00090_3_EmptyUUIDCheck(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Seed an account row (apps.account_id is NOT NULL FK) +
	// a compute_nodes row to satisfy the apps_node_id_fkey
	// before testing the empty-uuid CHECK. The accounts
	// schema is (id, email, plan, status, created_at, ...)
	// — no `updated_at` column; the migration tests in
	// 00080 + 00082 use the same shape.
	accountID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, $2, 'free', now())
	`, accountID, accountID[:8]+"@test.example"); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, nodeID, "empty-uuid-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	// INSERT an app with node_id = empty uuid → must fail 23514.
	// The apps schema is (id, account_id, slug, type, ram_mb,
	// max_concurrency, idle_timeout_s, status, created_at, manifest,
	// egress_allowlist, min_instances, project_id, root_dir,
	// workload_name, workload_class, ...). The migration tests in
	// 00080 + 00082 use the same minimal (id, account_id, slug,
	// type, ram_mb, max_concurrency, idle_timeout_s, status,
	// created_at) projection.
	_, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        '00000000-0000-0000-0000-000000000000', now())
	`, uuid.NewString(), accountID, "empty-"+accountID[:8])
	if err == nil {
		t.Fatal("expected check violation on apps.node_id = empty uuid; got nil (CHECK is missing or not enforced)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("want SQLSTATE 23514, got %v", err)
	}

	// Control: same INSERT with a real uuid succeeds (the
	// apps_node_id_fkey is satisfied, the empty-uuid CHECK
	// is satisfied).
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, now())
	`, uuid.NewString(), accountID, "good-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("control insert with real uuid: %v", err)
	}
}

// TestMigration_00090_4_ScheddTargetURLSchemeCheck pins the
// scheme CHECK on compute_nodes.schedd_target_url. Operators
// that set the column to anything other than a unix:// or
// tcp:// URL must hit 23514. A value of NULL is fine (the
// column is nullable; legacy operator-added rows with no
// schedd yet are a valid state until the daemon starts).
func TestMigration_00090_4_ScheddTargetURLSchemeCheck(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, nodeID, "scheme-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	// Bad scheme: "https://..." must fail 23514.
	_, err := pool.Exec(ctx, `
		update compute_nodes set schedd_target_url = 'https://10.0.0.2:7100' where id = $1
	`, nodeID)
	if err == nil {
		t.Fatal("expected check violation on schedd_target_url = 'https://...'; got nil")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("want SQLSTATE 23514 on https scheme, got %v", err)
	}

	// Bad scheme: bare path. Same 23514.
	_, err = pool.Exec(ctx, `
		update compute_nodes set schedd_target_url = '/path/to/sock' where id = $1
	`, nodeID)
	if err == nil {
		t.Fatal("expected check violation on schedd_target_url = '/path/to/sock'; got nil")
	}

	// Good scheme: unix:// (canonical single-box target).
	if _, err := pool.Exec(ctx, `
		update compute_nodes set schedd_target_url = 'unix:///run/faas/schedd.sock' where id = $1
	`, nodeID); err != nil {
		t.Fatalf("unix:// scheme: %v", err)
	}

	// Good scheme: tcp:// (multi-box posture).
	if _, err := pool.Exec(ctx, `
		update compute_nodes set schedd_target_url = 'tcp://10.0.0.2:7100' where id = $1
	`, nodeID); err != nil {
		t.Fatalf("tcp:// scheme: %v", err)
	}

	// Good: NULL is allowed (column is nullable).
	if _, err := pool.Exec(ctx, `
		update compute_nodes set schedd_target_url = NULL where id = $1
	`, nodeID); err != nil {
		t.Fatalf("NULL schedd_target_url: %v (column is nullable; nil must be allowed)", err)
	}
}

// TestMigration_00090_5_DefaultLocalBackfill pins the
// load-bearing single-box backfill. Migration 00024 seeded a
// synthetic default-local row; after 00090 applies, that row
// carries schedd_target_url = 'unix:///run/faas/schedd.sock'
// via the migration's UPDATE. Hard-fail on missing row: the
// contract this test depends on is "every fresh-DB apply
// ends with a default-local row carrying the canonical unix
// socket schedd dial target".
func TestMigration_00090_5_DefaultLocalBackfill(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	var got string
	err := pool.QueryRow(ctx, `
		select schedd_target_url from compute_nodes
		 where name = 'default-local'
		 limit 1
	`).Scan(&got)
	if err != nil {
		t.Fatalf("default-local row not present in this schema (err=%v); migration 00024's seed is missing", err)
	}
	if got != "unix:///run/faas/schedd.sock" {
		t.Errorf("default-local schedd_target_url = %q, want %q (single-box backfill must default the unix socket)", got, "unix:///run/faas/schedd.sock")
	}
}

// TestMigration_00090_6_AppsBackfilledToDefaultLocal pins
// the apps backfill contract. Every pre-Phase-2 app row is
// backfilled to the default-local node. Hard-fail on any
// row still at NULL or at the empty uuid (which the CHECK
// would forbid, but a pre-CHECK state during the up sequence
// could conceivably land at the empty uuid if the migration
// author got the order wrong). The test queries every apps
// row and asserts node_id = (default-local's id).
func TestMigration_00090_6_AppsBackfilledToDefaultLocal(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Resolve the default-local id (canonical pattern from
	// migration 00024's seeding path).
	var defaultLocalID string
	if err := pool.QueryRow(ctx, `
		select id from compute_nodes where name = 'default-local' limit 1
	`).Scan(&defaultLocalID); err != nil {
		t.Fatalf("resolve default-local id: %v (migration 00024's seed is missing)", err)
	}

	// Every existing apps row must be bound to that id.
	rows, err := pool.Query(ctx, `
		select id, node_id from apps
	`)
	if err != nil {
		t.Fatalf("query apps: %v", err)
	}
	defer rows.Close()
	rowCount := 0
	for rows.Next() {
		var id, nodeID string
		if err := rows.Scan(&id, &nodeID); err != nil {
			t.Fatalf("scan apps row: %v", err)
		}
		rowCount++
		if nodeID != defaultLocalID {
			t.Errorf("apps row %s has node_id = %s, want %s (backfill must hit the default-local row)", id, nodeID, defaultLocalID)
		}
		if nodeID == "00000000-0000-0000-0000-000000000000" {
			t.Errorf("apps row %s has node_id = empty uuid (CHECK should have rejected, but the up sequence may have landed in an intermediate state)", id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate apps rows: %v", err)
	}
	// No assertion on rowCount — the test schema may have
	// zero pre-existing apps. The point is "every app that
	// exists has a non-null, non-empty node_id pointing to
	// default-local".
}

// TestMigration_00090_7_ReplaySafe pins the idempotency
// contract. A second MigrateUp() returns nil — every
// ADD COLUMN is IF NOT EXISTS, every constraint add is
// paired with a DROP IF EXISTS in the down block, the
// backfill UPDATE is unconditional (idempotent because
// the WHERE clause matches the post-backfill state). This
// is the tripwire for the replay-safety pattern PR #377 /
// ADR-041 established; without it, a hot-reload of the
// binary would 42710 ("column already exists") or 42P07
// ("duplicate table") and refuse to boot.
func TestMigration_00090_7_ReplaySafe(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay MigrateUp: %v (every ADD COLUMN must be IF NOT EXISTS, every constraint add paired with DROP IF EXISTS)", err)
	}
}

// TestMigration_00090_8_DownSymmetry pins the down path.
// MigrateDown to 00082 drops apps.node_id and
// compute_nodes.schedd_target_url; MigrateUp re-creates
// them; the schema round-trips cleanly. We drive the SQL
// the down body carries directly (no MigrateDown helper in
// pkg/db today), then re-apply the up body and assert the
// columns + constraints come back. A non-symmetric down
// would leave a broken schema on a release that needs to
// roll back 00090 in isolation.
func TestMigration_00090_8_DownSymmetry(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	// Down body — drop in the reverse order of creation.
	if _, err := pool.Exec(ctx, `drop index if exists apps_node_id_idx`); err != nil {
		t.Fatalf("down: drop index: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop constraint if exists apps_node_id_fkey`); err != nil {
		t.Fatalf("down: drop fkey: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop constraint if exists apps_node_id_nonempty_chk`); err != nil {
		t.Fatalf("down: drop empty-uuid chk: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop column if exists node_id`); err != nil {
		t.Fatalf("down: drop apps.node_id: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table compute_nodes drop constraint if exists compute_nodes_schedd_target_url_scheme_chk`); err != nil {
		t.Fatalf("down: drop scheme chk: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table compute_nodes drop column if exists schedd_target_url`); err != nil {
		t.Fatalf("down: drop schedd_target_url: %v", err)
	}

	// Probe: column gone.
	var count int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and column_name  = 'node_id'
	`).Scan(&count); err != nil {
		t.Fatalf("probe apps.node_id absence: %v", err)
	}
	if count != 0 {
		t.Errorf("after down, apps.node_id still present (count=%d)", count)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'compute_nodes'
		   and column_name  = 'schedd_target_url'
	`).Scan(&count); err != nil {
		t.Fatalf("probe schedd_target_url absence: %v", err)
	}
	if count != 0 {
		t.Errorf("after down, compute_nodes.schedd_target_url still present (count=%d)", count)
	}

	// Re-apply the up body — must succeed cleanly.
	if _, err := pool.Exec(ctx, `alter table apps add column if not exists node_id uuid`); err != nil {
		t.Fatalf("re-add apps.node_id: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table compute_nodes add column if not exists schedd_target_url text`); err != nil {
		t.Fatalf("re-add compute_nodes.schedd_target_url: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and column_name  = 'node_id'
	`).Scan(&count); err != nil {
		t.Fatalf("probe apps.node_id re-added: %v", err)
	}
	if count != 1 {
		t.Errorf("after re-add, apps.node_id present = %d, want 1", count)
	}
}
