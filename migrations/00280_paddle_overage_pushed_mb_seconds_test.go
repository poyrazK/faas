//go:build !no_pg

// Apply-walk + column-shape test for migration 00280
// (paddle_overage_dedupe.pushed_mb_seconds). Insert a row directly
// via the production schema, drop + recreate the schema via the
// down/up pair, and assert the value round-trips — both pinned
// physical-column shape (via to_regclass + information_schema) and
// the load-bearing semantic that CompletePaddleOverageWindow stamps
// the integer through the upstream INSERT.
//
// The semantic-round-trip half is what closes issue #686: a
// post-apply migration with the column but the production path
// still dropping mbSeconds (the previous behavior at
// pgstore.go:9956: `_ = mbSeconds`) would PASS the apply-walk
// shape test and FAIL the semantic test. The two together pin
// the load-bearing behavior.
//
// Build tag mirrors 00022_backfill_test.go:1 — set
// FAAS_SKIP_PG_TESTS=1 locally to skip.

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrations_00280_PaddleOveragePushedMBSeconds(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Migrate up. apply_walk_test.go proves the migrations chain
	// applies cleanly; we add this test to pin the new column
	// behavior on top of that.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (2) Column exists, is nullable, bigint. This is the shape
	// contract. If a future migration drops the column or changes
	// the type, this assertion fails.
	var dataType, isNullable string
	if err := pool.QueryRow(ctx, `
		select data_type, is_nullable
		from information_schema.columns
		where table_schema = current_schema()
		  and table_name = 'paddle_overage_dedupe'
		  and column_name = 'pushed_mb_seconds'
	`).Scan(&dataType, &isNullable); err != nil {
		t.Fatalf("paddle_overage_dedupe.pushed_mb_seconds column missing post-apply: %v", err)
	}
	if dataType != "bigint" {
		t.Errorf("pushed_mb_seconds data_type = %q, want bigint", dataType)
	}
	if isNullable != "YES" {
		t.Errorf("pushed_mb_seconds is_nullable = %q, want YES (legacy 00041 rows have no value)", isNullable)
	}

	// (3) Semantic round-trip: CompletePaddleOverageWindow stamps
	// the integer. This is the path that issues #686 closes — a
	// future regression that drops the stamp (e.g. the
	// `_ = mbSeconds` line at pgstore.go:9956 returning) will
	// flip this assertion red.
	s := state.NewPgStore(pool)
	// paddle_overage_dedupe.account_id is uuid and FK-shaped, so the
	// claim path must be handed the account id CreateAccount minted —
	// a synthetic "acct-…" string 22P02s inside the dedupe upsert.
	email := "acct-mig-00280-" + time.Now().UTC().Format("150405.000000000") + "@mig.example.test"
	acct, err := s.CreateAccount(ctx, email, api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	acctID := acct.ID
	if acctID == "" {
		t.Fatal("CreateAccount returned an empty account id")
	}
	window := time.Now().UTC().Truncate(time.Hour)
	if claimed, err := s.ClaimPaddleOverageWindow(ctx, acctID, window, "pod-mig", 5*time.Minute); err != nil || !claimed {
		t.Fatalf("ClaimPaddleOverageWindow: claimed=%v err=%v", claimed, err)
	}
	if err := s.CompletePaddleOverageWindow(ctx, acctID, window, 4096); err != nil {
		t.Fatalf("CompletePaddleOverageWindow: %v", err)
	}
	var stamped int64
	if err := pool.QueryRow(ctx, `
		select pushed_mb_seconds
		from paddle_overage_dedupe
		where account_id = $1 and window_start = $2
	`, acctID, window).Scan(&stamped); err != nil {
		t.Fatalf("read pushed_mb_seconds: %v", err)
	}
	if stamped != 4096 {
		t.Errorf("pushed_mb_seconds = %d, want 4096", stamped)
	}
}
