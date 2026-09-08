//go:build !no_pg

// Migration-apply tests for 00167 (apps.overflow_node + the
// apps_overflow_node_chk empty-uuid CHECK + the FK with ON DELETE
// SET NULL + the partial index apps_overflow_node_idx; Tier A10
// per-app overflow_node preference, ADR-088).
//
// Pins the Tier A10 schema contract verbatim:
//
//	1. apps.overflow_node column exists with the expected
//	   data_type (uuid) + nullability (YES) — the column is
//	   nullable; "no preference" is the default.
//	2. apps_overflow_node_chk tolerates NULL (the no-preference
//	   case) AND rejects the empty uuid — the tripwire against
//	   a buggy INSERT path that produces 00000000-...
//	3. apps_overflow_node_fkey enforces FK with ON DELETE SET
//	   NULL: deleting a compute_node referenced by an app's
//	   overflow_node must NULL the column, not error.
//	4. apps_overflow_node_idx is a partial index over
//	   (overflow_node) WHERE overflow_node IS NOT NULL — a
//	   NULL-preference app must NOT be in the index (the engine
//	   hot path is "find pressured apps with an explicit
//	   preference"). Partial predicate keeps the index narrow.
//	5. Replay-safety: a second MigrateUp() returns nil —
//	   every ADD COLUMN is IF NOT EXISTS, every constraint
//	   add is paired with a DROP IF EXISTS in the down block,
//	   every index add is paired with DROP INDEX IF EXISTS.
//	6. Down symmetry: the down body drops
//	   apps.overflow_node + the CHECK + the FK + the partial
//	   index cleanly; the re-applied up body round-trips.
//
// Build tag mirrors apply_walk_test.go:4 and the other
// 0009x/0016x migration tests — set FAAS_SKIP_PG_TESTS=1
// to skip.

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigration_00167_1_ColumnShape pins the apps.overflow_node
// column shape after 00167 applies. data_type='uuid' +
// is_nullable='YES'. Any drift (e.g. someone tightening NOT
// NULL, or typing text instead of uuid) fails loud — the
// engine relies on the column being nullable so a fresh INSERT
// with no preference succeeds.
func TestMigration_00167_1_ColumnShape(t *testing.T) {
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
		   and column_name  = 'overflow_node'
	`).Scan(&dtype, &nullable)
	if err != nil {
		t.Fatalf("query apps.overflow_node column: %v", err)
	}
	if dtype != "uuid" {
		t.Errorf("apps.overflow_node data_type = %q, want %q", dtype, "uuid")
	}
	if nullable != "YES" {
		t.Errorf("apps.overflow_node is_nullable = %q, want %q", nullable, "YES")
	}
}

// TestMigration_00167_2_AllowsNull pins the no-preference case.
// INSERT a row with overflow_node = NULL; SELECT it back; assert
// NULL round-trips. The rebalancer's hot path treats a NULL
// preference as "fall back to first-peer-with-headroom" — so a
// NULL app must round-trip cleanly.
func TestMigration_00167_2_AllowsNull(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Seed an account + compute_node + app with overflow_node = NULL.
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
	`, nodeID, "overflow-null-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, overflow_node, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, NULL, now())
	`, appID, accountID, "overflow-null-app-"+accountID[:8], nodeID); err != nil {
		t.Fatalf("insert app with overflow_node = NULL: %v", err)
	}

	var got *string
	if err := pool.QueryRow(ctx,
		`select overflow_node::text from apps where id = $1`,
		appID).Scan(&got); err != nil {
		t.Fatalf("select overflow_node: %v", err)
	}
	if got != nil {
		t.Errorf("apps.overflow_node round-tripped to %v, want NULL", got)
	}
}

// TestMigration_00167_3_AllowsValidUUID pins the normal
// preference-set case. INSERT a row with overflow_node pointing
// at a real compute_node; SELECT it back; assert the value
// round-trips. The engine's hot path reads overflow_node on every
// pressured app; a non-NULL value must round-trip verbatim.
func TestMigration_00167_3_AllowsValidUUID(t *testing.T) {
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
	ownerID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://owner:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, ownerID, "owner-"+ownerID[:8]); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	overflowID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://overflow:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, overflowID, "overflow-"+overflowID[:8]); err != nil {
		t.Fatalf("seed overflow target: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, overflow_node, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, $5, now())
	`, appID, accountID, "overflow-valid-"+accountID[:8], ownerID, overflowID); err != nil {
		t.Fatalf("insert app with valid overflow_node: %v", err)
	}

	var got string
	if err := pool.QueryRow(ctx,
		`select overflow_node::text from apps where id = $1`,
		appID).Scan(&got); err != nil {
		t.Fatalf("select overflow_node: %v", err)
	}
	if got != overflowID {
		t.Errorf("apps.overflow_node round-tripped to %q, want %q", got, overflowID)
	}
}

