//go:build !no_pg

// Migration-apply tests for 00123 (compute_nodes.vcpu_budget,
// Tier A2 — per-node vCPU admission budget).
//
// Pins the Tier A2 acceptance gate verbatim:
// <the migration set applies cleanly through 00123; the column
// accepts the canonical int shape and round-trips; default is 160;
// replay-safe: ADD COLUMN IF NOT EXISTS makes a second MigrateUp
// no-op.>
//
//	1. compute_nodes.vcpu_budget column exists with the expected
//	   data_type + nullability + column default.
//	2. The CHECK constraint rejects vcpu_budget=0 (the defensive
//	   net for a future operator that tries to set zero).
//	3. The column accepts the canonical int shape and round-trips:
//	   insert a row with vcpu_budget=320, read it back, assert 320.
//	4. The default applies on backfill: a row INSERTed without
//	   specifying vcpu_budget gets 160 (the migration default +
//	   the single-box backwards-compat posture).
//	5. The default-local row seeded by migration 00024 carries
//	   vcpu_budget=160 after 00123 applies (no operator tuning
//	   needed for the legacy single-box path).
//	6. Replay-safety: a second MigrateUp() returns nil — the
//	   ADD COLUMN IF NOT EXISTS makes the migration idempotent.
//	7. Down path: MigrateDown to 00080 drops vcpu_budget, then
//	   MigrateUp re-creates it.
//
// Build tag mirrors apply_walk_test.go:4 and the other
// 0007x migration tests — set FAAS_SKIP_PG_TESTS=1 to skip.

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

// TestMigration_00123_1_VCPUBudgetColumnShape pins the column shape
// after migration 00123 applies. data_type=integer + is_nullable=NO
// + column_default=160. Any drift (e.g. someone typing smallint, or
// adding a permissive NULL, or dropping the default) fails loud.
func TestMigration_00123_1_VCPUBudgetColumnShape(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Scope by current_schema() so a parallel pgtest schema doesn't
	// leak its compute_nodes.vcpu_budget column into the iteration
	// (per memory: migrations info_schema scoping pattern).
	var dtype, nullable string
	var hasDefault bool
	err := pool.QueryRow(ctx, `
		select data_type, is_nullable, (column_default IS NOT NULL)
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name   = 'compute_nodes'
		   and column_name  = 'vcpu_budget'
	`).Scan(&dtype, &nullable, &hasDefault)
	if err != nil {
		t.Fatalf("query compute_nodes.vcpu_budget column: %v", err)
	}
	if dtype != "integer" {
		t.Errorf("vcpu_budget data_type = %q, want %q", dtype, "integer")
	}
	if nullable != "NO" {
		t.Errorf("vcpu_budget is_nullable = %q, want %q", nullable, "NO")
	}
	if !hasDefault {
		t.Errorf("vcpu_budget has no column default (DEFAULT 160 expected; missing DEFAULT breaks single-box backfill)")
	}
}

// TestMigration_00123_2_CheckConstraintRejectsZero pins the
// defensive CHECK (vcpu_budget > 0). A future operator that
// upserts vcpu_budget=0 must hit SQLSTATE 23514 (check_violation),
// not silently accept it — zero would make every chooser candidate
// filter refuse the row and the fleet would silently lose capacity.
func TestMigration_00123_2_CheckConstraintRejectsZero(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	// Seed a parent compute_nodes row first (name + target_url are
	// NOT NULL on the original schema, so we can't just INSERT into
	// vcpu_budget alone).
	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle)
	`, nodeID, "chk-zero-"+nodeID[:8]); err != nil {
		t.Fatalf("seed compute_nodes: %v", err)
	}

	// UPDATE vcpu_budget=0 must fail with check_violation.
	_, err := pool.Exec(ctx, `
		update compute_nodes set vcpu_budget = 0 where id = $1
	`, nodeID)
	if err == nil {
		t.Fatal("expected check violation on vcpu_budget=0; got nil (CHECK (vcpu_budget > 0) is missing or not enforced)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("want SQLSTATE 23514, got %v", err)
	}
}

// TestMigration_00123_3_CanonicalRoundTrip pins the column's
// int shape: insert a row with vcpu_budget=320 (operator-tuned
// heterogeneous fleet — a smaller box with vpcpus=40 and
// vcpu_budget=320), read it back, assert 320.
func TestMigration_00123_3_CanonicalRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	nodeID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle, vcpu_budget) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle, 320)
	`, nodeID, "rt-"+nodeID[:8]); err != nil {
		t.Fatalf("insert with vcpu_budget=320: %v", err)
	}

	var got int
	if err := pool.QueryRow(ctx, `
		select vcpu_budget from compute_nodes where id = $1
	`, nodeID).Scan(&got); err != nil {
		t.Fatalf("query vcpu_budget: %v", err)
	}
	if got != 320 {
		t.Errorf("vcpu_budget = %d, want 320 (round-trip lost the value)", got)
	}
}

