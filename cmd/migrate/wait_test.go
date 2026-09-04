package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// quietLogger is a slog.Logger that drops every record. The wait
// helper logs at every state transition; in tests we want the
// assertions to drive the pass/fail signal, not the log noise.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestWaitForMigrationsApplied_NotifyUnblocks is the happy path:
// the waiter subscribes first, the "leader" (a goroutine) inserts
// the required row, the migration_notify_trg (migration
// 00347) fires pg_notify('migrations_applied'), the waiter
// re-reads the ledger, observes the complete required set, and
// returns.
//
// This is the F2-B fix in test form: the non-leader path of
// `cmd/migrate -wait-for-migrations` stays parked until the
// leader's final migration lands, with no polling-spin on the
// connection pool and no race window between the SELECT MAX and
// the LISTEN.
func TestWaitForMigrationsApplied_NotifyUnblocks(t *testing.T) {
	pool := pgtest.Open(t)

	// Seed an empty ledger + a single applied row so MAX() is
	// defined. The waiter is asking for "have we reached v=1?";
	// the leader will land v=1 after a short delay.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS goose_db_version (
			id          bigserial PRIMARY KEY,
			version_id  bigint NOT NULL,
			is_applied  boolean NOT NULL,
			tstamp      timestamptz DEFAULT now()
		)`); err != nil {
		t.Fatalf("create goose_db_version: %v", err)
	}
	// No rows yet — MAX(version_id) = 0; the helper will subscribe.

	expected := []int64{1}

	// Capture the current MAX before subscribing so the test
	// asserts "waiter saw the leader's row materialise", not
	// "waiter saw the leader's row already there from a prior test".
	var before int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version`).Scan(&before); err != nil {
		t.Fatalf("scan before: %v", err)
	}
	if before != 0 {
		t.Fatalf("expected fresh ledger, got MAX=%d", before)
	}

	// Launch waiter.
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- WaitForMigrationsApplied(ctx, pool, expected, quietLogger())
	}()

	// Give the waiter time to subscribe. The helper subscribes
	// inside the function body (after the fast-path SELECT), so
	// a 50 ms sleep is enough on a healthy local Postgres; the
	// CI machines have 100 ms+ slack in their scheduling.
	time.Sleep(100 * time.Millisecond)

	// Simulate the leader inserting the final migration row.
	if _, err := pool.Exec(ctx,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, true)`,
		expected[0]); err != nil {
		t.Fatalf("leader INSERT: %v", err)
	}

	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("waiter did not unblock within 5s after leader INSERT")
	}

	// Confirm the ledger state at the end: exactly one applied row.
	var final int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version`).Scan(&final); err != nil {
		t.Fatalf("scan final: %v", err)
	}
	if final != expected[0] {
		t.Fatalf("expected ledger at v=%d, got v=%d", expected[0], final)
	}
}

// TestWaitForMigrationsApplied_NoOpIfAlreadyCurrent pins the fast
// path: if the ledger already contains the complete required set before the
// waiter subscribes, the helper returns immediately without
// subscribing. This is the dev-loop case where the operator reruns
// the waiter after the leader crashed and was restarted manually.
//
// Without this guard, the helper would subscribe to LISTEN, then
// sit in the select until the next leader commit (or the next
// poll tick) — wasting a connection and obscuring the "you're
// already current" status from the log.
func TestWaitForMigrationsApplied_NoOpIfAlreadyCurrent(t *testing.T) {
	pool := pgtest.Open(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS goose_db_version (
			id          bigserial PRIMARY KEY,
			version_id  bigint NOT NULL,
			is_applied  boolean NOT NULL,
			tstamp      timestamptz DEFAULT now()
		)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (1, true), (2, true)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	start := time.Now()
	if err := WaitForMigrationsApplied(ctx, pool, []int64{1, 2}, quietLogger()); err != nil {
		t.Fatalf("expected fast-path success, got: %v", err)
	}
	elapsed := time.Since(start)

	// The fast path does one SELECT and returns. Anything past
	// ~500 ms means we went through the LISTEN subscribe path
	// (which round-trips through Postgres) — a regression.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("fast-path took %s; expected <500ms", elapsed)
	}
}

