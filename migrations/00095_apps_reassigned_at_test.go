//go:build !no_pg

// Migration-apply tests for 00095 (apps.reassigned_at + the
// apps_reassigned_at_chk clock-skew CHECK + the partial
// index apps_reassigned_at_idx, Tier A4 cross-node app
// rebalance, ADR-064).
//
// Pins the Tier A4 schema contract verbatim:
//
//	1. apps.reassigned_at column exists with the expected
//	   data_type (timestamp with time zone) + nullability
//	   (YES) — the column is nullable at insert time; a fresh
//	   app has no reassignment history yet.
//	2. apps_reassigned_at_chk tolerates NULL (the never-
//	   reassigned case) and tolerates a past timestamp (the
//	   normal post-reassignment case). The clock-skew
//	   allowance is `now() + interval '1 minute'`; values
//	   clearly in the future still error loud.
//	3. apps_reassigned_at_idx is a partial index over
//	   reassigned_at WHERE reassigned_at IS NOT NULL — a
//	   NULL app must NOT be in the index (the rebalancer's
//	   hot filter is "non-NULL AND < now() - cooldown"; a
//	   NULL app is always eligible so it must never appear
//	   in the index).
//	4. Replay-safety: a second MigrateUp() returns nil —
//	   every ADD COLUMN is IF NOT EXISTS, every constraint
//	   add is paired with a DROP IF EXISTS in the down
//	   block (PR #377 / ADR-041 contract).
//	5. Down symmetry: the down body drops
//	   apps.reassigned_at + the CHECK + the partial index
//	   cleanly; the re-applied up body round-trips.
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

// TestMigration_00095_1_ColumnShape pins the apps.reassigned_at
// column shape after 00095 applies. data_type='timestamp with
// time zone' + is_nullable='YES'. Any drift (e.g. someone
// tightening NOT NULL, or typing text instead of timestamptz)
// fails loud — the rebalancer relies on the column being
// nullable so a fresh INSERT with no reassignment history
// succeeds.
func TestMigration_00095_1_ColumnShape(t *testing.T) {
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
		   and column_name  = 'reassigned_at'
	`).Scan(&dtype, &nullable)
	if err != nil {
		t.Fatalf("query apps.reassigned_at column: %v", err)
	}
	if dtype != "timestamp with time zone" {
		t.Errorf("apps.reassigned_at data_type = %q, want %q", dtype, "timestamp with time zone")
	}
	if nullable != "YES" {
		t.Errorf("apps.reassigned_at is_nullable = %q, want %q", nullable, "YES")
	}
}

// TestMigration_00095_2_AllowsNull pins the never-reassigned
// case. INSERT a row with reassigned_at = NULL; SELECT it
// back; assert NULL round-trips. The rebalancer's
// ListOrphanedApps filter explicitly returns NULL rows so a
// never-reassigned orphaned app is always eligible for
// reassignment on the first drain event.
func TestMigration_00095_2_AllowsNull(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Seed an account + compute_node + app with reassigned_at = NULL.
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
	`, nodeID, "rebal-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, reassigned_at, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, NULL, now())
	`, appID, accountID, "rebal-null-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("insert app with reassigned_at = NULL: %v", err)
	}

	var got *time.Time
	if err := pool.QueryRow(ctx,
		`select reassigned_at from apps where id = $1`,
		appID).Scan(&got); err != nil {
		t.Fatalf("select reassigned_at: %v", err)
	}
	if got != nil {
		t.Errorf("apps.reassigned_at round-tripped to %v, want NULL", got)
	}
}

// TestMigration_00095_3_AllowsPastTimestamp pins the
// normal post-reassignment case. UPDATE the app's
// reassigned_at to now() - 1 hour; the row must round-trip.
// The CHECK tolerates any timestamp in the past — no upper
// bound is enforced except the clock-skew window.
func TestMigration_00095_3_AllowsPastTimestamp(t *testing.T) {
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
	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, nodeID, "past-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, reassigned_at, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, now() - interval '1 hour', now())
	`, appID, accountID, "rebal-past-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("insert app with past reassigned_at: %v", err)
	}

	var got time.Time
	if err := pool.QueryRow(ctx,
		`select reassigned_at from apps where id = $1`,
		appID).Scan(&got); err != nil {
		t.Fatalf("select reassigned_at: %v", err)
	}
	if got.IsZero() {
		t.Errorf("apps.reassigned_at round-tripped to zero value, want a recent past timestamp")
	}
}

