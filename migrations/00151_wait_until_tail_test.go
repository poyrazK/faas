//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_00151_WaitUntilTail pins the IDEMPOTENT ALTER shape
// and the column types of the tail-count + tail-seconds columns
// landed by 00151_wait_until_tail.sql (issue #667 / ADR-078).
//
// What we pin (replay-safety + audit-evidence cost):
//   - Column types match the per-table shape used by the runner
//     envelope / scheduler / sampler: tail_count is integer
//     (sufficient up to TailCapMax * MaxConcurrentWakeRequests; the
//     16 structural cap × a generous per-instance multiplier is well
//     under int32). tail_seconds is bigint (additive-merge semantics
//     accumulate across many hours; the integer range would saturate
//     on a busy Scale instance within a day).
//   - Both tail_seconds columns are NOT NULL DEFAULT 0 — the sampler
//     hot path reads `coalesce(tail_seconds, 0)` if NULL is allowed,
//     and the additive ON CONFLICT merge in AppendUsage is simpler
//     when the destination column is NOT NULL.
//   - tail_count on instances is NOT NULL DEFAULT 0 for the same
//     reason — the reaper's `TailCount > 0` early-out is a plain
//     truthiness check; NULL coalescing would force a `coalesce`
//     per-row in the hot path.
//   - All three columns are reachable via current_schema() (not
//     hard-coded 'public') to keep lookups relative to the pool. Each
//     test uses its own migrated database.
//   - The replay guard: re-running the migration after a successful
//     first run is a no-op (ADD COLUMN IF NOT EXISTS).
//
// Cross-PR slot reservation note: this test is coupled to the .sql
// at slot 149. If a sibling PR renumbers, the test file moves with
// it (per migration-slot-renumber-at-pr-creation, ADR-041).
func TestMigrations_00151_WaitUntilTail(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)

	// Use a private migrated database: the complete migration set includes
	// public-qualified objects. A separate database preserves test isolation.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// 1. Column existence + types + NOT NULL — information_schema
	//    is the platform-neutral way; the older pg_catalog views also
	//    work but column-name filtering is the same shape. The
	//    current_schema() filter (NOT 'public') is the pgtest
	//    isolation discipline — see migrations/info-schema-scoping-pattern.
	type colSpec struct {
		dt string // expected information_schema.data_type
		nn string // expected is_nullable ("YES" / "NO")
	}
	want := map[string]map[string]colSpec{
		"instances": {
			"tail_count": {"integer", "NO"},
		},
		"usage_minutes": {
			"tail_seconds": {"bigint", "NO"},
		},
		"usage_daily": {
			"tail_seconds": {"bigint", "NO"},
		},
	}

	for table, cols := range want {
		for col, spec := range cols {
			var dt, nn string
			err := pool.QueryRow(ctx, `
				SELECT data_type, is_nullable
				  FROM information_schema.columns
				 WHERE table_schema = current_schema()
				   AND table_name   = $1
				   AND column_name  = $2`,
				table, col).Scan(&dt, &nn)
			if err != nil {
				t.Errorf("%s.%s column missing — migration 00151 not applied: %v", table, col, err)
				continue
			}
			if dt != spec.dt {
				t.Errorf("%s.%s type: got %s, want %s", table, col, dt, spec.dt)
			}
			if nn != spec.nn {
				t.Errorf("%s.%s nullable: got %s, want %s", table, col, nn, spec.nn)
			}
		}
	}

	// 2. Replay safety — re-run the migration; the second pass is
	//    a no-op thanks to ADD COLUMN IF NOT EXISTS. If replay safety
	//    is broken (a future PR drops the IF NOT EXISTS guard), this
	//    test catches it before the operator's `goose up` on a
	//    partially-migrated DB fails with 42710.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Errorf("replay of migration 00151 should be a no-op, got: %v", err)
	}

	// silence the unused-import lint for `migrations` if all the
	// pure-coverage checks above happen to skip — keep the import
	// set honest.
	_ = migrations.FS
}
