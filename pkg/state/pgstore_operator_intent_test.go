//go:build !no_pg

// pgstore_operator_intent_test.go — operator_intents table CRUD
// against a real Postgres (pgtest.Open). Pins:
//
//  1. InsertOperatorIntent → row exists with status='pending'.
//  2. ClaimPendingOperatorIntent returns the same row with
//     status='running' + started_at stamped.
//  3. SKIP LOCKED semantics: a second Claim while the first
//     row is `running` returns ErrOperatorIntentNotFound
//     (the running row is invisible to the next claim).
//  4. MarkOperatorIntentSucceeded → status='succeeded' +
//     finished_at + snap_ids_marked_stale stamped.
//  5. MarkOperatorIntentFailed → status='failed' + error +
//     finished_at stamped.
//  6. GetOperatorIntent returns the row by id;
//     ErrOperatorIntentNotFound for missing id.
//  7. Replay-safe: a second claim after MarkSucceeded returns
//     ErrOperatorIntentNotFound (the row is terminal).
//  8. ReclaimStuckRunningOperatorIntents resets rows older
//     than the threshold back to `pending`, leaves terminal
//     rows alone, and is idempotent on a second call.
//
// Build tag matches the rest of the pgstore tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgStoreOperatorIntent(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.Open(t)
	// These tests exercise the PgStore CRUD contract, while the migration
	// package pins the production schema. Installing only the table keeps each
	// isolated fixture cheap; replaying all migrations for every test pushes
	// the PostgreSQL shard over its wall-clock budget.
	if _, err := pool.Exec(ctx, `
		CREATE TABLE operator_intents (
			id uuid PRIMARY KEY,
			kind text NOT NULL CHECK (kind IN
				('force_park', 'force_cold_boot', 'force_restart')),
			target_id text NOT NULL,
			account_id uuid NULL,
			actor_id uuid NOT NULL,
			reason text NULL,
			metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
			status text NOT NULL DEFAULT 'pending' CHECK (status IN
				('pending', 'running', 'succeeded', 'failed', 'cancelled')),
			requested_at timestamptz NOT NULL DEFAULT now(),
			started_at timestamptz NULL,
			finished_at timestamptz NULL,
			error text NULL,
			snap_ids_marked_stale text[] NULL,
			trace_id text NULL
				CHECK (trace_id IS NULL OR trace_id ~ '^[0-9a-f]{32}$')
		);
		CREATE INDEX operator_intents_pending_idx
			ON operator_intents (status, requested_at)
			WHERE status = 'pending';
		CREATE INDEX operator_intents_target_idx
			ON operator_intents (target_id, requested_at DESC);
		CREATE INDEX operator_intents_trace_idx
			ON operator_intents (trace_id)
			WHERE trace_id IS NOT NULL;
	`); err != nil {
		t.Fatalf("create operator_intents fixture: %v", err)
	}
	return state.NewPgStore(pool), pool, ctx
}

func TestPgStore_OperatorIntent_ListByTraceID(t *testing.T) {
	store, _, ctx := pgStoreOperatorIntent(t)
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	otherTraceID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	actor := "22222222-2222-2222-2222-222222222222"
	if _, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindForcePark,
		"target-a", nil, actor, "matching", nil, &traceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertOperatorIntent(ctx, state.OperatorIntentKindForcePark,
		"target-b", nil, actor, "unrelated", nil, &otherTraceID); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListOperatorIntentsByTraceID(ctx, traceID, 10)
	if err != nil || len(rows) != 1 || rows[0].Reason != "matching" {
		t.Fatalf("ListOperatorIntentsByTraceID = (%v, %v), want one exact match", rows, err)
	}
}

