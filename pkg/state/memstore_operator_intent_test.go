// memstore_operator_intent_test.go — MemStore operator_intents
// CRUD. Mirrors pgstore_operator_intent_test.go at the
// in-process layer so handler tests can construct a MemStore
// without spinning up Postgres. Pins:
//
//  1. InsertOperatorIntent → row exists with status='pending'.
//  2. ClaimPendingOperatorIntent returns the row FIFO
//     (oldest requested_at first), transitions to
//     status='running', stamps started_at.
//  3. Claim-fenced node execution stamps terminal
//     state with finished_at.
//  4. GetOperatorIntent returns ErrOperatorIntentNotFound
//     for missing id.
//  5. FIFO claim semantics: with two pending rows, the
//     earlier-requested row is claimed first.
//  6. ReclaimStuckRunningOperatorIntents resets rows whose
//     StartedAt is older than the threshold back to
//     `pending` (StartedAt cleared), leaves terminal rows
//     alone, and is idempotent.
//
// Build tag matches the rest of the memstore tests; no
// Postgres required.
package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStore_OperatorIntent_FullLifecycle(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}

	acct := "11111111-1111-1111-1111-111111111111"
	actor := "22222222-2222-2222-2222-222222222222"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		node.ID,
		&acct, actor, "wedged instance", json.RawMessage(`{}`), nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	if id == "" {
		t.Fatalf("InsertOperatorIntent returned empty id")
	}

	got, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("ClaimPendingOperatorIntent: %v", err)
	}
	if got.ID != id {
		t.Errorf("Claim id=%q, want %q", got.ID, id)
	}
	if got.Status != state.OperatorIntentRunning {
		t.Errorf("Claim status=%q, want running", got.Status)
	}
	if got.StartedAt == nil {
		t.Errorf("Claim did not stamp started_at")
	}

	if err := finishOperatorNodeCRUD(t, store, ctx, id); err != nil {
		t.Fatalf("ExecuteOperatorNode: %v", err)
	}

	got2, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("GetOperatorIntent: %v", err)
	}
	if got2.Status != state.OperatorIntentSucceeded {
		t.Errorf("status=%q, want succeeded", got2.Status)
	}
	if got2.FinishedAt == nil {
		t.Errorf("finished_at not stamped")
	}
}

func TestMemStore_OperatorIntent_FIFOClaim(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	actor := "22222222-2222-2222-2222-222222222222"
	acct := "11111111-1111-1111-1111-111111111111"

	// Insert two intents — older first, then newer.
	first, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForcePark,
		"first-target", &acct, actor, "first", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert first: %v", err)
	}
	// Sleep to ensure requested_at ordering survives at
	// the time.Now() resolution. memstore uses
	// time.Now().UTC(); 1ms is plenty.
	time.Sleep(time.Millisecond)
	second, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForcePark,
		"second-target", &acct, actor, "second", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert second: %v", err)
	}

	// Claim should return the older (first) row first.
	got1, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim 1: %v", err)
	}
	if got1.ID != first {
		t.Errorf("first claim id=%q, want %q (older row)", got1.ID, first)
	}

	got2, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim 2: %v", err)
	}
	if got2.ID != second {
		t.Errorf("second claim id=%q, want %q (newer row)", got2.ID, second)
	}

	// Third claim → ErrOperatorIntentNotFound (queue empty).
	if _, err := store.ClaimPendingOperatorIntent(ctx); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("third claim: got %v, want ErrOperatorIntentNotFound", err)
	}
}

func TestMemStore_OperatorIntent_MarkFailed(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	actor := "22222222-2222-2222-2222-222222222222"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		"44444444-4444-4444-4444-444444444444", nil, actor, "stale snap", nil, nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := finishOperatorNodeCRUD(t, store, ctx, id); err != nil {
		t.Fatalf("ExecuteOperatorNode failed outcome: %v", err)
	}
	got, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != state.OperatorIntentFailed {
		t.Errorf("status=%q, want failed", got.Status)
	}
	if got.Error != "Compute node is unavailable." {
		t.Errorf("error=%q, want %q", got.Error, "Compute node is unavailable.")
	}
}

func TestMemStore_OperatorIntent_GetNotFound(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	if _, err := store.GetOperatorIntent(ctx, "missing-id"); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("Get(missing): got %v, want ErrOperatorIntentNotFound", err)
	}
}

func TestMemStore_OperatorIntent_MarkWithoutClaimReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	actor := "22222222-2222-2222-2222-222222222222"
	id, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		"55555555-5555-5555-5555-555555555555", nil, actor, "no claim", nil, nil,
	)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	// Mark succeeded on a row that's still pending → error.
	if err := store.MarkOperatorIntentSucceeded(ctx, id, nil); !errors.Is(err, state.ErrOperatorIntentNotFound) {
		t.Errorf("MarkSucceeded on pending row: got %v, want ErrOperatorIntentNotFound", err)
	}
}

