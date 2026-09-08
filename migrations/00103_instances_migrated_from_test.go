//go:build !no_pg

// Migration-apply tests for 00103 (instances.migrated_from_node_id +
// instances.migrated_at + instances.lease_token +
// instances_migrated_at_chk + instances_migrated_from_node_id_idx,
// Tier A5 cross-node live-instance migration, ADR-065, follow-up
// to ADR-064).
//
// Pins the Tier A5 schema contract verbatim:
//
//	1. instances.migrated_from_node_id column exists with the
//	   expected data_type (uuid) + nullability (YES). FK to
//	   compute_nodes(id) ON DELETE SET NULL — the lineage
//	   reference must be honest when a node is
//	   decommissioned.
//	2. instances.migrated_at column exists with data_type
//	   'timestamp with time zone' + nullable YES. The CHECK
//	   instances_migrated_at_chk tolerates NULL and tolerates
//	   a past timestamp; values clearly in the future still
//	   error 23514 (clock-skew guard, same shape as 00095 /
//	   00059).
//	3. instances.lease_token column exists with data_type
//	   'text' + nullable YES. Holds the per-migration UUID
//	   the new owner mints at Phase 1 of the four-phase
//	   handoff; the conditional-UPDATE predicate at Phase 3
//	   carries the lease_token so a peer claim can never
//	   silently succeed with a stale lease.
//	4. FK ON DELETE SET NULL: deleting the referenced
//	   compute_node sets migrated_from_node_id to NULL
//	   rather than cascading the instances row delete.
//	   (Cascading would orphan the migration lineage for
//	   every live-instance that ever ran on that node.)
//	5. instances_migrated_from_node_id_idx is a partial
//	   index over (migrated_from_node_id) WHERE
//	   migrated_from_node_id IS NOT NULL. The "NOT NULL"
//	   predicate keeps the index narrow (only instances
//	   that actually migrated). A NULL instance never
//	   appears in the index, mirroring the A4
//	   apps_reassigned_at_idx discipline.
//	6. Replay-safety: a second MigrateUp() returns nil —
//	   every ADD COLUMN is IF NOT EXISTS, every constraint
//	   add is paired with DROP IF EXISTS, every index add
//	   is paired with DROP INDEX IF EXISTS (PR #377 /
//	   ADR-041 contract).
//	7. Down symmetry: the down body drops the index + CHECK
//	   + the three columns cleanly; the re-applied up body
//	   round-trips.
//
// Build tag mirrors apply_walk_test.go:4 and the other
// 0008x/0009x migration tests — set FAAS_SKIP_PG_TESTS=1
// to skip.

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigration_00103_1_ColumnShape pins the schema of the
// three new instances columns after 00103 applies. A regression
// (e.g. tightening NOT NULL on migrated_from_node_id, or
// typing lease_token as integer) fails loud here — the engine
// relies on all three being nullable at insert time (a fresh
// instance has no migration history yet).
func TestMigration_00103_1_ColumnShape(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	cases := []struct {
		column string
		want   string // expected is_nullable string
	}{
		{"migrated_from_node_id", "YES"},
		{"migrated_at", "YES"},
		{"lease_token", "YES"},
	}
	for _, c := range cases {
		var nullable string
		err := pool.QueryRow(ctx, `
			select is_nullable
			  from information_schema.columns
			 where table_schema = current_schema()
			   and table_name   = 'instances'
			   and column_name  = $1
		`, c.column).Scan(&nullable)
		if err != nil {
			t.Fatalf("query instances.%s is_nullable: %v", c.column, err)
		}
		if nullable != c.want {
			t.Errorf("instances.%s is_nullable = %q, want %q",
				c.column, nullable, c.want)
		}
	}

	// Spot-check migrated_at's data_type. timestamptz, not text.
	var dtype string
	if err := pool.QueryRow(ctx, `
		select data_type from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'instances'
		   and column_name  = 'migrated_at'
	`).Scan(&dtype); err != nil {
		t.Fatalf("query migrated_at data_type: %v", err)
	}
	if dtype != "timestamp with time zone" {
		t.Errorf("instances.migrated_at data_type = %q, want %q",
			dtype, "timestamp with time zone")
	}
}

