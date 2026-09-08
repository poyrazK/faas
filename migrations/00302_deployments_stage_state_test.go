//go:build !no_pg

// Migration-apply test for 00302_deployments_stage_state.sql
// (ADR-117 — deploy-stage-progress).
//
// Pins:
//
//  1. Migration set applies cleanly through 00302 (no goose
//     duplicate-version panic). Slot 00302 was picked as the next
//     free slot on origin/main past the open-PR reservations
//     (00281–00301 — 00301 is taken by PR #984 (issue #977
//     deployment annotations); cross-PR precheck verified against
//     refs/pull/<N>/head before push per
//     scripts/ci/check_migration_slots.sh). Renumbered twice during
//     review: first 00288 → 00296 to dodge main's
//     `00296_reserve_slot.sql` fence for PR #986, then
//     00300 → 00302 after PR #984 (which had briefly claimed
//     00300) renumbered itself to 00301.
//  2. The new `deployments.stage_state` column carries the expected
//     default jsonb shape and is NOT NULL.
//  3. The CHECK `deployments_stage_state_current_check` accepts each
//     of the six closed vocabulary values
//     (source_download / dependency_restore / image_build /
//     security_scan / snapshot_prepare / readiness).
//  4. The CHECK rejects a typo ('imagee_build') with SQLSTATE 23514
//     check_violation. Pins the closed vocabulary contract — easy
//     confusion if a future contributor adds 'build_image' or
//     'snapshotting' (the internal micro-state enum) thinking it's
//     the user-visible name.
//  5. The CHECK constraint name is
//     `deployments_stage_state_current_check` — the auto-name
//     Postgres picks for the inline jsonb expression CHECK. If a
//     future migration renames the inline CHECK this pin must update
//     together (silent breakage here means 00302 becomes a no-op —
//     exactly the bug this test exists to catch).
//  6. Replay safety: re-running db.MigrateUp is a no-op (the
//     migration is replay-safe via `IF NOT EXISTS` + the DO-block
//     constraint guard).

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

// stageStateVocab is the closed vocabulary
// deployments_stage_state_current_check must carry after this
// migration. Adding a new value here without also widening the
// migration's IN list is a load-bearing failure mode — the wire
// vocabulary on `event: stage {name}` would silently drift out of
// step with the schema CHECK.
var stageStateVocab = []string{
	"source_download",
	"dependency_restore",
	"image_build",
	"security_scan",
	"snapshot_prepare",
	"readiness",
}

func TestMigrations_00302_DeploymentsStageState(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Apply through 00302.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot — 00301 reserve_slot fence on main must not collide)", err)
	}

	// (2) Default shape + NOT NULL.
	var (
		stageState []byte
		isNull     bool
	)
	err := pool.QueryRow(ctx, `
		select stage_state, stage_state IS NULL
		  from deployments
		 limit 1`).Scan(&stageState, &isNull)
	if err != nil {
		// Empty deployments table — insert a sentinel row so the
		// SELECT below can probe the default. A fresh pgtest
		// schema is empty, so we always land here.
		if _, ierr := pool.Exec(ctx, `
			insert into deployments (id, app_id, status)
			values ('00000000-0000-0000-0000-00000000302a',
			        '00000000-0000-0000-0000-00000000302a',
			        'pending')`); ierr != nil {
			t.Fatalf("insert sentinel deployment row: %v", ierr)
		}
		err = pool.QueryRow(ctx, `
			select stage_state, stage_state IS NULL
			  from deployments
			 where id = '00000000-0000-0000-0000-00000000302a'`).Scan(&stageState, &isNull)
		if err != nil {
			t.Fatalf("select stage_state default: %v", err)
		}
	}
	if isNull {
		t.Fatal("stage_state IS NULL after default backfill (column must be NOT NULL DEFAULT …)")
	}
	if !strings.Contains(string(stageState), `"source_download"`) {
		t.Errorf("stage_state default: missing source_download in %s (column DEFAULT must set the first stage of the 6-stage vocabulary)", string(stageState))
	}

	// (3) + (5) CHECK constraint shape + constraint name pin.
	var def string
	err = pool.QueryRow(ctx, `
		select pg_get_constraintdef(c.oid)
		  from pg_constraint c
		  join pg_namespace n on n.oid = c.connamespace
		 where c.conname = 'deployments_stage_state_current_check'
		   and n.nspname = current_schema()`).Scan(&def)
	if err != nil {
		t.Fatalf("query deployments_stage_state_current_check constraint: %v (closed-vocabulary CHECK must have landed)", err)
	}
	for _, v := range stageStateVocab {
		needle := "'" + v + "'"
		if !strings.Contains(def, needle) {
			t.Errorf("deployments_stage_state_current_check: missing %s in def %q (closed vocabulary must include all 6 values; a regression here means the CHECK was narrowed)", needle, def)
		}
	}

	// (4) Typo 'imagee_build' is rejected. Pins the closed
	// vocabulary contract — easy confusion if a future contributor
	// adds 'build_image' (a swap of the words) thinking it's a
	// synonym, or 'snapshotting' (the internal micro-state on
	// pkg/state/types.go) thinking it leaks to the wire.
	var stageStateTypoErr error
	if _, err := pool.Exec(ctx, `
		update deployments
		   set stage_state = jsonb_set(stage_state, '{current}', '"imagee_build"')
		 where id = '00000000-0000-0000-0000-00000000302a'`); err != nil {
		stageStateTypoErr = err
	}
	if stageStateTypoErr == nil {
		t.Fatal("update stage_state->>'current'='imagee_build': expected 23514 check_violation, got nil (closed vocabulary must reject typos)")
	}
	var pgErr *pgconn.PgError
	if !errors.As(stageStateTypoErr, &pgErr) {
		t.Fatalf("update stage_state typo: expected pgconn.PgError, got %T: %v", stageStateTypoErr, stageStateTypoErr)
	}
	if pgErr.Code != "23514" {
		t.Errorf("update stage_state typo: expected SQLSTATE 23514 check_violation, got %s (closed vocabulary contract)", pgErr.Code)
	}

	// (6) Replay safety: re-running db.MigrateUp is a no-op.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v (migration must be replay-safe — IF NOT EXISTS + DO-block constraint guard is the load-bearing carve-out)", err)
	}
}