func TestPgStore_OperatorIntent_FullLifecycle(t *testing.T) {
	store, _, ctx := pgStoreOperatorIntent(t)

	// 1. Insert.
	acct := "11111111-1111-1111-1111-111111111111"
	actor := "22222222-2222-2222-2222-222222222222"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForcePark,
		"33333333-3333-3333-3333-333333333333",
		&acct, actor, "wedged instance", json.RawMessage(`{}`), nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	if id == "" {
		t.Fatalf("InsertOperatorIntent returned empty id")
	}

	// 2. Claim.
	got, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("ClaimPendingOperatorIntent: %v", err)
	}
	if got.ID != id {
		t.Errorf("Claim returned id=%q, want %q", got.ID, id)
	}
	if got.Status != state.OperatorIntentRunning {
		t.Errorf("Claim status=%q, want running", got.Status)
	}
	if got.StartedAt == nil {
		t.Errorf("Claim did not stamp started_at")
	}

	// 3. SKIP LOCKED — second claim returns
	// ErrOperatorIntentNotFound (the running row is
	// invisible to the next claim).
	if _, err := store.ClaimPendingOperatorIntent(ctx); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("second claim while running: got %v, want ErrOperatorIntentNotFound", err)
	}

	// 4. Mark succeeded.
	if err := store.MarkOperatorIntentSucceeded(ctx, id, []string{"snap-1", "snap-2"}); err != nil {
		t.Fatalf("MarkOperatorIntentSucceeded: %v", err)
	}

	// 5. Get returns terminal state.
	got2, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("GetOperatorIntent: %v", err)
	}
	if got2.Status != state.OperatorIntentSucceeded {
		t.Errorf("Get status=%q, want succeeded", got2.Status)
	}
	if got2.FinishedAt == nil {
		t.Errorf("Get did not stamp finished_at")
	}
	if len(got2.SnapIDsMarkedStale) != 2 || got2.SnapIDsMarkedStale[0] != "snap-1" {
		t.Errorf("Get snap_ids=%v, want [snap-1 snap-2]", got2.SnapIDsMarkedStale)
	}

	// 6. Replay-safe: third claim returns
	// ErrOperatorIntentNotFound (the row is terminal,
	// invisible to the claim query).
	if _, err := store.ClaimPendingOperatorIntent(ctx); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("claim after succeeded: got %v, want ErrOperatorIntentNotFound", err)
	}
}

func TestPgStore_OperatorIntent_FailurePath(t *testing.T) {
	store, _, ctx := pgStoreOperatorIntent(t)

	acct := "11111111-1111-1111-1111-111111111111"
	actor := "22222222-2222-2222-2222-222222222222"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForceColdBoot,
		"44444444-4444-4444-4444-444444444444",
		&acct, actor, "stale snap", nil, nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}

	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := store.MarkOperatorIntentFailed(ctx, id, "deployment not found", nil); err != nil {
		t.Fatalf("MarkOperatorIntentFailed: %v", err)
	}

	got, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("GetOperatorIntent: %v", err)
	}
	if got.Status != state.OperatorIntentFailed {
		t.Errorf("status=%q, want failed", got.Status)
	}
	if got.Error != "deployment not found" {
		t.Errorf("error=%q, want %q", got.Error, "deployment not found")
	}
}

func TestPgStore_OperatorIntent_GetNotFound(t *testing.T) {
	store, _, ctx := pgStoreOperatorIntent(t)

	_, err := store.GetOperatorIntent(ctx, "deadbeef-dead-beef-dead-beefdeadbeef")
	if !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("GetOperatorIntent(missing): got %v, want ErrOperatorIntentNotFound", err)
	}
}

func TestPgStore_OperatorIntent_NilAccountID(t *testing.T) {
	store, _, ctx := pgStoreOperatorIntent(t)

	actor := "22222222-2222-2222-2222-222222222222"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForcePark,
		"55555555-5555-5555-5555-555555555555",
		nil, actor, "fleet-level", json.RawMessage(`{}`), nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}

	got, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("GetOperatorIntent: %v", err)
	}
	if got.AccountID != nil {
		t.Errorf("AccountID=%v, want nil for fleet-level", got.AccountID)
	}
}