// TestMigration_00103_2_AllowsNull pins the never-migrated
// case. INSERT an instance row with all three new columns
// NULL; SELECT it back; assert NULL round-trips. The engine's
// hot path (Phase 1 of the four-phase handoff) writes the
// columns only after a successful snapshot; a fresh instance
// must stay NULLable so the cold INSERT path is unchanged.
func TestMigration_00103_2_AllowsNull(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Seed parent rows. account + app + deployment + node +
	// instance with the three new columns NULL.
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
	`, nodeID, "live-mig-null-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, created_at)
		values ($1, $2, $3, 'function', 256, 2, 60, 'active',
		        $4, now())
	`, appID, accountID, "live-mig-null-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	depID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, image_digest, status, created_at)
		values ($1, $2, 'image', 'sha256:seed-live-mig-null', 'live', now())
	`, depID, appID); err != nil {
		t.Fatalf("seed deployments: %v", err)
	}

	instanceID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into instances (id, app_id, deployment_id, state, ram_mb,
		                       node_id, migrated_from_node_id,
		                       migrated_at, lease_token)
		values ($1, $2, $3, 'running', 256, $4, NULL, NULL, NULL)
	`, instanceID, appID, depID, nodeID); err != nil {
		t.Fatalf("insert instance with NULL migration cols: %v", err)
	}

	// Round-trip: all three NULL.
	var (
		migNode *string
		migAt   *time.Time
		lease   *string
	)
	if err := pool.QueryRow(ctx, `
		select migrated_from_node_id, migrated_at, lease_token
		  from instances where id = $1
	`, instanceID).Scan(&migNode, &migAt, &lease); err != nil {
		t.Fatalf("select migration cols: %v", err)
	}
	if migNode != nil {
		t.Errorf("migrated_from_node_id round-tripped to %v, want NULL", *migNode)
	}
	if migAt != nil {
		t.Errorf("migrated_at round-tripped to %v, want NULL", *migAt)
	}
	if lease != nil {
		t.Errorf("lease_token round-tripped to %v, want NULL", *lease)
	}
}

// TestMigration_00103_3_AllowsPastTimestamp pins the normal
// post-migration case. UPDATE an instance's migrated_at to
// now() - 1 hour; the row must round-trip. The CHECK tolerates
// any timestamp in the past — no upper bound is enforced except
// the clock-skew window.
func TestMigration_00103_3_AllowsPastTimestamp(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Seed parent rows (same shape as the NULL test, but
	// also need a target node for migrated_from_node_id).
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
	`, nodeID, "live-mig-past-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	fromNodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50052', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, fromNodeID, "live-mig-past-from-"+fromNodeID[:8]); err != nil {
		t.Fatalf("seed from-node: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, created_at)
		values ($1, $2, $3, 'function', 256, 2, 60, 'active',
		        $4, now())
	`, appID, accountID, "live-mig-past-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	depID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, image_digest, status, created_at)
		values ($1, $2, 'image', 'sha256:seed-live-mig-past', 'live', now())
	`, depID, appID); err != nil {
		t.Fatalf("seed deployments: %v", err)
	}

	instanceID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into instances (id, app_id, deployment_id, state, ram_mb,
		                       node_id, migrated_from_node_id,
		                       migrated_at, lease_token)
		values ($1, $2, $3, 'running', 256, $4, $5,
		        now() - interval '1 hour', 'lease-token-past')
	`, instanceID, appID, depID, nodeID, fromNodeID); err != nil {
		t.Fatalf("insert instance with past migrated_at: %v", err)
	}

	var (
		gotFrom  string
		gotAt    time.Time
		gotLease string
	)
	if err := pool.QueryRow(ctx, `
		select migrated_from_node_id, migrated_at, lease_token
		  from instances where id = $1
	`, instanceID).Scan(&gotFrom, &gotAt, &gotLease); err != nil {
		t.Fatalf("select migration cols: %v", err)
	}
	if gotFrom != fromNodeID {
		t.Errorf("migrated_from_node_id = %q, want %q", gotFrom, fromNodeID)
	}
	if gotAt.IsZero() {
		t.Errorf("migrated_at round-tripped to zero value, want a recent past timestamp")
	}
	if gotLease != "lease-token-past" {
		t.Errorf("lease_token = %q, want %q", gotLease, "lease-token-past")
	}
}