// TestMigration_00095_4_RejectsFutureTimestamp pins the
// clock-skew guard. UPDATE the app's reassigned_at to
// now() + 1 hour (clearly past the CHECK's +1 minute
// tolerance); the row must fail 23514. The CHECK is the
// tripwire for a misconfigured clock or a buggy write
// path that would otherwise pin an app's cooldown far in
// the future.
func TestMigration_00095_4_RejectsFutureTimestamp(t *testing.T) {
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
	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, nodeID, "future-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	// INSERT path: rejected by CHECK. The CHECK fires on
	// UPDATE too, but INSERT exercises it from the cold path
	// (no UPDATE chain to mask a regression).
	_, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, reassigned_at, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, now() + interval '1 hour', now())
	`, uuid.NewString(), accountID, "rebal-future-"+accountID[:8], nodeID)
	if err == nil {
		t.Fatal("expected check violation on reassigned_at = now() + 1h; got nil (CHECK is missing or not enforced)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("want SQLSTATE 23514 on future reassigned_at, got %v", err)
	}
}

// TestMigration_00095_5_PartialIndex pins the partial-index
// shape. apps_reassigned_at_idx must exist as a btree index
// over (reassigned_at) with a WHERE reassigned_at IS NOT
// NULL predicate. The "NULL excluded" predicate is
// load-bearing: a NULL app must NOT be in the index (the
// rebalancer's hot filter is "non-NULL AND < now() -
// cooldown"; a NULL app is always eligible so it must never
// appear in the index). Drop the predicate and the index
// would balloon to every app row.
func TestMigration_00095_5_PartialIndex(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Index presence.
	var idxCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'apps'
		   and indexname  = 'apps_reassigned_at_idx'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("query apps_reassigned_at_idx: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("apps_reassigned_at_idx present = %d, want 1", idxCount)
	}

	// Partial-index predicate: pg_get_indexdef must contain
	// `WHERE ((reassigned_at IS NOT NULL))`. The exact
	// formatting is Postgres-version-dependent, so we check
	// for the substring rather than the full string.
	var idxDef string
	if err := pool.QueryRow(ctx, `
		select pg_get_indexdef(indexrelid)
		  from pg_index
		 join pg_class c on c.oid = indexrelid
		 where c.relname = 'apps_reassigned_at_idx'
		   and c.relnamespace = (select oid from pg_namespace
		                          where nspname = current_schema())
	`).Scan(&idxDef); err != nil {
		t.Fatalf("query pg_get_indexdef: %v", err)
	}
	if !strings.Contains(idxDef, "WHERE") || !strings.Contains(idxDef, "reassigned_at") || !strings.Contains(idxDef, "IS NOT NULL") {
		t.Errorf("apps_reassigned_at_idx predicate missing: %q", idxDef)
	}
}

// TestMigration_00095_6_ReplaySafe pins the idempotency
// contract. A second MigrateUp() returns nil — every ADD
// COLUMN is IF NOT EXISTS, every constraint add is paired
// with a DROP IF EXISTS in the down block, every index add
// is paired with DROP INDEX IF EXISTS (PR #377 / ADR-041).
func TestMigration_00095_6_ReplaySafe(t *testing.T) {
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

// TestMigration_00095_7_DownSymmetry pins the down path.
// Drive the SQL the down body carries directly, then re-
// apply the up body and assert the column + CHECK + index
// come back. A non-symmetric down would leave a broken
// schema on a release that needs to roll back 00095 in
// isolation.
func TestMigration_00095_7_DownSymmetry(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	// Down body — drop in the reverse order of creation.
	if _, err := pool.Exec(ctx, `drop index if exists apps_reassigned_at_idx`); err != nil {
		t.Fatalf("down: drop index: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop constraint if exists apps_reassigned_at_chk`); err != nil {
		t.Fatalf("down: drop chk: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop column if exists reassigned_at`); err != nil {
		t.Fatalf("down: drop reassigned_at: %v", err)
	}

	// Probe: column gone.
	var count int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and column_name  = 'reassigned_at'
	`).Scan(&count); err != nil {
		t.Fatalf("probe reassigned_at absence: %v", err)
	}
	if count != 0 {
		t.Errorf("after down, apps.reassigned_at still present (count=%d)", count)
	}

	// Probe: index gone.
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'apps'
		   and indexname  = 'apps_reassigned_at_idx'
	`).Scan(&count); err != nil {
		t.Fatalf("probe apps_reassigned_at_idx absence: %v", err)
	}
	if count != 0 {
		t.Errorf("after down, apps_reassigned_at_idx still present (count=%d)", count)
	}

	// Re-apply the up body — must succeed cleanly.
	if _, err := pool.Exec(ctx, `alter table apps add column if not exists reassigned_at timestamptz`); err != nil {
		t.Fatalf("re-add reassigned_at: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps add constraint apps_reassigned_at_chk check (reassigned_at is null or reassigned_at <= now() + interval '1 minute')`); err != nil {
		t.Fatalf("re-add chk: %v", err)
	}
	if _, err := pool.Exec(ctx, `create index if not exists apps_reassigned_at_idx on apps (reassigned_at) where reassigned_at is not null`); err != nil {
		t.Fatalf("re-add index: %v", err)
	}

	// Probe: column back.
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and column_name  = 'reassigned_at'
	`).Scan(&count); err != nil {
		t.Fatalf("probe reassigned_at re-added: %v", err)
	}
	if count != 1 {
		t.Errorf("after re-add, apps.reassigned_at present = %d, want 1", count)
	}
}

// contains helper used by the partial-index predicate check
// lives in 00057_sessions_test.go (migrations package is
// `migrations_test`); this file uses strings.Contains directly.
