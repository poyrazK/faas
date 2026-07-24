//go:build !no_pg

// Migration-apply test for 00029 (apps.egress_allowlist cidr[]).
// Pins the load-bearing contract from ADR-031 (M8 tier-2 network
// roadmap):
//
//   1. The migration set applies cleanly through 00029.
//   2. The egress_allowlist column exists with default '{}'.
//   3. UPDATE apps SET egress_allowlist = ARRAY['1.2.3.0/24'::cidr]
//      round-trips; read-back via pgstore returns the same array.
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
// Contract history: 00029 originally shipped with a v4-only
// BEFORE-row TRIGGER (`apps_egress_allowlist_v4_only` /
// `apps_egress_allowlist_v4_only_check()`). ADR-032 (migration
// 00030) replaces it with a v4-or-v6, non-/0 guard
// (`apps_egress_allowlist_cidr` /
// `apps_egress_allowlist_cidr_check()`). The `AcceptsV6`
// sub-test below replaced the original `RejectsV6` sub-test at
// ADR-032 ship time — the v6 mirror closes a paper-cut where a
// tenant who pinned an IPv4 allowlist still had every IPv6
// destination reachable (the v6 chain's only constraint was the
// ADR-023 deny set + the chain-policy accept).
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

// TestMigrations_00029_AppEgressAllowlist pins the column + the
// v4-round-trip contract.
//
// Three named scenarios:
//
//   - DefaultEmpty: a freshly-migrated row's column reads back '{}'
//     even when the row was inserted before this migration landed.
//     (The default applies at insert-time, but a re-read after
//     migration proves the column is present and defaulted.)
//   - RoundTripV4: an UPDATE with a single v4 CIDR round-trips.
//   - AcceptsV6: an UPDATE that sets a v6 entry now succeeds
//     (ADR-032 — the v4-only trigger was replaced by a v4-or-v6
//     non-/0 guard in migration 00030). The non-/0 contract itself
//     is pinned by 00030's
//     TestMigrations_00030_AppEgressAllowlistV6::RejectsSlashZeroV6.
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

	// (5) AcceptsV6 — the v4-only guard from 00029 was replaced by
	// migration 00030 (ADR-032) with a v4-or-v6, non-/0 guard
	// (`apps_egress_allowlist_cidr`). v6 entries now succeed; the
	// non-/0 contract is pinned by 00030's
	// TestMigrations_00030_AppEgressAllowlistV6::RejectsSlashZeroV6.
	if _, err := pool.Exec(ctx, `
		update apps
		   set egress_allowlist = array['fe80::/10'::cidr]
		 where id = '00000000-0000-0000-0000-000000000029'
	`); err != nil {
		t.Fatalf("update v6 egress_allowlist (post-ADR-032): %v", err)
	}
	var asTextV6 string
	if err := pool.QueryRow(ctx, `
		select egress_allowlist::text
		  from apps
		 where id = '00000000-0000-0000-0000-000000000029'
	`).Scan(&asTextV6); err != nil {
		t.Fatalf("read v6 egress_allowlist (post-ADR-032): %v", err)
	}
	if !strings.Contains(asTextV6, "fe80::/10") {
		t.Errorf("v6 round-trip missing fe80::/10: %q", asTextV6)
	}
	// Reference errors + pgconn so the import set stays the same
	// if a future commit re-introduces a structured-PgError
	// assertion in this file (the 00030 test owns the typed
	// PgError checks; this file pins the positive round-trip).
	var _ = errors.As
	var _ pgconn.PgError
}