// TestMigration_00103_4_RejectsFutureTimestamp pins the
// clock-skew guard. INSERT an instance with
// migrated_at = now() + 1 hour (clearly past the CHECK's
// +1 minute tolerance); the row must fail 23514. The CHECK
// is the tripwire for a misconfigured clock or a buggy write
// path that would otherwise pin an instance's lineage far in
// the future.
func TestMigration_00103_4_RejectsFutureTimestamp(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Seed minimal parents — accounts + app + node + dep, all
	// already established at this point.
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
	`, nodeID, "live-mig-future-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}
	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, created_at)
		values ($1, $2, $3, 'function', 256, 2, 60, 'active',
		        $4, now())
	`, appID, accountID, "live-mig-future-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	depID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, image_digest, status, created_at)
		values ($1, $2, 'image', 'sha256:seed-live-mig-future', 'live', now())
	`, depID, appID); err != nil {
		t.Fatalf("seed deployments: %v", err)
	}

	_, err := pool.Exec(ctx, `
		insert into instances (id, app_id, deployment_id, state, ram_mb,
		                       node_id, migrated_from_node_id,
		                       migrated_at, lease_token)
		values ($1, $2, $3, 'running', 256, $4, NULL,
		        now() + interval '1 hour', NULL)
	`, uuid.NewString(), appID, depID, nodeID)
	if err == nil {
		t.Fatal("expected check violation on migrated_at = now() + 1h; got nil (CHECK is missing or not enforced)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("want SQLSTATE 23514 on future migrated_at, got %v", err)
	}
}

// TestMigration_00103_5_FKOnDelete pins the ON DELETE SET NULL
// behavior. Insert an instance pointing at a compute_node;
// delete the compute_node; the instance row stays but its
// migrated_from_node_id flips to NULL. The alternative —
// CASCADE — would silently delete every instance row whose
// node was ever decommissioned, which is unacceptable for a
// historical lineage column.
func TestMigration_00103_5_FKOnDelete(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	accountID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ($1, $2, 'free', now())
	`, accountID, accountID[:8]+"@test.example"); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}

	// Two nodes: the current owner + the legacy "from" node
	// that we'll delete.
	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, nodeID, "live-mig-fk-keep-"+nodeID[:8]); err != nil {
		t.Fatalf("seed keep-node: %v", err)
	}
	fromNodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50052', 160, 56000, 200, 47600, 'unavailable'::compute_node_lifecycle)
	`, fromNodeID, "live-mig-fk-drop-"+fromNodeID[:8]); err != nil {
		t.Fatalf("seed from-node: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, created_at)
		values ($1, $2, $3, 'function', 256, 2, 60, 'active',
		        $4, now())
	`, appID, accountID, "live-mig-fk-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	depID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, image_digest, status, created_at)
		values ($1, $2, 'image', 'sha256:seed-live-mig-fk', 'live', now())
	`, depID, appID); err != nil {
		t.Fatalf("seed deployments: %v", err)
	}
	instanceID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into instances (id, app_id, deployment_id, state, ram_mb,
		                       node_id, migrated_from_node_id,
		                       migrated_at, lease_token)
		values ($1, $2, $3, 'running', 256, $4, $5,
		        now(), 'lease-token-fk')
	`, instanceID, appID, depID, nodeID, fromNodeID); err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	// Delete the from-node. SET NULL must fire, NOT CASCADE.
	if _, err := pool.Exec(ctx,
		`delete from compute_nodes where id = $1`, fromNodeID); err != nil {
		t.Fatalf("delete from-node: %v", err)
	}

	// Probe: instance row still present, migrated_from_node_id NULL.
	var (
		gotIns   string
		gotFrom  *string
		gotLease *string
	)
	if err := pool.QueryRow(ctx, `
		select id, migrated_from_node_id, lease_token
		  from instances where id = $1
	`, instanceID).Scan(&gotIns, &gotFrom, &gotLease); err != nil {
		t.Fatalf("select after FK delete: %v", err)
	}
	if gotIns != instanceID {
		t.Errorf("instance row id = %q, want %q (FK CASCADE would have deleted the row)",
			gotIns, instanceID)
	}
	if gotFrom != nil {
		t.Errorf("migrated_from_node_id = %v after FK delete, want NULL (FK must be SET NULL, not CASCADE)",
			*gotFrom)
	}
	if gotLease == nil || *gotLease != "lease-token-fk" {
		t.Errorf("lease_token lost after FK delete = %v, want %q", gotLease, "lease-token-fk")
	}
}

// TestMigration_00103_6_PartialIndex pins the partial-index
// shape. instances_migrated_from_node_id_idx must exist as a
// btree over (migrated_from_node_id) with a WHERE
// migrated_from_node_id IS NOT NULL predicate. The "NOT NULL"
// predicate is load-bearing: a NULL instance never appears in
// the index (the dashboard's "fleet migrated-from" panel scans
// non-NULL rows only). Drop the predicate and the index
// would balloon to every instance row.
func TestMigration_00103_6_PartialIndex(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Index presence.
	var idxCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'instances'
		   and indexname  = 'instances_migrated_from_node_id_idx'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("query instances_migrated_from_node_id_idx: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("instances_migrated_from_node_id_idx present = %d, want 1", idxCount)
	}

	// Partial-index predicate: pg_get_indexdef must contain
	// `WHERE ((migrated_from_node_id IS NOT NULL))`. Match
	// on substring to tolerate Postgres-version formatting
	// drift (mirrors the 00095 partial-index test).
	var idxDef string
	if err := pool.QueryRow(ctx, `
		select pg_get_indexdef(indexrelid)
		  from pg_index
		 join pg_class c on c.oid = indexrelid
		 where c.relname = 'instances_migrated_from_node_id_idx'
		   and c.relnamespace = (select oid from pg_namespace
		                          where nspname = current_schema())
	`).Scan(&idxDef); err != nil {
		t.Fatalf("query pg_get_indexdef: %v", err)
	}
	if !strings.Contains(idxDef, "WHERE") ||
		!strings.Contains(idxDef, "migrated_from_node_id") ||
		!strings.Contains(idxDef, "IS NOT NULL") {
		t.Errorf("instances_migrated_from_node_id_idx predicate missing: %q", idxDef)
	}
}

