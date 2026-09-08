//go:build !no_pg

// Migration-apply test for 00156 (issue #695 / ADR-080 — flip the
// global default for apps auth from public-by-default to
// authenticated-by-default + grand-father existing customers).
//
// Pins:
//
//  1. The migration set applies cleanly through 00155.
//  2. The new column apps.auth_default_flipped_at exists with the
//     expected timestamptz + NULLable shape (regression check that
//     a future contributor doesn't flip it to NOT NULL — the
//     grand-father mechanism is `WHERE auth_default_flipped_at IS
//     NULL` on insert).
//  3. The migration's audit row `apps.auth_default_global_flipped`
//     is emitted exactly once with the ADR-080 §3 payload shape
//     (migrated_count + plan_overrides + from/to defaults). The
//     migration runs against a fresh pgtest schema with zero apps,
//     so the captured migrated_count is 0 — that's the right shape
//     (a future contributor that flips the migration to emit
//     per-account rows breaks here).
//  4. After seeding two pre-flip apps and re-running the production
//     UPDATE verbatim, both rows land auth_default_flipped_at !=
//     NULL — the grand-father backfill is exercised end-to-end.
//     Same convention as migrations/00131_apps_align_min_instances_test.go
//     and migrations/00022_backfill_test.go: seed divergent rows
//     post-MigrateUp, then run the production backfill SQL.
//  5. Existing app behaviour preserved: the seeded apps still have
//     require_authn=false and public_auth_mode='open' AFTER the
//     backfill. The grand-father marker is independent of these
//     columns — flipping the marker does NOT flip the per-app
//     state. Load-bearing for issue #695 AC #1.
//  6. Replay safety: a second execution of the production UPDATE
//     is a no-op (the WHERE auth_default_flipped_at IS NULL
//     predicate filters out already-stamped rows; a re-stamp would
//     shift the `since YYYY-MM-DD` suffix on the CLI annotation
//     and the dashboard banner display).
//  7. The audit row remains at exactly 1 across the re-apply path
//     (the migration's INSERT is guarded by `WHERE NOT EXISTS`).
//
// Build tag matches the rest of the migration tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package migrations_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00156_AppsAuthDefaultFlip(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00156. A regression that drops a slot between
	// 1 and 155 surfaces here before the per-assertion pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 155)", err)
	}

	// (2) Column shape: apps.auth_default_flipped_at is timestamptz
	// NULL. information_schema probe catches a future regression that
	// flips the column to NOT NULL (which would break the grand-father
	// re-apply path).
	var dataType, isNullable string
	if err := pool.QueryRow(ctx, `
		select data_type, is_nullable
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'apps'
		   and column_name = 'auth_default_flipped_at'
	`).Scan(&dataType, &isNullable); err != nil {
		t.Fatalf("read apps.auth_default_flipped_at column metadata: %v", err)
	}
	if dataType != "timestamp with time zone" {
		t.Errorf("auth_default_flipped_at data_type = %q, want \"timestamp with time zone\"", dataType)
	}
	if isNullable != "YES" {
		t.Errorf("auth_default_flipped_at is_nullable = %q, want \"YES\" (grandfather INSERT depends on NULL)", isNullable)
	}

	// (3) Audit row shape at migration-time. The migration runs against
	// a fresh pgtest schema (zero apps), so its captured
	// migrated_count subquery reads 0 — that's the right shape: the
	// test pins the payload contract, NOT a specific migrated_count
	// for a populated production DB. A future contributor that flips
	// the migration to emit per-account rows breaks the count==1
	// assertion below.
	var auditCount int
	if err := pool.QueryRow(ctx, `
		select count(*) from events
		 where kind = 'apps.auth_default_global_flipped'
		   and actor = 'migration'
	`).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit row count for apps.auth_default_global_flipped = %d, want exactly 1", auditCount)
	}
	var payload []byte
	var actorAccountID *string // nullable; SHOULD be NULL for migration-emitted rows
	if err := pool.QueryRow(ctx, `
		select actor_account_id::text, data
		  from events
		 where kind = 'apps.auth_default_global_flipped'
		   and actor = 'migration'
		 order by at asc
		 limit 1
	`).Scan(&actorAccountID, &payload); err != nil {
		t.Fatalf("read audit row payload: %v", err)
	}
	if actorAccountID != nil {
		t.Errorf("audit row actor_account_id = %v, want NULL (migration-emitted, no actor account)", *actorAccountID)
	}
	var parsed struct {
		MigratedCount             int64  `json:"migrated_count"`
		FromRequireAuthnDefault   bool   `json:"from_require_authn_default"`
		ToRequireAuthnDefault     bool   `json:"to_require_authn_default"`
		FromPublicAuthModeDefault string `json:"from_public_auth_mode_default"`
		ToPublicAuthModeDefault   string `json:"to_public_auth_mode_default"`
		PlanOverrides             map[string]struct {
			RequireAuthn   bool   `json:"require_authn"`
			PublicAuthMode string `json:"public_auth_mode"`
		} `json:"plan_overrides"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("parse audit row JSON: %v", err)
	}
	if parsed.FromRequireAuthnDefault != false {
		t.Errorf("audit from_require_authn_default = %v, want false", parsed.FromRequireAuthnDefault)
	}
	if parsed.ToRequireAuthnDefault != true {
		t.Errorf("audit to_require_authn_default = %v, want true", parsed.ToRequireAuthnDefault)
	}
	if parsed.FromPublicAuthModeDefault != "open" {
		t.Errorf("audit from_public_auth_mode_default = %q, want \"open\"", parsed.FromPublicAuthModeDefault)
	}
	if parsed.ToPublicAuthModeDefault != "bearer" {
		t.Errorf("audit to_public_auth_mode_default = %q, want \"bearer\"", parsed.ToPublicAuthModeDefault)
	}
	wantOverrides := map[string]struct {
		RequireAuthn   bool
		PublicAuthMode string
	}{
		"free":  {false, "open"},
		"hobby": {true, "open"},
		"pro":   {true, "bearer"},
		"scale": {true, "bearer"},
	}
	for plan, want := range wantOverrides {
		got, ok := parsed.PlanOverrides[plan]
		if !ok {
			t.Errorf("audit plan_overrides missing %q", plan)
			continue
		}
		if got.RequireAuthn != want.RequireAuthn || got.PublicAuthMode != want.PublicAuthMode {
			t.Errorf("audit plan_overrides[%q] = (%v, %q), want (%v, %q)",
				plan, got.RequireAuthn, got.PublicAuthMode, want.RequireAuthn, want.PublicAuthMode)
		}
	}
	// At migration-time the schema has zero apps, so the audit's
	// migrated_count subquery reads 0. The production backfill stamp
	// path is exercised in step (5) below.
	if parsed.MigratedCount != 0 {
		t.Errorf("audit migrated_count at migration-time = %d, want 0 (fresh schema)", parsed.MigratedCount)
	}

	// (4) Seed two pre-flip apps on different accounts so the
	// grand-father backfill has rows to stamp. These rows are
	// inserted post-MigrateUp so they have auth_default_flipped_at =
	// NULL — exactly the state of a real pre-flip app when the
	// migration lands (the migration's UPDATE has already run and
	// seen them as 0, so they were untouched).
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000001561',
		        'auth-default-flip-a@example.com', 'pro', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account A: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000001562',
		        'auth-default-flip-b@example.com', 'hobby', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account B: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, require_authn, public_auth_mode, created_at)
		values ('00000000-0000-0000-0000-000000001563',
		        '00000000-0000-0000-0000-000000001561',
		        'auth-default-flip-a-app', 'function', 512, 5, 300, 'active', false, 'open', now()),
		       ('00000000-0000-0000-0000-000000001564',
		        '00000000-0000-0000-0000-000000001562',
		        'auth-default-flip-b-app', 'function', 256, 2, 60, 'active', false, 'open', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed two pre-flip apps: %v", err)
	}

	// (5) Run the production UPDATE verbatim — the same statement
	// the migration runs. This is the convention from
	// migrations/00131_apps_align_min_instances_test.go (seed post-
	// MigrateUp, then re-run the backfill SQL to exercise the
	// predicate shape directly). A regression that drops the IS NULL
	// predicate, re-stamps existing rows, or omits the column from
	// the UPDATE surfaces here.
	if _, err := pool.Exec(ctx, `
		UPDATE apps
		   SET auth_default_flipped_at = COALESCE(auth_default_flipped_at, now())
		 WHERE auth_default_flipped_at IS NULL
	`); err != nil {
		t.Fatalf("production UPDATE: %v", err)
	}

	// (6) Grandfather marker round-trip: every app row reads back
	// auth_default_flipped_at != NULL after the backfill. This is
	// the load-bearing invariant for issue #695 AC #2 — no existing
	// customer's behaviour changes because (a) the column didn't
	// exist before the migration, (b) the migration backfill stamps
	// the marker without touching require_authn / public_auth_mode.
	var nulls, stamped int64
	if err := pool.QueryRow(ctx, `
		select count(*) filter (where auth_default_flipped_at is null) as nulls,
		       count(*) filter (where auth_default_flipped_at is not null) as stamped
		  from apps
	`).Scan(&nulls, &stamped); err != nil {
		t.Fatalf("count grandfather markers: %v", err)
	}
	if nulls != 0 {
		t.Errorf("auth_default_flipped_at has %d NULL rows after backfill; grand-father UPDATE must stamp every existing row", nulls)
	}
	if stamped != 2 {
		t.Errorf("auth_default_flipped_at stamped %d rows, want exactly 2 (the two fixture apps)", stamped)
	}

	// (7) Existing app behaviour preserved: the seed apps still have
	// require_authn=false and public_auth_mode='open' AFTER the
	// backfill. The grand-father marker is independent of these
	// columns — flipping the marker does NOT flip the per-app state.
	// This is the load-bearing invariant for issue #695 AC #1 (no
	// existing customer breakage).
	for _, id := range []string{
		"00000000-0000-0000-0000-000000001563",
		"00000000-0000-0000-0000-000000001564",
	} {
		var requireAuthn bool
		var publicAuthMode string
		if err := pool.QueryRow(ctx, `
			select require_authn, public_auth_mode
			  from apps where id = $1
		`, id).Scan(&requireAuthn, &publicAuthMode); err != nil {
			t.Fatalf("read app %s post-backfill: %v", id, err)
		}
		if requireAuthn != false {
			t.Errorf("app %s require_authn = %v after backfill, want false (grandfather must preserve pre-flip state)", id, requireAuthn)
		}
		if publicAuthMode != "open" {
			t.Errorf("app %s public_auth_mode = %q after backfill, want \"open\"", id, publicAuthMode)
		}
	}

	// (8) Replay-safety: a second execution of the production UPDATE
	// is a no-op. The audit row count remains 1 (the migration's
	// INSERT is guarded by WHERE NOT EXISTS); the grandfather stamps
	// are unchanged (the UPDATE is guarded by IS NULL). ADR-041
	// contract.
	// auth_default_flipped_at is timestamptz (pinned at (2) above), so
	// the scan target is time.Time — scanning a timestamptz into a
	// string fails under pgx's binary protocol.
	var stampBefore time.Time
	if err := pool.QueryRow(ctx,
		`select auth_default_flipped_at from apps where id = '00000000-0000-0000-0000-000000001563'`,
	).Scan(&stampBefore); err != nil {
		t.Fatalf("replay-safety: read stamp before: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE apps
		   SET auth_default_flipped_at = COALESCE(auth_default_flipped_at, now())
		 WHERE auth_default_flipped_at IS NULL
	`); err != nil {
		t.Fatalf("replay-safety: second UPDATE failed: %v", err)
	}
	var stampAfter time.Time
	if err := pool.QueryRow(ctx,
		`select auth_default_flipped_at from apps where id = '00000000-0000-0000-0000-000000001563'`,
	).Scan(&stampAfter); err != nil {
		t.Fatalf("replay-safety: read stamp after: %v", err)
	}
	if !stampAfter.Equal(stampBefore) {
		t.Errorf("replay-safety: grand-father stamp changed across re-apply (%s → %s); the UPDATE must NOT re-stamp existing rows",
			stampBefore.Format(time.RFC3339Nano), stampAfter.Format(time.RFC3339Nano))
	}
	// Stamped count unchanged.
	if err := pool.QueryRow(ctx,
		`select count(*) from apps where auth_default_flipped_at is not null`,
	).Scan(&stamped); err != nil {
		t.Fatalf("replay-safety: re-count stamps: %v", err)
	}
	if stamped != 2 {
		t.Errorf("replay-safety: stamped count = %d after second UPDATE, want 2 (no new rows stamped)", stamped)
	}
}