// TestPgStore_OperatorIntent_ReclaimStuckRunning seeds one
// row in `running`, calls ReclaimStuckRunningOperatorIntents
// with a threshold older than started_at, then asserts:
//
//   - the row's status is back to 'pending' + started_at is
//     NULL (the reclaim path stamps NULL because the row is
//     no longer in-flight);
//   - a follow-up ClaimPendingOperatorIntent picks the same
//     row up (round-trip into the dispatch path is unblocked);
//   - a second reclaim with the same threshold returns 0
//     (idempotency);
//   - a separately-stamped MarkSucceeded row is NOT touched
//     (terminal rows are not reclaimed — only `running`).
func TestPgStore_OperatorIntent_ReclaimStuckRunning(t *testing.T) {
	store, pool, ctx := pgStoreOperatorIntent(t)

	actor := "33333333-3333-3333-3333-333333333333"
	accountID := "44444444-4444-4444-4444-444444444444"

	// Stuck-running row: claim it (status -> running,
	// started_at -> now()), then back-date started_at via a
	// direct UPDATE so the reclaim threshold (now - 5min)
	// selects it. We can't time-travel without a direct
	// UPDATE; the pgstore surface only exposes
	// Mark{Success,Failed}, both of which transition to a
	// terminal state and would defeat the test.
	stuckID, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForcePark,
		"66666666-6666-6666-6666-666666666666",
		&accountID, actor, "stuck", json.RawMessage(`{}`), nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent(stuck): %v", err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("ClaimPendingOperatorIntent(stuck): %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE operator_intents SET started_at = now() - INTERVAL '10 minutes' WHERE id = $1`,
		stuckID); err != nil {
		t.Fatalf("back-date started_at: %v", err)
	}

	// Terminal row: insert + claim + MarkSucceeded. The
	// reclaim must leave this row alone (the WHERE clause
	// filters on status='running').
	terminalID, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForceColdBoot,
		"77777777-7777-7777-7777-777777777777",
		&accountID, actor, "terminal", json.RawMessage(`{}`), nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent(terminal): %v", err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("ClaimPendingOperatorIntent(terminal): %v", err)
	}
	if err := store.MarkOperatorIntentSucceeded(ctx, terminalID, []string{"s1"}); err != nil {
		t.Fatalf("MarkOperatorIntentSucceeded(terminal): %v", err)
	}

	// Reclaim with a 5-min cutoff — the stuck row's started_at
	// is 10 min old so it qualifies; the terminal row stays.
	threshold := time.Now().Add(-5 * time.Minute)
	n, err := store.ReclaimStuckRunningOperatorIntents(ctx, threshold)
	if err != nil {
		t.Fatalf("ReclaimStuckRunningOperatorIntents: %v", err)
	}
	if n != 1 {
		t.Errorf("reclaim count = %d, want 1", n)
	}

	stuck, err := store.GetOperatorIntent(ctx, stuckID)
	if err != nil {
		t.Fatalf("GetOperatorIntent(stuck): %v", err)
	}
	if stuck.Status != state.OperatorIntentPending {
		t.Errorf("stuck.Status = %q, want pending", stuck.Status)
	}
	if stuck.StartedAt != nil {
		t.Errorf("stuck.StartedAt = %v, want nil after reclaim", stuck.StartedAt)
	}

	// Terminal row is untouched.
	terminal, err := store.GetOperatorIntent(ctx, terminalID)
	if err != nil {
		t.Fatalf("GetOperatorIntent(terminal): %v", err)
	}
	if terminal.Status != state.OperatorIntentSucceeded {
		t.Errorf("terminal.Status = %q, want succeeded", terminal.Status)
	}

	// Idempotency: second reclaim with the same threshold
	// returns 0 — the stuck row is back to pending and
	// invisible to the WHERE clause, and the terminal row
	// was never matched.
	n2, err := store.ReclaimStuckRunningOperatorIntents(ctx, threshold)
	if err != nil {
		t.Fatalf("ReclaimStuckRunningOperatorIntents (idempotent): %v", err)
	}
	if n2 != 0 {
		t.Errorf("reclaim idempotent count = %d, want 0", n2)
	}

	// The reclaimed row is now claimable.
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Errorf("ClaimPendingOperatorIntent after reclaim: %v", err)
	}
}