// TestMigration_00103_7_ReplaySafe pins the idempotency
// contract. A second MigrateUp() returns nil — every ADD
// COLUMN is IF NOT EXISTS, every constraint add is paired
// with DROP IF EXISTS, every index add is paired with DROP
// INDEX IF EXISTS (PR #377 / ADR-041).
func TestMigration_00103_7_ReplaySafe(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay MigrateUp: %v (every ADD COLUMN must be IF NOT EXISTS, every constraint add paired with DROP IF EXISTS, every index add paired with DROP INDEX IF EXISTS)", err)
	}
}

// TestMigration_00103_8_DownSymmetry pins the down path.
// Drive the SQL the down body carries directly, then re-
// apply the up body and assert the columns + CHECK + index
// come back. A non-symmetric down would leave a broken
// schema on a release that needs to roll back 00103 in
// isolation.
func TestMigration_00103_8_DownSymmetry(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	// Down body — drop in the reverse order of creation.
	// (The actual migration uses ALTER TABLE ... DROP
	// COLUMN IF EXISTS for each column; the test mirrors the
	// down body exactly so a drift between the file and the
	// test surfaces immediately.)
	if _, err := pool.Exec(ctx,
		`drop index if exists instances_migrated_from_node_id_idx`); err != nil {
		t.Fatalf("down: drop index: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`alter table instances drop constraint if exists instances_migrated_at_chk`); err != nil {
		t.Fatalf("down: drop chk: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`alter table instances drop column if exists lease_token`); err != nil {
		t.Fatalf("down: drop lease_token: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`alter table instances drop column if exists migrated_at`); err != nil {
		t.Fatalf("down: drop migrated_at: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`alter table instances drop column if exists migrated_from_node_id`); err != nil {
		t.Fatalf("down: drop migrated_from_node_id: %v", err)
	}

	// Probe: all three columns gone.
	var count int
	for _, col := range []string{
		"migrated_from_node_id", "migrated_at", "lease_token",
	} {
		if err := pool.QueryRow(ctx, `
			select count(*) from information_schema.columns
			 where table_schema = current_schema()
			   and table_name   = 'instances'
			   and column_name  = $1
		`, col).Scan(&count); err != nil {
			t.Fatalf("probe %s absence: %v", col, err)
		}
		if count != 0 {
			t.Errorf("after down, instances.%s still present (count=%d)", col, count)
		}
	}

	// Probe: index gone.
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'instances'
		   and indexname  = 'instances_migrated_from_node_id_idx'
	`).Scan(&count); err != nil {
		t.Fatalf("probe instances_migrated_from_node_id_idx absence: %v", err)
	}
	if count != 0 {
		t.Errorf("after down, instances_migrated_from_node_id_idx still present (count=%d)", count)
	}

	// Re-apply the up body — must succeed cleanly.
	if _, err := pool.Exec(ctx, `
		alter table instances
		  add column if not exists migrated_from_node_id uuid
		    references compute_nodes(id) on delete set null,
		  add column if not exists migrated_at timestamptz,
		  add column if not exists lease_token text,
		  add constraint instances_migrated_at_chk
		    check (migrated_at is null
		           or migrated_at <= now() + interval '1 minute')
	`); err != nil {
		t.Fatalf("re-add columns: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		create index if not exists instances_migrated_from_node_id_idx
		  on instances (migrated_from_node_id)
		  where migrated_from_node_id is not null
	`); err != nil {
		t.Fatalf("re-add index: %v", err)
	}

	// Probe: migrated_from_node_id back.
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'instances'
		   and column_name  = 'migrated_from_node_id'
	`).Scan(&count); err != nil {
		t.Fatalf("probe migrated_from_node_id re-added: %v", err)
	}
	if count != 1 {
		t.Errorf("after re-add, instances.migrated_from_node_id present = %d, want 1", count)
	}
}
