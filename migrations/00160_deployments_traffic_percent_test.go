//go:build !no_pg

// Migration-apply test for 00160 (deployments.traffic_percent +
// CHECK constraint). Pins the load-bearing contract from issue
// #556 (traffic splitting across deployments, PR-A: schema +
// validation + wire surface; picker mutation is PR-B).
//
//  1. The migration set applies cleanly through 00160.
//  2. The new column lands on deployments with the expected type
//     and NOT NULL DEFAULT 100 shape (existing rows stay valid).
//  3. The CHECK constraint accepts the documented [0, 100] range
//     and rejects a stray value (23514 SQLSTATE on the bad insert).
//  4. Re-running goose MigrateUp is a no-op (idempotent replay
//     safety — the apply_walk_test pins this at the directory
//     level but per-migration shape is also asserted here as
//     defence in depth).
//
// Build tag mirrors 00025_deployments_rootfs_key_test.go: set
// FAAS_SKIP_PG_TESTS=1 locally to skip.

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

func TestMigrations_00160_DeploymentTrafficPercent(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00160 should land last.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR follow-up failure mode: missing migration slot between 159 and 160)", err)
	}

	// (2) Column shape. Scoped to current_schema() per
	// migrations-info-schema-scoping-pattern.md so a parallel
	// pgtest run on the same box doesn't bleed rows in.
	var dataType, nullable string
	if err := pool.QueryRow(ctx, `
		select data_type, is_nullable
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'deployments'
		   and column_name = 'traffic_percent'`).Scan(&dataType, &nullable); err != nil {
		t.Fatalf("query column: %v", err)
	}
	if dataType != "integer" {
		t.Errorf("traffic_percent type = %q, want integer", dataType)
	}
	if nullable != "NO" {
		t.Errorf("traffic_percent nullable = %q, want NO (NOT NULL DEFAULT 100)", nullable)
	}

	// (3) CHECK constraint exists and names the closed [0, 100] range.
	//
	// The rendering is NOT pinned: pg_get_constraintdef normalises
	// `BETWEEN 0 AND 100` into `(traffic_percent >= 0) AND
	// (traffic_percent <= 100)`, so a substring probe for "BETWEEN"
	// asserts which spelling the migration author happened to use
	// rather than what the constraint does. Both spellings are
	// accepted here; the "it is a range, not an enumeration" semantics
	// are pinned by the accept/reject cases in steps (4) and (5).
	var def string
	err := pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'deployments_traffic_percent_chk'
		   and n.nspname = current_schema()`).Scan(&def)
	if err != nil {
		t.Fatalf("query constraint: %v (range CHECK must have landed)", err)
	}
	if !strings.Contains(def, "0") || !strings.Contains(def, "100") {
		t.Errorf("constraint def %q missing 0 or 100 (range CHECK must cover [0, 100])", def)
	}
	upper := strings.ToUpper(def)
	isBetween := strings.Contains(upper, "BETWEEN")
	isComparison := strings.Contains(def, ">=") && strings.Contains(def, "<=")
	if !isBetween && !isComparison {
		t.Errorf("constraint def %q is neither a BETWEEN nor a >=/<= range (a range CHECK must bound both ends, not enumerate values)", def)
	}

	// (4) Bad value rejected. Insert a row with traffic_percent =
	// 101 and expect a 23514 (check_violation) SQLSTATE. The
	// pgtest helper only opens a schema; the (account, app)
	// fixture is seeded inline with a slot-unique UUID literal
	// (mirrors the 00147 test's pattern at lines 144-159).
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ('00000000-0000-0000-0000-000000000160', 'scale', 'traffic-test@example.com')
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-000000000160',
		        '00000000-0000-0000-0000-000000000160',
		        'traffic-test', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}
	const insertTrafficPercent = `
		insert into deployments (app_id, image_digest, status, traffic_percent)
		values ('00000000-0000-0000-0000-000000000160', $1, 'building', $2)`

	// Both ends of the range must be closed. Matching the constraint
	// name (not just the SQLSTATE) is what keeps this from passing
	// because some unrelated CHECK rejected the row first.
	for _, tc := range []struct {
		name    string
		digest  string
		percent int
	}{
		{"above 100", "sha256:" + strings.Repeat("f", 64), 101},
		{"negative", "sha256:" + strings.Repeat("e", 64), -1},
	} {
		_, err := pool.Exec(ctx, insertTrafficPercent, tc.digest, tc.percent)
		if err == nil {
			t.Errorf("insert traffic_percent=%d (%s) succeeded; want 23514 CHECK violation", tc.percent, tc.name)
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Errorf("insert traffic_percent=%d (%s): got %v, want a *pgconn.PgError", tc.percent, tc.name, err)
			continue
		}
		if pgErr.Code != "23514" {
			t.Errorf("insert traffic_percent=%d (%s): got SQLSTATE %s, want 23514", tc.percent, tc.name, pgErr.Code)
		}
		if pgErr.ConstraintName != "deployments_traffic_percent_chk" {
			t.Errorf("insert traffic_percent=%d (%s): got constraint %q, want deployments_traffic_percent_chk", tc.percent, tc.name, pgErr.ConstraintName)
		}
	}

	// (5) Boundaries AND the interior are accepted. 0 and 100 are both
	// legal (0 = "this row receives no traffic" used during rollback;
	// 100 = "this row is the sole live deployment"), and 50 is the
	// assertion that makes step (3) unnecessary as a rendering probe:
	// a constraint written as `traffic_percent IN (0, 100)` would pass
	// every other check in this test and fail here. That, plus the
	// -1/101 rejections above, is the closed range [0, 100] pinned by
	// behaviour rather than by the spelling pg_get_constraintdef
	// happens to emit.
	//
	// Each row uses a distinct image_digest so the
	// deployments.image_digest uniqueness (if any) doesn't trip —
	// current schema doesn't enforce it, but the belt-and-braces
	// approach matches how 00147 seeds.
	for i, v := range []int{0, 50, 100} {
		digest := "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
		if _, err := pool.Exec(ctx, insertTrafficPercent, digest, v); err != nil {
			t.Errorf("insert traffic_percent=%d failed: %v (in-range value must be accepted)", v, err)
		}
	}

	// (6) Replay safety: applying the migration set a second time
	// must not blow up. The ADD COLUMN IF NOT EXISTS / DROP
	// CONSTRAINT IF EXISTS guards handle this; this assertion is
	// a tripwire that survives future refactors.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (the ADD COLUMN IF NOT EXISTS / DROP CONSTRAINT IF EXISTS guards must have been silently dropped)", err)
	}
}