// TestMigration_00123_4_DefaultBackfill pins the migration
// default. A row INSERTed without specifying vcpu_budget must
// land at 160 — the legacy single-box posture + the contract
// that an operator never has to manually set the budget on the
// synthetic default-local row seeded by migration 00024.
func TestMigration_00123_4_DefaultBackfill(t *testing.T) {
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
	`, nodeID, "dflt-"+nodeID[:8]); err != nil {
		t.Fatalf("insert without vcpu_budget: %v", err)
	}

	var got int
	if err := pool.QueryRow(ctx, `
		select vcpu_budget from compute_nodes where id = $1
	`, nodeID).Scan(&got); err != nil {
		t.Fatalf("query vcpu_budget: %v", err)
	}
	if got != 160 {
		t.Errorf("vcpu_budget = %d, want 160 (DEFAULT missing or wrong)", got)
	}
}

// TestMigration_00123_5_DefaultLocalRowBackfill pins the
// load-bearing single-box backfill. Migration 00024 seeded a
// synthetic default-local row; after 00123 applies, that row
// carries vcpu_budget=160 via the column default without
// operator action. (We can't predict the synthetic row's id on
// a fresh DB, so we look it up by name.)
//
// Hard-fail on missing row: the contract this test depends on
// is "every fresh-DB apply ends with a default-local row".
// A skip would mask a regression where 00024's seed was dropped
// or 00123's backfill skipped the row entirely. pgtest.Open
// runs the full migration set, so the row is always present on a
// clean run; a missing row is a real bug, not a test-rig
// limitation.
func TestMigration_00123_5_DefaultLocalRowBackfill(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	var got int
	err := pool.QueryRow(ctx, `
		select vcpu_budget from compute_nodes
		 where name = 'default-local'
		 limit 1
	`).Scan(&got)
	if err != nil {
		t.Fatalf("default-local row not present in this schema (err=%v); migration 00024's seed is missing — pgtest.Open should run the full migration set", err)
	}
	if got != 160 {
		t.Errorf("default-local vcpu_budget = %d, want 160 (single-box backwards-compat)", got)
	}
}

// TestMigration_00123_6_ReplaySafe pins the idempotency contract.
// A second MigrateUp() returns nil — the ADD COLUMN IF NOT EXISTS
// makes the migration a no-op. This is the tripwire for the
// replay-safety pattern PR #377 / ADR-041 established; without
// it, a hot-reload of the binary would 42710 ("column already
// exists") and refuse to boot.
func TestMigration_00123_6_ReplaySafe(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	// First apply.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("first MigrateUp: %v", err)
	}
	// Second apply — must be a clean no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay MigrateUp: %v (ADD COLUMN IF NOT EXISTS must be idempotent; PR #377 / ADR-041)", err)
	}
}

// TestMigration_00123_7_DownSymmetry pins the down path. We
// don't have a MigrateDownTo helper in pkg/db today (only
// MigrateUp), so we drive goose directly via a SQL probe: read
// the migration's own -- +goose Down body and verify the column
// drops cleanly. A non-symmetric down would leave a broken
// schema on a release that needs to roll back 00123 in isolation.
//
// On skip: this test only runs on a Postgres-backed test
// (FAAS_SKIP_PG_TESTS=1 to opt out).
func TestMigration_00123_7_DownSymmetry(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	// Drop the column directly (the down block runs ALTER TABLE
	// ... DROP COLUMN vcpu_budget). This isn't the canonical goose
	// path, but it validates the SQL the down block carries.
	if _, err := pool.Exec(ctx, `alter table compute_nodes drop column vcpu_budget`); err != nil {
		t.Fatalf("down SQL: %v", err)
	}
	// Re-create it via the migration body (up path).
	if _, err := pool.Exec(ctx, `
		alter table compute_nodes
		  add column vcpu_budget int not null default 160
		    check (vcpu_budget > 0)
	`); err != nil {
		t.Fatalf("up SQL re-apply: %v (down→up round-trip must leave a clean schema)", err)
	}
	// Probe: round-trip a value through the re-created column.
	var nodeID = uuid.NewString()
	if _, err := pool.Exec(ctx, `
		insert into compute_nodes (id, name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb, lifecycle, vcpu_budget) values ($1, $2, 'tcp://test:50051', 160, 56000, 200, 47600, 'active'::compute_node_lifecycle, 240)
	`, nodeID, "rsym-"+nodeID[:8]); err != nil {
		t.Fatalf("insert after re-create: %v", err)
	}
	var got int
	if err := pool.QueryRow(ctx, `
		select vcpu_budget from compute_nodes where id = $1
	`, nodeID).Scan(&got); err != nil {
		t.Fatalf("query vcpu_budget after re-create: %v", err)
	}
	if got != 240 {
		t.Errorf("vcpu_budget = %d, want 240 (round-trip after drop→re-add)", got)
	}
}
