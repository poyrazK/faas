package meter

// sampler_floor_pgtest_test pins the synthetic-instance-ID schema
// round-trip end-to-end against a real Postgres instance
// (issue #515 / PR #516 / ADR-060). usage_minutes.instance_id is
// a UUID column (migrations/00001_init.sql:99) and
// PgStore.AppendUsage passes the ID raw. FloorInstanceID is the
// only synthetic-ID scheme that satisfies both the schema type
// AND first-write-wins idempotency on (instance_id, minute)
// across re-ticks; a literal "<appID>:floor:<i>" string would
// fail INSERT with 22P02 invalid_text_representation.
//
// pgtest.Open auto-skips when Postgres isn't reachable; on a
// dev box or in this worktree's local CI the test is a no-op
// (skip) and the deterministic-UUID pin in sampler_floor_test.go
// is the unit-suite defensive layer. The pgtest path is the
// production-closeness proof that no future schema change to
// usage_minutes.instance_id can break the floor silently — a
// 22P02 here fails CI before the change lands.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// floorPgStore stands up a fresh schema, migrates it, returns a
// PgStore + ctx. Mirrors pkg/state/pgstore_test.go::pgStore (the
// helper isn't exported so we have to replicate it; pkg/meter
// can't import the helper without dragging in pkg/state test
// internals).
func floorPgStore(t *testing.T) (*state.PgStore, context.Context) {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)
	return s, ctx
}

// seedFloorPgApp creates account + app + deployment in the real
// PgStore. Mirrors pkg/state/pgstore_test.go::seedLiveDeploy but
// uses Hobby plan (the floor is Hobby-only by PR-A's PATCH gate;
// the schema round-trip is the same regardless of plan).
func seedFloorPgApp(t *testing.T, s *state.PgStore, ctx context.Context) (acctID, appID string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "floor-pg@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "floor-pg", RAMMB: 256, Type: state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return acct.ID, app.ID
}

// TestPg_FloorInstanceID_PassesAppendUsage pins the
// schema-round-trip property of FloorInstanceID: the synthetic
// UUID v5 derived from (onebox-faas/meterd/floor/v1, appID, 0)
// must INSERT into usage_minutes.instance_id (a UUID column)
// without 22P02. A future refactor that swaps in a literal
// "<appID>:floor:<i>" string would surface here as a hard
// failure before the migration lands.
//
// Two writes for the same (floorID, minute) confirm the
// first-write-wins idempotency contract on the synthetic row:
// the second write is a no-op, not a 409, and mb_seconds is
// preserved.
func TestPg_FloorInstanceID_PassesAppendUsage(t *testing.T) {
	s, ctx := floorPgStore(t)
	acctID, appID := seedFloorPgApp(t, s, ctx)

	// Synthetic UUID v5 from the project-wide floor namespace.
	// Derivation here MUST stay byte-identical to FloorInstanceID:
	// the unit test (sampler_floor_test.go::TestFloorNamespaceFrozen)
	// pins the namespace string; this test pins the SQL round-trip.
	floorID := FloorInstanceID(appID, 0).String()
	if _, err := uuid.Parse(floorID); err != nil {
		t.Fatalf("FloorInstanceID return %q is not a valid UUID: %v", floorID, err)
	}
	if got := uuid.MustParse(floorID).Version(); got != 5 {
		t.Errorf("FloorInstanceID version = %d, want 5 (UUID v5)", got)
	}

	minute := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// Hobby billable = 264 MB; floor per-minute mb_seconds = 264 × 60 = 15_840.
	const hobbyBillablePerMinute = int64(264 * 60)

	// First write — wins.
	if err := s.AppendUsage(ctx, acctID, appID, floorID, minute,
		hobbyBillablePerMinute, 1, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("first AppendUsage floorID: %v (non-UUID would surface here as 22P02)", err)
	}
	// Redelivered minute — no-op on mb_seconds (first-wins).
	if err := s.AppendUsage(ctx, acctID, appID, floorID, minute,
		99_999, 99, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("redelivered AppendUsage floorID: %v", err)
	}

	// Read back via UsageByHour covering the minute.
	rows, err := s.UsageByHour(ctx, acctID,
		time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("UsageByHour: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].MBSeconds != hobbyBillablePerMinute {
		t.Errorf("MBSeconds = %d, want %d (first-write-wins on synthetic floor row)",
			rows[0].MBSeconds, hobbyBillablePerMinute)
	}
	if rows[0].Requests != 1 {
		t.Errorf("Requests = %d, want 1 (first-write-wins on requests column)",
			rows[0].Requests)
	}
}

// TestPg_FloorInstanceID_RejectsLiteralString is the negative
// counterpart of TestPg_FloorInstanceID_PassesAppendUsage: the
// literal "<appID>:floor:0" scheme, which the ADR-060 rejected
// alternatives section calls out, must fail INSERT with 22P02
// invalid_text_representation. A future ADR or PR that proposes
// "switch the synthetic ID to a literal colon-string for
// debuggability" would surface here as a regression test failure
// unless it also migrates the schema off UUID — at which point
// this whole test class becomes irrelevant and the contract is
// preserved by the schema type itself.
func TestPg_FloorInstanceID_RejectsLiteralString(t *testing.T) {
	s, ctx := floorPgStore(t)
	acctID, appID := seedFloorPgApp(t, s, ctx)

	bogusID := appID + ":floor:0" // the rejected alternative
	minute := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	err := s.AppendUsage(ctx, acctID, appID, bogusID, minute,
		15_840, 1, 0, 0, 0, 0, 0)
	if err == nil {
		t.Fatalf("AppendUsage(%q) succeeded; want 22P02 invalid_text_representation", bogusID)
	}
	// Don't over-assert on the exact Postgres error string —
	// different pgx versions surface the message differently.
	// The regression we're pinning is "string-shaped ID got
	// through", which is verified by err != nil.
}
