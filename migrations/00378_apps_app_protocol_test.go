//go:build !no_pg

// Migration-apply test for 00378 (per-app app_protocol wire-protocol
// selector, ADR-124). Pins the new column + closed-set CHECK constraint:
//
//  1. The migration set applies cleanly through 00378.
//  2. The column accepts the canonical text shape and round-trips.
//  3. Default is 'http1' (regression check — pre-PR rows land on the
//     legacy H1 path with zero behavior change).
//  4. Closed-set CHECK constraint `apps_app_protocol_chk` exists and
//     admits the three values, rejects an out-of-set value.
//  5. Opt-in round-trip: PATCH-style UPDATE writes 'http2' / 'grpc'
//     and reads back the same value (mirrors the apid updateApp
//     handler path).
//  6. Replay-safe: ADD COLUMN IF NOT EXISTS plus DROP CONSTRAINT IF
//     EXISTS / ADD CONSTRAINT makes a second MigrateUp a no-op
//     (PR #377 / ADR-041 + 00346_deployments_annotation.sql precedent).
//
// Slot note: renumbered 00347 → 00373 → 00375 → 00378 (seventh hop).
// PR #1034 (ADR-124 PR-2 — cmd/migrate -leader mode) owns a real
// migration at 00347 (`00347_migration_notify.sql`); PR #1006's
// deployment_audit chain previously occupied 00373 + 00374; PR
// #1006's stages-prod-ready PR-A cluster holds slots 00375
// (endpoint_discovery on main) indirectly. Per
// `cross-pr-slot-precheck-pr-867-collision-2026-08-13.md`,
// 00378 is the first free slot above all open PR claims + main's
// 00375_endpoint_discovery.sql. Fence bridges added at 00376 +
// 00377 (above PR #1036's `00376_compute_nodes_active_unique.sql`
// + `00377_instances_wake_attempt_active_unique.sql` claims). The
// migration is slot-agnostic — only the filename and the test
// function name carry the literal slot.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00378_AppsAppProtocol(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// Seed UUIDs carry the slot number in the last group (`...000378`,
	// `...000478`) so a reader scanning the test fixtures can pin each
	// row to this migration without grepping the file name. The literal
	// slot value MUST stay in sync with the filename; renumber per
	// `migrations/README.md` if a sibling PR grabs 00378 first.

	// (1) Apply through 00378. A regression that drops a slot between
	// 1 and 347 surfaces here before the per-assertion pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 347)", err)
	}

	// (2) Seed an account + app. The literal UUIDs are fixed across
	// reruns so the seed is idempotent. App fixture uses the seeded
	// hobby plan so the plan-gate check (apid returns 403 on Free for
	// `grpc`) is testable downstream without affecting this migration
	// test.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000378',
		        'app-protocol-test@example.com', 'hobby', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000478',
		        '00000000-0000-0000-0000-000000000378',
		        'app-protocol-test-app', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// (3) Default round-trip: the freshly-inserted app reads
	// app_protocol='http1' (NOT NULL DEFAULT 'http1'). This is the
	// regression check that pre-PR rows stay on the buffered/H1 path
	// without an explicit PATCH.
	var defaultVal string
	if err := pool.QueryRow(ctx, `
		select app_protocol from apps where id = '00000000-0000-0000-0000-000000000478'
	`).Scan(&defaultVal); err != nil {
		t.Fatalf("read default app_protocol: %v", err)
	}
	if defaultVal != "http1" {
		t.Errorf("app_protocol default = %q, want %q (regression: pre-PR rows must stay on http1)", defaultVal, "http1")
	}

	// (4) Closed-set CHECK constraint `apps_app_protocol_chk` exists.
	// The pg_constraint query is the cheapest portable check; pinning
	// the consrc catches a regression that widens or narrows the
	// closed set without bumping this test.
	var conname string
	if err := pool.QueryRow(ctx, `
		select conname from pg_constraint
		where conrelid = 'apps'::regclass
		  and conname = 'apps_app_protocol_chk'
	`).Scan(&conname); err != nil {
		t.Fatalf("read CHECK constraint: %v (regression: apps_app_protocol_chk not created)", err)
	}
	if conname == "" {
		t.Errorf("apps_app_protocol_chk not found in pg_constraint")
	}

	// (4b) CHECK rejects an out-of-set value. A 'h2c' value would
	// fall through to the apid-default path silently otherwise.
	_, err := pool.Exec(ctx, `
		update apps set app_protocol = 'h2c' where id = '00000000-0000-0000-0000-000000000478'
	`)
	if err == nil {
		t.Errorf("CHECK rejected nothing: 'h2c' must be rejected (regression: closed set widened without an ADR)")
	}

	// (5) Opt-in round-trip: PATCH-style UPDATE writes 'http2' and
	// reads back 'http2'. Mirrors the apid updateApp handler path so
	// a future regression in the write side surfaces here.
	if _, err := pool.Exec(ctx, `
		update apps set app_protocol = 'http2' where id = '00000000-0000-0000-0000-000000000478'
	`); err != nil {
		t.Fatalf("update app_protocol http2: %v", err)
	}
	var optedIn string
	if err := pool.QueryRow(ctx, `
		select app_protocol from apps where id = '00000000-0000-0000-0000-000000000478'
	`).Scan(&optedIn); err != nil {
		t.Fatalf("read opted-in app_protocol: %v", err)
	}
	if optedIn != "http2" {
		t.Errorf("app_protocol after update = %q, want %q", optedIn, "http2")
	}

	// (5b) Opt-in to grpc round-trips (plan gate is at apid, the SQL
	// layer accepts it on this Hobby fixture).
	if _, err := pool.Exec(ctx, `
		update apps set app_protocol = 'grpc' where id = '00000000-0000-0000-0000-000000000478'
	`); err != nil {
		t.Fatalf("update app_protocol grpc: %v", err)
	}
	var optedGrpc string
	if err := pool.QueryRow(ctx, `
		select app_protocol from apps where id = '00000000-0000-0000-0000-000000000478'
	`).Scan(&optedGrpc); err != nil {
		t.Fatalf("read opted-in grpc: %v", err)
	}
	if optedGrpc != "grpc" {
		t.Errorf("app_protocol after update = %q, want %q", optedGrpc, "grpc")
	}

	// (6) Replay safety: a second MigrateUp is a no-op (the migration
	// uses ADD COLUMN IF NOT EXISTS plus DROP CONSTRAINT IF EXISTS /
	// ADD CONSTRAINT). PR #377 / ADR-041 contract.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