// TestMemStore_OperatorIntent_ReclaimStuckRunning mirrors
// the pgstore test: claim a row (status -> running, stamps
// started_at), then assert that
// ReclaimStuckRunningOperatorIntents:
//
//   - flips the row's status back to `pending` with started_at
//     cleared, when the threshold is in the future (matches
//     every running row regardless of when it started);
//   - leaves terminal rows alone (where clause filters on
//     status='running');
//   - is idempotent on a second call;
//   - allows the reclaimed row to be Claimed again.
//
// Unlike the pgstore test we don't back-date started_at
// (the MemStore map is a struct value, not a pointer, and
// there's no exposed StartedAt setter); using a future
// threshold achieves the same WHERE-clause match — a row is
// reclaimed iff status='running' AND started_at IS NOT NULL
// AND started_at < threshold. A threshold in the future
// selects every running row.
func TestMemStore_OperatorIntent_ReclaimStuckRunning(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}

	actor := "22222222-2222-2222-2222-222222222222"
	acct := "11111111-1111-1111-1111-111111111111"

	stuckID, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		"66666666-6666-6666-6666-666666666666",
		&acct, actor, "stuck", nil, nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent(stuck): %v", err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("ClaimPendingOperatorIntent(stuck): %v", err)
	}

	// Terminal row: insert + claim + atomic completion.
	terminalID, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindNodeDrain,
		node.ID,
		&acct, actor, "terminal", nil, nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent(terminal): %v", err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("ClaimPendingOperatorIntent(terminal): %v", err)
	}
	if err := finishOperatorNodeCRUD(t, store, ctx, terminalID); err != nil {
		t.Fatalf("ExecuteOperatorNode(terminal): %v", err)
	}

	// Future threshold matches the just-claimed row (whose
	// started_at is roughly time.Now()) because every
	// started_at is in the past relative to it.
	threshold := time.Now().Add(time.Minute)
	n, err := store.ReclaimStuckRunningOperatorIntents(ctx, threshold)
	if err != nil {
		t.Fatalf("ReclaimStuckRunningOperatorIntents: %v", err)
	}
	if n != 1 {
		t.Errorf("reclaim count = %d, want 1", n)
	}

	got, err := store.GetOperatorIntent(ctx, stuckID)
	if err != nil {
		t.Fatalf("GetOperatorIntent(stuck post-reclaim): %v", err)
	}
	if got.Status != state.OperatorIntentPending {
		t.Errorf("stuck.Status = %q, want pending", got.Status)
	}
	if got.StartedAt != nil {
		t.Errorf("stuck.StartedAt = %v, want nil after reclaim", got.StartedAt)
	}

	terminal, err := store.GetOperatorIntent(ctx, terminalID)
	if err != nil {
		t.Fatalf("GetOperatorIntent(terminal): %v", err)
	}
	if terminal.Status != state.OperatorIntentSucceeded {
		t.Errorf("terminal.Status = %q, want succeeded", terminal.Status)
	}

	// Idempotency.
	n2, err := store.ReclaimStuckRunningOperatorIntents(ctx, threshold)
	if err != nil {
		t.Fatalf("ReclaimStuckRunningOperatorIntents (idempotent): %v", err)
	}
	if n2 != 0 {
		t.Errorf("reclaim idempotent count = %d, want 0", n2)
	}

	// Reclaimed row is now claimable again.
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Errorf("ClaimPendingOperatorIntent after reclaim: %v", err)
	}
}

// TestMemStore_OperatorIntentOutcomeMissingCounts_CoversPgStore
// is the in-memory twin of the pgstore path that
// cmd/apid/obs_health_query.go reads to surface stuck-running
// rows via /v1/admin/obs/health. The Mega-PR (C7) added the
// PgStore method (pkg/state/pgstore_operator_intent.go:313) and
// the MemStore mirror (memstore.go:6225) but never pinned either
// — pg shard 2 covers PgStore, MemStore side was untested, so
// the pkg/state coverage gate fell to 69.9%. Two assertions:
//
//  1. A pending row (not running) is not counted.
//  2. A running row whose started_at is BEFORE threshold
//     (set via time.Now().Add(time.Minute) — every real
//     started_at is in the past relative to it) IS counted.
//
// The third clause of the WHERE — `started_at IS NOT NULL` —
// is exercised implicitly by ClaimPendingOperatorIntent, which
// stamps started_at before transitioning to `running`.
func TestMemStore_OperatorIntentOutcomeMissingCounts_CoversPgStore(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	actor := "22222222-2222-2222-2222-222222222222"
	acct := "11111111-1111-1111-1111-111111111111"

	// Stuck-running row inserted FIRST so it's the oldest
	// pending; ClaimPendingOperatorIntent will pick it up
	// (FIFO claim by requested_at). The threshold in the
	// future then matches its just-stamped started_at.
	stuckID, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForceColdBoot,
		"99999999-9999-9999-9999-999999999999",
		&acct, actor, "stuck", nil, nil,
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent(stuck): %v", err)
	}
	if _, err := store.ClaimPendingOperatorIntent(ctx); err != nil {
		t.Fatalf("ClaimPendingOperatorIntent(stuck): %v", err)
	}

	// Later-inserted pending row — must NOT be counted. Insert
	// AFTER the claim so the stuck row stays the only
	// running row (FIFO claim would otherwise pick this up).
	if _, err := store.InsertOperatorIntent(
		ctx, state.OperatorIntentKindForcePark,
		"88888888-8888-8888-8888-888888888888",
		&acct, actor, "pending", nil, nil,
	); err != nil {
		t.Fatalf("InsertOperatorIntent(pending): %v", err)
	}

	// Future threshold selects every running row.
	counts, err := store.OperatorIntentOutcomeMissingCounts(ctx, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("OperatorIntentOutcomeMissingCounts: %v", err)
	}
	if got := counts[string(state.OperatorIntentKindForceColdBoot)]; got != 1 {
		t.Errorf("stuck-running count = %d, want 1 (kind=%s)", got, state.OperatorIntentKindForceColdBoot)
	}
	if _, present := counts[string(state.OperatorIntentKindForcePark)]; present {
		t.Errorf("pending row must not be counted; got %v", counts)
	}

	// Sanity: the stuck id is still in the map.
	if _, err := store.GetOperatorIntent(ctx, stuckID); err != nil {
		t.Errorf("GetOperatorIntent(stuck) post-count: %v", err)
	}
}

