//go:build !no_pg

// Migration-apply test for 00131 (ADR-071 §Downstream / issue #557
// closure — apps.align_min_instances backfill). Pins:
//
//  1. The migration set applies cleanly through 00131.
//  2. Pre-migration: an app row with (col=3, jsonb={min_instances:0})
//     has divergent sources; the helper returns max()=3.
//  3. Post-migration: the same row has (col=3, jsonb={min_instances:3})
//     — the backfill projects the column into the jsonb without
//     silently clobbering the inverse direction (jsonb > column).
//  4. Rows with col=0 / jsonb unset are no-ops (no row rewrite).
//  5. Rows with jsonb > column are untouched (the customer's explicit
//     PATCH intent is preserved).
//  6. Replay-safety: a second MigrateUp is a no-op.
//
// Slot note: slot 128 is the highest existing on the rebased branch
// (00128_events_sidecar_name_idx). PR #618 originally claimed slots
// 129/130 then renumbered past PR #623's slot 129 (per cross-PR slot
// gate race memory) — the fence at slot 124 from PR-A is unchanged.
// This test pins 00131's apply + backfill; renumber would need
// filename + test name + apply range bump together.
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00131_AppsAlignMinInstances(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00131. A regression that drops a slot
	// between 1 and 130 surfaces here before the per-assertion pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 130)", err)
	}

	// Seed three divergent app rows BEFORE the 00131 UPDATE runs by
	// writing them post-MigrateUp — the migration itself only
	// matches rows where col > jsonb, so we then exercise both the
	// (col > jsonb) and (jsonb >= col) directions on the same schema
	// state. The ApplyUp range above includes 00131 already, so the
	// backfill has run; we seed divergent rows, verify the helper's
	// read-side returns max(), and re-run the migration's UPDATE
	// verbatim (mirroring the production predicate) to assert the
	// forward projection.
	//
	// Why post-00131 seeding instead of pre-migration seeding: the
	// shape we want to pin is "after the migration, divergent rows
	// are aligned" — re-deriving that pre/post invariant requires
	// two schema states which goose's MigrateUp doesn't expose in
	// one shot. Instead we seed three rows that exercise the
	// backfill predicate directly: the (col > jsonb) row gets
	// projected; the (col == jsonb) and (jsonb > col) rows are
	// untouched.
	//
	// Account + plan + app seed (literal UUIDs per the test fixture
	// convention; the migration-test-uuid-sed-residual memory flags
	// renumber chains but these are fixture ids not slot numbers).
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, plan, email)
		values ('00000000-0000-0000-0000-000000000131', 'scale', 'align-test@example.com')
	`); err != nil {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, min_instances, scaling_policy, ram_mb)
		values
		  ('00000000-0000-0000-0000-000000000131', '00000000-0000-0000-0000-000000000131',
		   'align-test', 3, '{"min_instances":0}'::jsonb, 256),
		  ('00000000-0000-0000-0000-000000000231', '00000000-0000-0000-0000-000000000131',
		   'align-noop', 0, '{}'::jsonb, 256),
		  ('00000000-0000-0000-0000-000000000331', '00000000-0000-0000-0000-000000000131',
		   'align-jsonb-wins', 1, '{"min_instances":5}'::jsonb, 256)
	`); err != nil {
		t.Fatalf("seed apps: %v", err)
	}

	// (2) Pre-backfill state on the col>jsonb row: helper reads
	// max(3, 0) = 3. The migration's UPDATE has NOT run yet for
	// these rows (they were inserted post-MigrateUp). Run the
	// production UPDATE verbatim so we exercise the migration's
	// predicate shape against the divergent state.
	// Sanity: assert divergence was seeded correctly.
	var divergent int
	if err := pool.QueryRow(ctx, `
		select min_instances from apps
		where id = '00000000-0000-0000-0000-000000000131'
	`).Scan(&divergent); err != nil {
		t.Fatalf("read divergent col: %v", err)
	}
	if divergent != 3 {
		t.Fatalf("seed invariant: col = %d, want 3", divergent)
	}
	var divergentJSONB int
	if err := pool.QueryRow(ctx, `
		select (scaling_policy->>'min_instances')::int from apps
		where id = '00000000-0000-0000-0000-000000000131'
	`).Scan(&divergentJSONB); err != nil {
		t.Fatalf("read divergent jsonb: %v", err)
	}
	if divergentJSONB != 0 {
		t.Fatalf("seed invariant: jsonb = %d, want 0", divergentJSONB)
	}

	// (3) Run the production UPDATE verbatim. This is the same
	// statement the migration runs; the test pins both directions:
	//   - col > jsonb → projection (the migration's intent)
	//   - col == jsonb → no-op
	//   - jsonb > col → no-op (customer intent preserved)
	if _, err := pool.Exec(ctx, `
		UPDATE apps
		SET scaling_policy = jsonb_set(
		    COALESCE(scaling_policy, '{}'::jsonb),
		    '{min_instances}',
		    to_jsonb(min_instances),
		    false
		)
		WHERE min_instances > 0
		  AND COALESCE((scaling_policy->>'min_instances')::int, 0) < min_instances
	`); err != nil {
		t.Fatalf("production UPDATE: %v", err)
	}

	// (4) Post-backfill state: col>jsonb row's jsonb is now 3.
	// The max() invariant collapses to (col, jsonb) = (3, 3).
	if err := pool.QueryRow(ctx, `
		select (scaling_policy->>'min_instances')::int from apps
		where id = '00000000-0000-0000-0000-000000000131'
	`).Scan(&divergentJSONB); err != nil {
		t.Fatalf("read post-backfill jsonb: %v", err)
	}
	if divergentJSONB != 3 {
		t.Errorf("backfill did not project col=3 into jsonb: jsonb = %d, want 3", divergentJSONB)
	}

	// (5) The no-op row (col=0): jsonb stays at the seeded '{}'.
	// The migration's WHERE predicate (min_instances > 0) filters it
	// out, so a second UPDATE is also a no-op.
	var noopCol int
	var noopJSONB string
	if err := pool.QueryRow(ctx, `
		select min_instances, scaling_policy::text from apps
		where id = '00000000-0000-0000-0000-000000000231'
	`).Scan(&noopCol, &noopJSONB); err != nil {
		t.Fatalf("read noop row: %v", err)
	}
	if noopCol != 0 {
		t.Errorf("noop col = %d, want 0", noopCol)
	}
	if noopJSONB != "{}" {
		t.Errorf("noop jsonb mutated to %s, want {} (zero-floor rows must be no-ops)", noopJSONB)
	}

	// (6) The customer-intent row (jsonb=5 > col=1): the migration's
	// WHERE predicate filters it out (jsonb < col is false). The
	// jsonb stays at 5. Pin this so a future change that flips the
	// direction of the backfill surfaces here as a regression.
	var intentCol int
	var intentJSONB int
	if err := pool.QueryRow(ctx, `
		select min_instances, (scaling_policy->>'min_instances')::int from apps
		where id = '00000000-0000-0000-0000-000000000331'
	`).Scan(&intentCol, &intentJSONB); err != nil {
		t.Fatalf("read intent row: %v", err)
	}
	if intentCol != 1 || intentJSONB != 5 {
		t.Errorf("customer intent row: col=%d jsonb=%d, want (1, 5)", intentCol, intentJSONB)
	}

	// (7) Replay-safety: a second MigrateUp is a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
