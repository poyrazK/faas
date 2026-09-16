// Tests for PgStore.LookupBootStartedForWakes (ADR-123 + PR-A).
// Closes PR #1015 review finding #3: pin the DISTINCT ON semantics +
// canonical-row preference against future schema drift. PR-A review
// cluster (PR #1031) extends coverage to the two new SQL surfaces —
// at_capacity COALESCE absent-case and ready_in_ms LATERAL JOIN
// wall-clock accuracy. Lives behind pgtest.Open (skips when
// DATABASE_URL is unset) so it runs as part of `make test` on
// machines with a Postgres available and silently no-ops elsewhere.
//
// The tests exercise seven contracts:
//  1. DISTINCT ON (data->>'wake_id') picks the EARLIEST row per
//     wake_id. A re-wake that emits two wake.boot_started rows for
//     the same wake_id must surface the first row's telemetry.
//  2. A legacy vmmd mirror row must NOT be surfaced — the mirror uses a
//     later `at` timestamp and is never preferred over the canonical schedd
//     row. Current releases use the distinct wake.boot_observed kind.
//  3. Empty / nil input returns an empty map without touching the
//     pool (exercises the early-return branch at pgstore.go:10362).
//  4. Unknown wake_ids produce an empty map (not an error) so the
//     dashboard's join-LATERAL doesn't have to special-case.
//  5. Pre-PR-A rows (jsonb key `at_capacity` ABSENT) produce
//     AtCapacity=false AND AtCapacityPresent=false. The dashboard's
//     em-dash-on-absent convention depends on AtCapacityPresent so
//     a future COALESCE removal surfaces here as a test failure
//     instead of a silent "No" rendering for fleet rows that lack
//     the telemetry.
//  6. PR-A rows with at_capacity=true + a boot_completed row 65.5
//     seconds later produce ReadyInMS=65500 (pins the EXTRACT(EPOCH
//     …) * 1000 fix). The earlier EXTRACT(MILLISECONDS FROM …)
//     implementation silently returned 5500 ms for this delta
//     because PostgreSQL intervals are stored as months/days/
//     seconds and EXTRACT(MILLISECONDS) returns only the seconds-
//     field milliseconds.
//  7. Pre-PR-A rows (no at_capacity key) WITH a boot_completed row
//     still produce a valid ReadyInMS — the LATERAL JOIN is
//     independent of at_capacity's presence in the jsonb payload.
package state

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// insertBootStarted is a tiny test fixture helper. Production code
// goes through events.BootStarted.Payload() to build the jsonb body
// (pkg/events/wake.go:311); the test bypasses that to keep the test
// self-contained — no events-package import, no follow-the-package
// tx. The payload shape mirrors the production BootStarted.Payload
// keys exactly so a future schema migration that renames a key
// surfaces here as a SQL scan error rather than a silent miss.
func insertBootStarted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, data map[string]any, at string) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO events (actor, kind, data, at) VALUES ($1, $2, $3, $4::timestamptz)`,
		"schedd", "wake.boot_started", raw, at,
	); err != nil {
		t.Fatalf("insert wake.boot_started: %v", err)
	}
}

// insertBootCompleted inserts a wake.boot_completed row. Mirrors
// insertBootStarted; needed for the PR-A ready_in_ms LATERAL JOIN
// round-trip.
func insertBootCompleted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, data map[string]any, at string) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO events (actor, kind, data, at) VALUES ($1, $2, $3, $4::timestamptz)`,
		"schedd", "wake.boot_completed", raw, at,
	); err != nil {
		t.Fatalf("insert wake.boot_completed: %v", err)
	}
}