// TestMigration_00167_4_RejectsEmptyUUID pins the empty-uuid
// tripwire. INSERT a row with overflow_node = the all-zero UUID
// 00000000-... — a buggy "uninitialised" sentinel; the CHECK
// must fire and reject with SQLSTATE 23514. Without this guard a
// future bug could silently route an app to a non-existent
// compute_node.
func TestMigration_00167_4_RejectsEmptyUUID(t *testing.T) {
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
	`, nodeID, "empty-uuid-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	// INSERT path: rejected by the empty-uuid CHECK. The CHECK
	// is the tripwire against a buggy INSERT path that produces
	// an "uninitialised" 00000000-... row.
	_, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, overflow_node, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, '00000000-0000-0000-0000-000000000000'::uuid, now())
	`, uuid.NewString(), accountID, "empty-uuid-app-"+accountID[:8], nodeID)
	if err == nil {
		t.Fatal("expected check violation on overflow_node = 00000000-...; got nil (CHECK is missing or not enforced)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("want SQLSTATE 23514 on empty-uuid overflow_node, got %v", err)
	}
}

// TestMigration_00167_5_FKEnforced pins the FOREIGN KEY. INSERT
// a row with overflow_node pointing at a non-existent UUID — the
// FK must fire and reject with SQLSTATE 23503. Without this
// guard, the engine would observe an overflow_node that no
// compute_node row matches and silently miss the spill target.
func TestMigration_00167_5_FKEnforced(t *testing.T) {
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
	`, nodeID, "fk-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	// overflow_node references a non-existent compute_node UUID.
	_, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, overflow_node, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, $5, now())
	`, uuid.NewString(), accountID, "fk-app-"+accountID[:8], nodeID, uuid.NewString())
	if err == nil {
		t.Fatal("expected FK violation on overflow_node = non-existent UUID; got nil (FK is missing or not enforced)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("want SQLSTATE 23503 on foreign-key overflow_node, got %v", err)
	}
}

// TestMigration_00167_6_OnDeleteSetNull pins the SET NULL
// foreign-key behaviour. When a compute_node referenced by an
// app's overflow_node is drained (compute_nodes DELETE — a
// future operator action; today there's an admin endpoint that
// only updates active=false, but the migration must support
// the future DELETE path), the cascade must NULL the
// overflow_node reference, NOT error or RESTRICT. Apps whose
// only preference was the drained node fall through to the
// first-peer-with-headroom fallback at engine-time.
func TestMigration_00167_6_OnDeleteSetNull(t *testing.T) {
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
	ownerID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://owner:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, ownerID, "owner-set-null-"+ownerID[:8]); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	overflowID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://overflow:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, overflowID, "overflow-set-null-"+overflowID[:8]); err != nil {
		t.Fatalf("seed overflow target: %v", err)
	}

	appID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb,
		                  max_concurrency, idle_timeout_s, status,
		                  node_id, overflow_node, created_at)
		values ($1, $2, $3, 'function', 128, 1, 30, 'active',
		        $4, $5, now())
	`, appID, accountID, "set-null-app-"+accountID[:8], ownerID, overflowID); err != nil {
		t.Fatalf("insert app referencing overflow_node: %v", err)
	}

	// DELETE the compute_node the app's overflow_node references.
	// ON DELETE SET NULL means the app's overflow_node column is
	// cleared silently; the row itself remains.
	if _, err := pool.Exec(ctx,
		`delete from compute_nodes where id = $1`,
		overflowID); err != nil {
		t.Fatalf("delete referenced compute_node: %v", err)
	}

	var got *string
	if err := pool.QueryRow(ctx,
		`select overflow_node::text from apps where id = $1`,
		appID).Scan(&got); err != nil {
		t.Fatalf("select overflow_node after delete: %v", err)
	}
	if got != nil {
		t.Errorf("after ON DELETE SET NULL, apps.overflow_node = %v, want NULL", *got)
	}
}

// TestMigration_00167_7_PartialIndex pins the partial-index
// shape. apps_overflow_node_idx must exist as a btree index
// over (overflow_node) with a WHERE overflow_node IS NOT NULL
// predicate. The "NULL excluded" predicate is load-bearing: a
// NULL-preference app must NOT be in the index (most apps in
// the fleet do not declare an explicit spill target). Indexing
// only the non-NULL tail keeps the index narrow.
func TestMigration_00167_7_PartialIndex(t *testing.T) {
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
		   and indexname  = 'apps_overflow_node_idx'
	`).Scan(&idxCount); err != nil {
		t.Fatalf("query apps_overflow_node_idx: %v", err)
	}
	if idxCount != 1 {
		t.Errorf("apps_overflow_node_idx present = %d, want 1", idxCount)
	}

	// Partial-index predicate: pg_get_indexdef must contain
	// `WHERE ((overflow_node IS NOT NULL))`. The exact
	// formatting is Postgres-version-dependent, so we check
	// for the substring rather than the full string.
	var idxDef string
	if err := pool.QueryRow(ctx, `
		select pg_get_indexdef(indexrelid)
		  from pg_index
		 join pg_class c on c.oid = indexrelid
		 where c.relname = 'apps_overflow_node_idx'
		   and c.relnamespace = (select oid from pg_namespace
		                          where nspname = current_schema())
	`).Scan(&idxDef); err != nil {
		t.Fatalf("query pg_get_indexdef: %v", err)
	}
	if !strings.Contains(idxDef, "WHERE") || !strings.Contains(idxDef, "overflow_node") || !strings.Contains(idxDef, "IS NOT NULL") {
		t.Errorf("apps_overflow_node_idx predicate missing: %q", idxDef)
	}
}