// TestMemStore_OperatorActionTraceCompleteness_CoversPgStore
// pins the in-memory twin of
// pkg/state/pgstore_operator_intent.go:360 (pgstore path the
// schedd 60s completeness tick reads). The Mega-PR (C7) added
// the MemStore mirror (memstore.go:6257) without test coverage;
// this file closes the gap so the pkg/state coverage gate stays
// above 70%.
//
// Three assertions cover the 4 conditional branches in the
// aggregation loop:
//
//  1. Rows whose kind does NOT start with "operator.action."
//     are excluded (the len<16 short-circuit).
//  2. Rows whose `at` is before the `since` window are
//     excluded.
//  3. The ratio is correct for a kind with mixed trace_id
//     presence — 1-of-2 = 0.5.
//
// The vacuous-truth rule (kinds with zero rows absent from the
// map) is exercised by the empty-input baseline of the function
// — every MemStore starts with no events, so the first call
// returns an empty map.
func TestMemStore_OperatorActionTraceCompleteness_CoversPgStore(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()

	// Baseline: empty store returns empty map (vacuous-truth
	// surface for the handler's closed-set seed).
	got, err := store.OperatorActionTraceCompleteness(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("OperatorActionTraceCompleteness(baseline): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("baseline map = %v, want empty", got)
	}

	// Non-operator-action row must be excluded.
	if err := store.AppendEventWithTrace(ctx, "test:schedd", "app.wake", nil, []byte(`{}`), nil); err != nil {
		t.Fatalf("AppendEventWithTrace(non-operator): %v", err)
	}

	// Operator-action rows: 2 with trace_id, 1 without. The
	// "without" case exercises the
	// `e.TraceID != nil && *e.TraceID != ""` guard — passing
	// nil for traceID leaves TraceID nil on the row.
	if err := store.AppendEventWithTrace(ctx, "test:schedd", "operator.action.park_instance", nil, []byte(`{}`), strPtr("4bf92f3577b34da6a3ce929d0e0e4736")); err != nil {
		t.Fatalf("AppendEventWithTrace(with trace_id 1): %v", err)
	}
	if err := store.AppendEventWithTrace(ctx, "test:schedd", "operator.action.park_instance", nil, []byte(`{}`), strPtr("4bf92f3577b34da6a3ce929d0e0e4737")); err != nil {
		t.Fatalf("AppendEventWithTrace(with trace_id 2): %v", err)
	}
	if err := store.AppendEventWithTrace(ctx, "test:schedd", "operator.action.park_instance", nil, []byte(`{}`), nil); err != nil {
		t.Fatalf("AppendEventWithTrace(without trace_id): %v", err)
	}

	ratio, err := store.OperatorActionTraceCompleteness(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("OperatorActionTraceCompleteness(populated): %v", err)
	}
	if got := ratio["operator.action.park_instance"]; got != 2.0/3.0 {
		t.Errorf("ratio[operator.action.park_instance] = %v, want 0.6667", got)
	}

	// Window excludes everything older than since. A
	// since-in-the-future window yields an empty map (every
	// event's At is in the past).
	future, err := store.OperatorActionTraceCompleteness(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("OperatorActionTraceCompleteness(future window): %v", err)
	}
	if len(future) != 0 {
		t.Errorf("future-window map = %v, want empty (every At is in the past)", future)
	}
}

// strPtr is a tiny helper — *string is the wire shape
// AppendEventWithTrace expects for trace_id; avoiding a
// package-level var keeps the test self-contained.
func strPtr(s string) *string { return &s }

// Completion is claim-fenced for every supported operator kind (ADR-1526).
func finishOperatorNodeCRUD(t *testing.T, store interface {
	state.OperatorNodeStore
	GetOperatorIntent(context.Context, string) (state.OperatorIntent, error)
}, ctx context.Context, id string) error {
	t.Helper()
	i, err := store.GetOperatorIntent(ctx, id)
	if err != nil {
		return err
	}
	_, err = store.ExecuteOperatorNode(ctx, id, i.ClaimToken)
	return err
}