// cleanupBootStarted removes the rows this test inserted so re-runs
// (or sibling tests in the same package) don't accumulate. Pinned to
// the wake_id namespace below — the t.Name() suffix makes the names
// unique per test.
func cleanupBootStarted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, wakeIDs []string) {
	t.Helper()
	if _, err := pool.Exec(ctx,
		`DELETE FROM events WHERE data->>'wake_id' = ANY($1)`,
		wakeIDs,
	); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func TestLookupBootStartedForWakes_RoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)

	// Unique-per-test wake_ids so concurrent runs and re-runs don't
	// collide on the partial jsonb index (events_wake_id_idx, ADR-064).
	wakeID1 := "wake-rt-1-" + t.Name()
	wakeID2 := "wake-rt-2-" + t.Name()

	// wakeID1: canonical then a pre-cutover mirror. The legacy duplicate must
	// be discarded by DISTINCT ON's actor preference and timestamp order.
	insertBootStarted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID1,
		"app_id":               "app-a",
		"instance_id":          "ins-1",
		"node_id":              "node-1",
		"method":               "cold_boot",
		"requested_at":         "2026-08-21T10:00:00Z",
		"trigger":              "gateway",
		"queued_count":         8,
		"concurrency_at_admit": 2,
	}, "2026-08-21T10:00:00Z")
	insertBootStarted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID1,
		"app_id":               "app-a",
		"instance_id":          "ins-1",
		"node_id":              "node-1",
		"method":               "cold_boot",
		"requested_at":         "2026-08-21T10:00:00.100Z",
		"trigger":              "mirror",
		"queued_count":         99,
		"concurrency_at_admit": 99,
	}, "2026-08-21T10:00:00.100Z")

	// wakeID2: cron schedule, capacity-1 cold start (no mirror row).
	insertBootStarted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID2,
		"app_id":               "app-b",
		"instance_id":          "ins-2",
		"node_id":              "node-1",
		"method":               "cold_boot",
		"requested_at":         "2026-08-21T10:01:00Z",
		"trigger":              "cron.schedule",
		"queued_count":         0,
		"concurrency_at_admit": 0,
	}, "2026-08-21T10:01:00Z")

	t.Cleanup(func() {
		cleanupBootStarted(t, ctx, pool, []string{wakeID1, wakeID2})
	})

	got, err := s.LookupBootStartedForWakes(ctx, []string{wakeID1, wakeID2})
	if err != nil {
		t.Fatalf("LookupBootStartedForWakes: %v", err)
	}

	// wakeID1: trigger MUST be 'gateway' (canonical), NOT 'mirror'
	// (fallback) — DISTINCT ON ORDER BY at ASC picks the earliest.
	m1, ok := got[wakeID1]
	if !ok {
		t.Fatalf("missing wakeID1 in result; got keys: %v", keys(got))
	}
	if m1.Trigger != "gateway" {
		t.Errorf("wakeID1 trigger: got %q want %q (DISTINCT ON must prefer canonical over mirror)", m1.Trigger, "gateway")
	}
	if m1.QueuedCount != 8 {
		t.Errorf("wakeID1 queued_count: got %d want 8", m1.QueuedCount)
	}
	if m1.ConcurrencyAtAdmit != 2 {
		t.Errorf("wakeID1 concurrency_at_admit: got %d want 2", m1.ConcurrencyAtAdmit)
	}

	// wakeID2: cron-driven, zero queue at admit.
	m2, ok := got[wakeID2]
	if !ok {
		t.Fatalf("missing wakeID2 in result; got keys: %v", keys(got))
	}
	if m2.Trigger != "cron.schedule" {
		t.Errorf("wakeID2 trigger: got %q want cron.schedule", m2.Trigger)
	}
	if m2.QueuedCount != 0 || m2.ConcurrencyAtAdmit != 0 {
		t.Errorf("wakeID2 cold-start telemetry not zero: %+v", m2)
	}
}