// TestWaitForMigrationsApplied_RespectsContextCancel pins that a
// cancelled caller ctx stops the wait promptly. The helper must
// not race past ctx.Done() with the leader's notify — the
// select is the load-bearing primitive, and a stuck select on
// a daemon in shutdown is the worst-case resource leak.
//
// We assert wall-clock: cancel fires, helper returns within 1s.
// The notify-driven arm doesn't fire (we never insert); the
// ticker arm is 5s. ctx.Done() is the arm that wins.
func TestWaitForMigrationsApplied_RespectsContextCancel(t *testing.T) {
	pool := pgtest.Open(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS goose_db_version (
			id          bigserial PRIMARY KEY,
			version_id  bigint NOT NULL,
			is_applied  boolean NOT NULL,
			tstamp      timestamptz DEFAULT now()
		)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	expected := []int64{1, 5} // absent from the fresh ledger; helper must block.

	var (
		wg     sync.WaitGroup
		waitEr error
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitEr = WaitForMigrationsApplied(ctx, pool, expected, quietLogger())
	}()

	// Let the waiter reach the select{}.
	time.Sleep(100 * time.Millisecond)

	cancelAt := time.Now()
	cancel()
	wg.Wait()

	if !errors.Is(waitEr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", waitEr)
	}
	if elapsed := time.Since(cancelAt); elapsed > time.Second {
		t.Fatalf("waiter took %s to honour cancel; expected <1s", elapsed)
	}
}

// TestWaitForMigrationsApplied_NilPoolErrors pins the precondition
// guard. Without it, the helper would panic deep inside
// db.SubscribeWithReconnect, which is harder to attribute.
func TestWaitForMigrationsApplied_NilPoolErrors(t *testing.T) {
	if err := WaitForMigrationsApplied(context.Background(), nil, []int64{1}, quietLogger()); err == nil {
		t.Fatalf("nil pool: expected error, got nil")
	}
}

// TestWaitForMigrationsApplied_NoMigrationsNoOp pins the binary
// edge case: a development build ships zero migrations. The
// helper must return immediately and never touch the database.
// Without this guard, the fast-path SELECT against an empty
// ledger returns 0, and the helper would subscribe to LISTEN
// forever for a leader that will never run.
func TestWaitForMigrationsApplied_NoMigrationsNoOp(t *testing.T) {
	pool := pgtest.Open(t)

	start := time.Now()
	if err := WaitForMigrationsApplied(context.Background(), pool, nil, quietLogger()); err != nil {
		t.Fatalf("empty migration set: expected nil, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("empty-set fast-path took %s; expected <100ms", elapsed)
	}
}

// TestReadMissingAppliedMigrations_MaxDoesNotHideGap pins ADR-142's core
// readiness rule: a later timestamp in the ledger cannot hide an earlier
// migration that merged afterward.
func TestReadMissingAppliedMigrations_MaxDoesNotHideGap(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS goose_db_version (
			id          bigserial PRIMARY KEY,
			version_id  bigint NOT NULL,
			is_applied  boolean NOT NULL,
			tstamp      timestamptz DEFAULT now()
		)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	const later = int64(20260904150000999)
	if _, err := pool.Exec(ctx,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (1, true), ($1, true)`, later); err != nil {
		t.Fatalf("seed: %v", err)
	}

	missing, err := readMissingAppliedMigrations(ctx, pool, []int64{1, 2, later})
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if len(missing) != 1 || missing[0] != 2 {
		t.Fatalf("missing = %v, want [2]", missing)
	}
}

func TestReadMissingAppliedMigrations_LatestRollbackWins(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS goose_db_version (
			id          bigserial PRIMARY KEY,
			version_id  bigint NOT NULL,
			is_applied  boolean NOT NULL,
			tstamp      timestamptz DEFAULT now()
		)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (42, true), (42, false)`); err != nil {
		t.Fatalf("seed apply and rollback: %v", err)
	}

	missing, err := readMissingAppliedMigrations(ctx, pool, []int64{42})
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if len(missing) != 1 || missing[0] != 42 {
		t.Fatalf("missing = %v, want [42] after rollback", missing)
	}
}

// Compile-time check: the helper does not depend on any internal
// package state that would break in the test binary. Without it
// the linker silently drops the package-private helper in some
// test configurations.
var _ = db.NotifyMigrationsApplied
