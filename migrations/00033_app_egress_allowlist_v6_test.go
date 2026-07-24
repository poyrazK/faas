//go:build !no_pg

// Migration-apply test for 00033 (ADR-032 — v6 mirror of the
// per-app egress allowlist). Pins the contract swap:
//
//   1. The migration set applies cleanly through 00033.
//   2. v6 CIDRs round-trip (UPDATE / read-back).
//   3. Mixed v4 + v6 in one UPDATE round-trips.
//   4. `0.0.0.0/0` is rejected (SQLSTATE 23514, constraint
//      `apps_egress_allowlist_cidr`).
//   5. `::/0` is rejected with the same shape.
//
// Slot note: 00033 is the next free slot after 00032
// (`00030_invocations.sql`, `00031_invocations_notify.sql`, and
// `00032_delayed_tasks.sql` already on main — all for the
// event-driven FaaS / Move 1 surface, unrelated to the egress
// allowlist). Per `docs/adr/README.md` "migrations are
// append-only and contiguous" + the precedent set by PR #153
// (`00027→00028`), PR #159 (`00028→00029`), and PR #175
// (`00030→00032→00033`) for collision renumbering. If a parallel
// PR claims 00033 first, renumber per that precedent; the SQL is
// slot-agnostic.
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

// TestMigrations_00033_AppEgressAllowlistV6 pins the v6 mirror
// contract from ADR-032. Five named scenarios:
//
//   - ApplyThrough030: the full migration set applies cleanly
//     through 00033 (regression: missing slot between 1 and 33
//     surfaces here before we get to the per-assertion pins).
//   - RoundTripV6: an UPDATE with two v6 CIDRs reads back the
//     same CIDRs.
//   - RoundTripMixed: an UPDATE with v4 + v6 in one UPDATE
//     reads back both.
//   - RejectsSlashZeroV4: `0.0.0.0/0` fails with SQLSTATE 23514,
//     ConstraintName `apps_egress_allowlist_cidr`, message
//     contains `/0`.
//   - RejectsSlashZeroV6: `::/0` fails with the same shape.
//
// Assertion style mirrors `migrations/00029_app_egress_allowlist_test.go`:
// `errors.As(err, &pgErr)` + `pgErr.Code` + `pgErr.ConstraintName`.
// pgx v5's *pgconn.PgError.Error() renders only
// `Severity: Message (SQLSTATE Code)` (see
// `github.com/jackc/pgx/v5/pgconn/errors.go:53`), so the
// constraint name is reachable only via the typed fields, not
// `strings.Contains`.
func TestMigrations_00033_AppEgressAllowlistV6(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00033. A regression that drops a slot
	// between 1 and 33 surfaces here before the per-assertion pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 33)", err)
	}

	// (2) Seed an account + apps row to carry the column. The
	// literal UUIDs are fixed across reruns so the seed is
	// idempotent; they mirror the 00029 test style for grep-ability.
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000033',
		        'allowlist-v6-test@example.com', 'pro', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000033',
		        '00000000-0000-0000-0000-000000000033',
		        'allowlist-v6-test', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// (3) RoundTripV6 — UPDATE with two v6 CIDRs reads both back.
	// Postgres renders cidr[] as `{cidr1,cidr2}`; the order matches
	// the array literal.
	if _, err := pool.Exec(ctx, `
		update apps
		   set egress_allowlist = array['fe80::/10'::cidr, '2001:db8::/32'::cidr]
		 where id = '00000000-0000-0000-0000-000000000033'
	`); err != nil {
		t.Fatalf("update v6 egress_allowlist: %v", err)
	}
	var asText string
	if err := pool.QueryRow(ctx, `
		select egress_allowlist::text
		  from apps
		 where id = '00000000-0000-0000-0000-000000000033'
	`).Scan(&asText); err != nil {
		t.Fatalf("read v6 egress_allowlist: %v", err)
	}
	if !strings.Contains(asText, "fe80::/10") {
		t.Errorf("v6 round-trip missing fe80::/10: %q", asText)
	}
	if !strings.Contains(asText, "2001:db8::/32") {
		t.Errorf("v6 round-trip missing 2001:db8::/32: %q", asText)
	}

	// (4) RoundTripMixed — v4 + v6 in one UPDATE reads both back.
	// Single column carries both families; the renderer is what
	// partitions them into ip / ip6 chains (ADR-032).
	if _, err := pool.Exec(ctx, `
		update apps
		   set egress_allowlist = array['1.2.3.0/24'::cidr, 'fe80::/10'::cidr]
		 where id = '00000000-0000-0000-0000-000000000033'
	`); err != nil {
		t.Fatalf("update mixed egress_allowlist: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		select egress_allowlist::text
		  from apps
		 where id = '00000000-0000-0000-0000-000000000033'
	`).Scan(&asText); err != nil {
		t.Fatalf("read mixed egress_allowlist: %v", err)
	}
	if !strings.Contains(asText, "1.2.3.0/24") {
		t.Errorf("mixed round-trip missing 1.2.3.0/24: %q", asText)
	}
	if !strings.Contains(asText, "fe80::/10") {
		t.Errorf("mixed round-trip missing fe80::/10: %q", asText)
	}

	// (5) RejectsSlashZeroV4 — the non-/0 contract fires. The
	// trigger raises SQLSTATE 23514 (check_violation) with
	// constraint name `apps_egress_allowlist_cidr` via
	// `using constraint =`. Belt and suspenders: assert on the
	// structured fields AND the message text (which contains
	// `masklen` + `/0`).
	_, err := pool.Exec(ctx, `
		update apps
		   set egress_allowlist = array['0.0.0.0/0'::cidr]
		 where id = '00000000-0000-0000-0000-000000000033'
	`)
	if err == nil {
		t.Fatalf("UPDATE with 0.0.0.0/0 unexpectedly succeeded; apps_egress_allowlist_cidr TRIGGER did not fire")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("/0 v4 update error not a *pgconn.PgError: %v", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("/0 v4 SQLSTATE = %q, want %q (check_violation); full: %v", pgErr.Code, "23514", err)
	}
	if pgErr.ConstraintName != "apps_egress_allowlist_cidr" {
		t.Errorf("/0 v4 constraint name = %q, want %q; full: %v", pgErr.ConstraintName, "apps_egress_allowlist_cidr", err)
	}
	if !strings.Contains(pgErr.Message, "/0") && !strings.Contains(pgErr.Message, "masklen") {
		t.Errorf("/0 v4 message = %q, want substring %q or %q", pgErr.Message, "/0", "masklen")
	}

	// (6) RejectsSlashZeroV6 — same shape for v6.
	_, err = pool.Exec(ctx, `
		update apps
		   set egress_allowlist = array['::/0'::cidr]
		 where id = '00000000-0000-0000-0000-000000000033'
	`)
	if err == nil {
		t.Fatalf("UPDATE with ::/0 unexpectedly succeeded; apps_egress_allowlist_cidr TRIGGER did not fire")
	}
	pgErr = nil
	if !errors.As(err, &pgErr) {
		t.Fatalf("/0 v6 update error not a *pgconn.PgError: %v", err)
	}
	if pgErr.Code != "23514" {
		t.Errorf("/0 v6 SQLSTATE = %q, want %q (check_violation); full: %v", pgErr.Code, "23514", err)
	}
	if pgErr.ConstraintName != "apps_egress_allowlist_cidr" {
		t.Errorf("/0 v6 constraint name = %q, want %q; full: %v", pgErr.ConstraintName, "apps_egress_allowlist_cidr", err)
	}
	if !strings.Contains(pgErr.Message, "/0") && !strings.Contains(pgErr.Message, "masklen") {
		t.Errorf("/0 v6 message = %q, want substring %q or %q", pgErr.Message, "/0", "masklen")
	}
}