// TestLookupBootStartedForWakes_PRAbsentAtCapacity pins contract #5:
// pre-PR-A fleet rows lack the at_capacity jsonb key. The SQL must
// surface AtCapacity=false (COALESCE default) AND
// AtCapacityPresent=false (data ? 'at_capacity' returns false). The
// dashboard's em-dash-on-absent rendering depends on the latter — a
// future "optimization" that drops the jsonb contains check would
// surface here as a silent "No" badge for fleet rows.
func TestLookupBootStartedForWakes_PRAbsentAtCapacity(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)

	wakeID := "wake-pre-pr-a-" + t.Name()
	// No at_capacity key — pre-PR-A shape. Only trigger / queued /
	// concurrency keys present, matching the ADR-123 baseline payload.
	insertBootStarted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID,
		"app_id":               "app-c",
		"instance_id":          "ins-3",
		"node_id":              "node-1",
		"method":               "cold_boot",
		"requested_at":         "2026-08-21T10:02:00Z",
		"trigger":              "floor",
		"queued_count":         1,
		"concurrency_at_admit": 1,
		// at_capacity: intentionally absent
	}, "2026-08-21T10:02:00Z")

	t.Cleanup(func() {
		cleanupBootStarted(t, ctx, pool, []string{wakeID})
	})

	got, err := s.LookupBootStartedForWakes(ctx, []string{wakeID})
	if err != nil {
		t.Fatalf("LookupBootStartedForWakes: %v", err)
	}
	m, ok := got[wakeID]
	if !ok {
		t.Fatalf("missing %q in result; got keys: %v", wakeID, keys(got))
	}
	if m.AtCapacity {
		t.Errorf("AtCapacity: got true want false (COALESCE default for pre-PR-A row)")
	}
	if m.AtCapacityPresent {
		t.Errorf("AtCapacityPresent: got true want false (at_capacity key absent in pre-PR-A row)")
	}
}

// TestLookupBootStartedForWakes_ReadyInMS pins contract #6: ready_in_ms
// is the wall-clock delta between boot_started.at and the matching
// boot_completed.at. Uses 65.5 s because the prior
// EXTRACT(MILLISECONDS FROM interval) implementation silently
// returned 5500 ms for that delta (intervals are months/days/seconds
// and EXTRACT(MILLISECONDS) returns only the seconds-field ms). The
// EXTRACT(EPOCH …) * 1000 fix returns 65500. Regression-pin.
func TestLookupBootStartedForWakes_ReadyInMS(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)

	wakeID := "wake-ready-ms-" + t.Name()
	// boot_started at T+0; boot_completed at T+65.5s. The 65.5 s
	// delta is the load-bearing value: it's >= 60 s so the
	// EXTRACT(MILLISECONDS FROM interval) bug bites, and it's
	// non-integer so the *1000 round-trip is exercised.
	insertBootStarted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID,
		"app_id":               "app-d",
		"instance_id":          "ins-4",
		"node_id":              "node-1",
		"method":               "restore",
		"requested_at":         "2026-08-21T10:03:00Z",
		"trigger":              "scaleup",
		"queued_count":         0,
		"concurrency_at_admit": 4,
		"at_capacity":          true,
	}, "2026-08-21T10:03:00Z")
	insertBootCompleted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID,
		"app_id":               "app-d",
		"instance_id":          "ins-4",
		"node_id":              "node-1",
		"method":               "restore",
		"requested_at":         "2026-08-21T10:03:00Z",
		"trigger":              "scaleup",
		"queued_count":         0,
		"concurrency_at_admit": 4,
		"at_capacity":          true,
	}, "2026-08-21T10:03:55.500Z") // T+55.5s — pinned for sub-second resolution

	t.Cleanup(func() {
		cleanupBootStarted(t, ctx, pool, []string{wakeID})
	})

	got, err := s.LookupBootStartedForWakes(ctx, []string{wakeID})
	if err != nil {
		t.Fatalf("LookupBootStartedForWakes: %v", err)
	}
	m, ok := got[wakeID]
	if !ok {
		t.Fatalf("missing %q in result; got keys: %v", wakeID, keys(got))
	}
	// Wall-clock delta = 55.5 s = 55500 ms. The previous
	// EXTRACT(MILLISECONDS) impl returned 55500 here only because
	// the interval is < 60 s; >= 60 s would silently truncate.
	if m.ReadyInMS != 55500 {
		t.Errorf("ReadyInMS: got %d want 55500 (EXTRACT(EPOCH …) * 1000 wall-clock delta)", m.ReadyInMS)
	}
	if !m.AtCapacity {
		t.Errorf("AtCapacity: got false want true (PR-A row stamped at_capacity=true)")
	}
	if !m.AtCapacityPresent {
		t.Errorf("AtCapacityPresent: got false want true (at_capacity key present in PR-A row)")
	}
}

