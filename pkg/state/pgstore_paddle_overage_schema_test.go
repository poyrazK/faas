// pgstore_paddle_overage_schema_test.go — real-Postgres pins of the
// B4 pre-flight (PaddleOverageDedupeSchema). The companion memstore
// file (memstore_paddle_overage_schema_test.go) covers the
// in-process shape; this file covers the wire shape — what an
// apid process actually sees when it boots against the live
// control-plane Postgres.
//
// The CLI maps the probe output to operator-facing hints:
//   TableExists=false               → "apply 00034 then 00041"
//   TableExists=true, any HasX=false → "apply 00041"
//   everything true                  → ready
//
// A regression that flips either branch (e.g. a future migration
// that drops the 00041 columns, or a refactor that lets the probe
// short-circuit on the column query without surfacing the missing
// state) would flip these tests red. The pg-shard CI job owns
// them; no FAAS_PADDLE_API_KEY is required.
//
// Why not pgtest.Open: pgtest creates a per-test isolated schema
// (faas_test_<random>) and pins search_path to it, so migrations
// land in the test schema. But the production probe
// (PaddleOverageDedupeSchema at pgstore.go:9713) hard-codes
// `to_regclass('public.paddle_overage_dedupe')` — the B4 pre-flight
// is public-schema-shaped because production runs against public.
// openPublicSchema (below) gives each test an isolated, migrated schema.
//
// Build tag mirrors the rest of pkg/state pgtests: !no_pg so the
// FAAS_SKIP_PG_TESTS=1 escape hatch still works.

package state

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestPgStorePaddleOverageDedupeSchema_PostApply is the happy
// path: after migrations 00034 + 00041 + 00280 apply, the probe
// reports the four 00041 columns + non-zero counts. The two
// claims + one complete seed mirrors what meterd produces in
// production over a two-window stretch.
func TestPgStorePaddleOverageDedupeSchema_PostApply(t *testing.T) {
	ctx := context.Background()
	pool := openPublicSchema(t)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	s := NewPgStore(pool)

	// Seed two distinct (acct, window) pairs, then complete one.
	// Email must be unique (accounts.email is the natural key).
	// The acct.ID returned by CreateAccount is a Postgres uuid (the
	// accounts.id PK), NOT the email-derived label — that's what
	// ClaimPaddleOverageWindow keys on (paddle_overage_dedupe.account_id
	// is a uuid FK with ON DELETE CASCADE).
	emailA := uniqueEmail("pg-schema-A")
	emailB := uniqueEmail("pg-schema-B")
	acctA, err := s.CreateAccount(ctx, emailA, api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount(%s): %v", emailA, err)
	}
	acctB, err := s.CreateAccount(ctx, emailB, api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount(%s): %v", emailB, err)
	}

	now := time.Now().UTC().Truncate(time.Hour)
	windowA := now
	windowB := now.Add(time.Hour)
	lease := 5 * time.Minute

	if claimed, err := s.ClaimPaddleOverageWindow(ctx, acctA.ID, windowA, "pod-A", lease); err != nil || !claimed {
		t.Fatalf("claim A: claimed=%v err=%v", claimed, err)
	}
	if claimed, err := s.ClaimPaddleOverageWindow(ctx, acctB.ID, windowB, "pod-B", lease); err != nil || !claimed {
		t.Fatalf("claim B: claimed=%v err=%v", claimed, err)
	}
	if err := s.CompletePaddleOverageWindow(ctx, acctB.ID, windowB, 100); err != nil {
		t.Fatalf("Complete B: %v", err)
	}

	res, err := s.PaddleOverageDedupeSchema(ctx)
	if err != nil {
		t.Fatalf("PaddleOverageDedupeSchema: %v", err)
	}
	if !res.TableExists {
		t.Errorf("TableExists = false post-apply, want true (migrations 00034 + 00041 + 00280 must have created paddle_overage_dedupe)")
	}
	if !res.HasWindowStart || !res.HasState || !res.HasClaimedAt || !res.HasClaimedBy {
		t.Errorf("all HasX must be true post-apply; got %+v (a future migration that drops one of the 00041 columns would flip this red)", res)
	}
	if res.PendingRows != 1 {
		t.Errorf("PendingRows = %d, want 1 (acctA@windowA is still pending)", res.PendingRows)
	}
	if res.CompletedRows != 1 {
		t.Errorf("CompletedRows = %d, want 1 (acctB@windowB was completed with mbSeconds=100)", res.CompletedRows)
	}
}

