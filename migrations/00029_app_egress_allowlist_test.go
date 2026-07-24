//go:build !no_pg

// Migration-apply test for 00029 (apps.egress_allowlist cidr[]).
// Pins the load-bearing contract from ADR-031 (M8 tier-2 network
// roadmap):
//
//   1. The migration set applies cleanly through 00029.
//   2. The egress_allowlist column exists with default '{}'.
//   3. UPDATE apps SET egress_allowlist = ARRAY['1.2.3.0/24'::cidr]
//      round-trips; read-back via pgstore returns the same array.
//   4. The CHECK constraint rejects v6 entries (the v4-only v1
//      contract — v6 mirror is deferred per ADR-031 "v4 only in v1").
//
// Slot note: this PR initially shipped as 00028 (CI run 30029100342
// failed with `duplicate migration prefix 00028` because PR #153
// had already merged `00028_instances_wake_id.sql` to main). Per
// `docs/adr/README.md` "migrations are append-only and contiguous"
// + the precedent set by PR #153 itself (commit fe97bb1:
// `fix(migrations): renumber wake_id migration 00027 → 00028`),
// renumbered to the next free slot without rebasing — same shape,
// same rationale. No SQL changes; the migration is slot-agnostic
// (no RENAME that depends on the prefix), only the test name and
// the human-readable seed UUIDs change.
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00029_AppEgressAllowlist pins the column + CHECK.
//
// Three named scenarios:
//
//   - DefaultEmpty: a freshly-migrated row's column reads back '{}'
//     even when the row was inserted before this migration landed.
//     (The default applies at insert-time, but a re-read after
//     migration proves the column is present and defaulted.)
//   - RoundTripV4: an UPDATE with a single v4 CIDR round-trips.
//   - RejectsV6: an UPDATE that tries to set a v6 entry fails the
//     CHECK constraint with the SQLSTATE Postgres uses for
//     check_violation.
func TestMigrations_00029_AppEgressAllowlist(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply the full migration set; 00029 is the new tail. A
	// regression that drops a slot between 1 and 29 surfaces here
	// before we get to the per-assertion pins (mirrors the 00024
	// pattern at migrations/00024_compute_nodes_test.go:46).
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 29)", err)
	}

	// (2) Seed an account + apps row to carry the column. The
	// literal UUIDs mirror the 00022 backfill test style — fixed
	// across reruns so the seed is idempotent.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000029',
		        'allowlist-test@example.com', 'pro', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000029',
		        '00000000-0000-0000-0000-000000000029',
		        'allowlist-test', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// (3) DefaultEmpty — newly-inserted row's column reads '{}'.
	var asText string
	if err := pool.QueryRow(ctx, `
		select egress_allowlist::text
		  from apps
		 where id = '00000000-0000-0000-0000-000000000029'
	`).Scan(&asText); err != nil {
		t.Fatalf("read apps.egress_allowlist after migrate: %v", err)
	}
	if asText != "{}" {
		t.Errorf("default egress_allowlist = %q, want %q", asText, "{}")
	}

	// (4) RoundTripV4 — UPDATE with a v4 CIDR survives, reads back
	// the same CIDR text.
	if _, err := pool.Exec(ctx, `
		update apps
		   set egress_allowlist = array['1.2.3.0/24'::cidr, '8.8.8.0/24'::cidr]
		 where id = '00000000-0000-0000-0000-000000000029'
	`); err != nil {
		t.Fatalf("update v4 egress_allowlist: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select egress_allowlist::text
		  from apps
		 where id = '00000000-0000-0000-0000-000000000029'
	`).Scan(&asText); err != nil {
		t.Fatalf("read updated egress_allowlist: %v", err)
	}
	// Postgres renders a cidr[] as {cidr1,cidr2}; the order matches
	// the array literal.
	if !strings.Contains(asText, "1.2.3.0/24") {
		t.Errorf("round-trip egress_allowlist missing 1.2.3.0/24: %q", asText)
	}
	if !strings.Contains(asText, "8.8.8.0/24") {
		t.Errorf("round-trip egress_allowlist missing 8.8.8.0/24: %q", asText)
	}

	// (5) RejectsV6 — the per-element family guard must fire. The
	// guard is a BEFORE-row TRIGGER (migrations/00029_app_egress_
	// allowlist.sql) that runs on INSERT/UPDATE OF egress_allowlist
	// and raises SQLSTATE 23514 (check_violation) with constraint
	// name 'apps_egress_allowlist_v4_only' via `using constraint =`.
	// The CHECK-with-bool_and shape that the previous draft used is
	// rejected by Postgres at apply time (aggregate functions are
	// not allowed in CHECK expressions) — keep the trigger shape.
	// We assert on the structured PgError fields rather than the
	// message text: pgx v5's *pgconn.PgError.Error() renders only
	// `Severity: Message (SQLSTATE Code)` (see pgconn/errors.go:53),
	// so the constraint name is reachable only via errors.As +
	// pgErr.ConstraintName. Pinning the code + constraint name also
	// protects against a future helper that wraps the error string.
	_, err := pool.Exec(ctx, `
		update apps
		   set egress_allowlist = array['fe80::/10'::cidr]
		 where id = '00000000-0000-0000-0000-000000000029'
	`)
	if err == nil {
		t.Fatalf("UPDATE with v6 CIDR unexpectedly succeeded; apps_egress_allowlist_v4_only TRIGGER did not fire")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("v6 update error not a *pgconn.PgError: %v", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("v6 update SQLSTATE = %q, want %q (check_violation); full: %v", pgErr.Code, "23514", err)
	}
	if pgErr.ConstraintName != "apps_egress_allowlist_v4_only" {
		t.Errorf("v6 update constraint name = %q, want %q; full: %v", pgErr.ConstraintName, "apps_egress_allowlist_v4_only", err)
	}
	// Also assert the message text names the contract so a future
	// regression that swaps the SQLSTATE helper but keeps the message
	// still trips the test. Belt and suspenders; the structured
	// assertions above are the load-bearing ones.
	if !strings.Contains(pgErr.Message, "v4-only") {
		t.Errorf("v6 update message = %q, want substring %q", pgErr.Message, "v4-only")
	}
}