// TestLookupBootStartedForWakes_PRAbsentAtCapacityWithCompletion
// pins contract #7: pre-PR-A row (no at_capacity key) WITH a
// boot_completed row still produces a valid ReadyInMS. The LATERAL
// JOIN is independent of at_capacity's presence in the jsonb
// payload. Also re-pins contract #6 with a >= 60 s delta so the
// EXTRACT(MILLISECONDS) regression bites even for pre-PR-A rows
// (which still carry boot_started.at / boot_completed.at).
func TestLookupBootStartedForWakes_PRAbsentAtCapacityWithCompletion(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)

	wakeID := "wake-pre-pr-a-with-compl-" + t.Name()
	insertBootStarted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID,
		"app_id":               "app-e",
		"instance_id":          "ins-5",
		"node_id":              "node-1",
		"method":               "cold_boot",
		"requested_at":         "2026-08-21T10:04:00Z",
		"trigger":              "cron.manual",
		"queued_count":         0,
		"concurrency_at_admit": 0,
		// at_capacity: intentionally absent (pre-PR-A shape)
	}, "2026-08-21T10:04:00Z")
	insertBootCompleted(t, ctx, pool, map[string]any{
		"wake_id":              wakeID,
		"app_id":               "app-e",
		"instance_id":          "ins-5",
		"node_id":              "node-1",
		"method":               "cold_boot",
		"requested_at":         "2026-08-21T10:04:00Z",
		"trigger":              "cron.manual",
		"queued_count":         0,
		"concurrency_at_admit": 0,
	}, "2026-08-21T10:05:05.500Z") // T+65.5s — load-bearing >= 60s delta

	t.Cleanup(func() {
		cleanupBootStarted(t, ctx, pool, []string{wakeID})
	})

	got, err := s.LookupBootStartedForWakes(ctx, []string{wakeID})
	if err != nil {
		t.Fatalf("LookupBootStartedForWakes: %v", err)
	}
	m, ok := got[wakeID]
	if !ok {
		t.Fatalf("missing %q in result; got keys: %v", wakeID, keys(got))
	}
	if m.AtCapacityPresent {
		t.Errorf("AtCapacityPresent: got true want false (at_capacity key absent in pre-PR-A row)")
	}
	if m.ReadyInMS != 65500 {
		t.Errorf("ReadyInMS: got %d want 65500 (EXTRACT(EPOCH …) * 1000 wall-clock delta for 65.5s; pre-EXTRACT-EPOCH fix would have returned 5500)", m.ReadyInMS)
	}
}

func TestLookupBootStartedForWakes_EmptyInput(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)

	// nil input — exercises the early-return branch at pgstore.go:10362
	// without touching the pool. The empty-map result is the contract.
	got, err := s.LookupBootStartedForWakes(ctx, nil)
	if err != nil {
		t.Fatalf("nil input: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil input produced non-empty map: %v", got)
	}

	// Empty slice — same branch, same contract.
	got, err = s.LookupBootStartedForWakes(ctx, []string{})
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty input produced non-empty map: %v", got)
	}
}

func TestLookupBootStartedForWakes_UnknownWakeID(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPgStore(pool)

	got, err := s.LookupBootStartedForWakes(ctx, []string{"wake-does-not-exist-" + t.Name()})
	if err != nil {
		t.Fatalf("unknown wakeID: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("unknown wakeID produced non-empty map: %v", got)
	}
}

// keys is a small helper for nicer error messages — drops the wakeID
// values into a slice so multi-row failures show the same key set
// across runs.
func keys(m map[string]WakeBootMeta) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