// TestPgStorePaddleOverageDedupeSchema_PreApply_ReturnsTableMissing
// is the tripwire the B4 CLI maps to "apply 00034 then 00041".
// After the migrate-up, drop the table directly and re-probe.
// The probe must surface TableExists=false (and all HasX=false
// and counts zero) so the operator gets the missing-table hint
// rather than a 500 / generic probe error.
func TestPgStorePaddleOverageDedupeSchema_PreApply_ReturnsTableMissing(t *testing.T) {
	ctx := context.Background()
	pool := openPublicSchema(t)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	if _, err := pool.Exec(ctx, `drop table if exists paddle_overage_dedupe`); err != nil {
		t.Fatalf("drop paddle_overage_dedupe: %v", err)
	}

	s := NewPgStore(pool)
	res, err := s.PaddleOverageDedupeSchema(ctx)
	if err != nil {
		t.Fatalf("PaddleOverageDedupeSchema on missing table: %v (probe must surface TableExists=false, not a raw error)", err)
	}
	if res.TableExists {
		t.Errorf("TableExists = true after DROP, want false (the to_regclass probe must report the missing table — the CLI maps this to the missing-table hint)")
	}
	if res.HasWindowStart || res.HasState || res.HasClaimedAt || res.HasClaimedBy {
		t.Errorf("all HasX must be false on missing table; got %+v", res)
	}
	if res.PendingRows != 0 || res.CompletedRows != 0 {
		t.Errorf("counts on missing table must be 0; got pending=%d completed=%d", res.PendingRows, res.CompletedRows)
	}
}

// uniqueEmail produces a per-test email that won't collide with
// another test's seed. accounts.email is the natural key
// (state.accounts.email UNIQUE), so the test harness relies on a
// unique value. The unix-nano suffix is the same pattern
// cmd/e2e/billing_paddle_sandbox_test.go:250 uses for the signup
// email — keeping both seams consistent.
func uniqueEmail(label string) string {
	return fmt.Sprintf("acct-%s-%d@mig.example.test", label, time.Now().UnixNano())
}

// openPublicSchema returns a pgxpool whose connections default
// their search_path to public. Unlike pgtest.Open, this pool's
// schema is the production schema (public) — required because
// PaddleOverageDedupeSchema's to_regclass probe is
// public-schema-shaped. The pool is shared across both tests
// (a single t.Cleanup is registered the first time it's called);
// the first test to run migrates into public, subsequent tests
// find the migrations already applied.
//
// t.Cleanup drops every table in public so the next `go test`
// invocation starts fresh — this matters because the CI pg shard
// may persist the database across runs, and a stale
// paddle_overage_dedupe row from a prior run would mask the
// PreApply drop-table assertion.
//
// Honours FAAS_SKIP_PG_TESTS=1 — same escape hatch as pgtest.Open.
// openPublicSchema returns a pool whose public schema the caller may migrate.
//
// The probe under test inspects PUBLIC by name, so it genuinely needs a
// migrated public — but never the SHARED cluster's. This test used to migrate
// public on the shared cluster, and every other package's pgtest schema
// (search_path=<schema>,public) then resolved public.goose_db_version, goose
// reported "no migrations to run", and the schema stayed empty — the
// intermittent `relation "accounts" does not exist` failures in pg shard 2a
// (run 35150622499). pgtest.Open now refuses to start on a cluster whose
// public has been migrated, so that cannot come back quietly.
//
// pgtest.OpenMigrated hands out a PRIVATE database cloned from a migrated
// template when FAAS_PGTEST_TEMPLATE_DATABASE is set (CI sets it). Its public
// is migrated, it is ours alone, and it is dropped afterwards. Without the
// flag OpenMigrated falls back to an isolated schema, where a public-by-name
// probe cannot see its table, so the test declines rather than migrate the
// shared public to compensate.
func openPublicSchema(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv(pgtest.UseTemplateDatabase) == "" {
		t.Skipf("needs a private database (set %s=1, as CI does): the probe reads public by name, "+
			"and migrating the shared cluster's public poisons every other package", pgtest.UseTemplateDatabase)
	}
	return pgtest.OpenMigrated(t)
}