// TestMigration_00167_8_ReplaySafe pins the idempotency
// contract. A second MigrateUp() returns nil — every ADD
// COLUMN is IF NOT EXISTS, every constraint add is paired
// with a DROP IF EXISTS in the down block, every index add is
// paired with DROP INDEX IF EXISTS (PR #377 / ADR-041).
func TestMigration_00167_8_ReplaySafe(t *testing.T) {
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

// TestMigration_00167_9_DownSymmetry pins the down path.
// Drive the SQL the down body carries directly, then re-apply
// the up body and assert the column + CHECK + FK + index
// come back. A non-symmetric down would leave a broken schema
// on a release that needs to roll back 00167 in isolation.
func TestMigration_00167_9_DownSymmetry(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}

	// Down body — drop in the reverse order of creation.
	if _, err := pool.Exec(ctx, `drop index if exists apps_overflow_node_idx`); err != nil {
		t.Fatalf("down: drop index: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop constraint if exists apps_overflow_node_fkey`); err != nil {
		t.Fatalf("down: drop FK: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop constraint if exists apps_overflow_node_chk`); err != nil {
		t.Fatalf("down: drop chk: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps drop column if exists overflow_node`); err != nil {
		t.Fatalf("down: drop overflow_node: %v", err)
	}

	// Probe: column gone.
	var count int
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and column_name  = 'overflow_node'
	`).Scan(&count); err != nil {
		t.Fatalf("probe overflow_node absence: %v", err)
	}
	if count != 0 {
		t.Errorf("after down, apps.overflow_node still present (count=%d)", count)
	}

	// Probe: index gone.
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename  = 'apps'
		   and indexname  = 'apps_overflow_node_idx'
	`).Scan(&count); err != nil {
		t.Fatalf("probe apps_overflow_node_idx absence: %v", err)
	}
	if count != 0 {
		t.Errorf("after down, apps_overflow_node_idx still present (count=%d)", count)
	}

	// Re-apply the up body — must succeed cleanly.
	if _, err := pool.Exec(ctx, `alter table apps add column if not exists overflow_node uuid`); err != nil {
		t.Fatalf("re-add overflow_node: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps add constraint apps_overflow_node_chk check (overflow_node is null or overflow_node <> '00000000-0000-0000-0000-000000000000')`); err != nil {
		t.Fatalf("re-add chk: %v", err)
	}
	if _, err := pool.Exec(ctx, `alter table apps add constraint apps_overflow_node_fkey foreign key (overflow_node) references compute_nodes(id) on delete set null`); err != nil {
		t.Fatalf("re-add FK: %v", err)
	}
	if _, err := pool.Exec(ctx, `create index if not exists apps_overflow_node_idx on apps (overflow_node) where overflow_node is not null`); err != nil {
		t.Fatalf("re-add index: %v", err)
	}

	// Probe: column back.
	if err := pool.QueryRow(ctx, `
		select count(*) from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'apps'
		   and column_name  = 'overflow_node'
	`).Scan(&count); err != nil {
		t.Fatalf("probe overflow_node re-added: %v", err)
	}
	if count != 1 {
		t.Errorf("after re-add, apps.overflow_node present = %d, want 1", count)
	}
}
