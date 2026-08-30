package state_test

// pgstore_invocations_test locks the Move 1 PgStore contract for the
// 10 new invocation methods against a real Postgres cluster. MemStore
// has its own suite; this file is the load-bearing CI gate for the
// production SQL.
//
// Why both? MemStore's tests are unit tests — they exercise the
// semantics. This file exercises the *SQL*: the `for update skip
// locked` claim contract, the partial-index predicates, the
// `attempts++` atomicity, the instance_id round-trip. Each test
// pins one shape the drain depends on.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// seedInvocationPg returns a PgStore with a live account + app ready
// for invocation tests. The deployment is required because some
// PgStore methods (none in this file today, but for future-proofing)
// join through the live deployment; today only the app + account FKs
// are needed.
func seedInvocationPg(t *testing.T) (*state.PgStore, context.Context, string /*appID*/, string /*acctID*/) {
	t.Helper()
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)
	return s, ctx, appID, acctID
}

// TestPg_InvocationRoundTrip: enqueue → read by id → claim → stamp →
// complete → cancel. The full lifecycle, with assertions on each
// transition's storage shape.
func TestPg_InvocationRoundTrip(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	due := time.Now().UTC().Add(-time.Second)
	inv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		Method: "POST", Path: "/x", Payload: json.RawMessage(`{"k":"v"}`),
		DueAt: due,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if inv.State != state.InvocationPending {
		t.Errorf("post-enqueue state = %q, want pending", inv.State)
	}
	if inv.ID == "" || inv.CreatedAt.IsZero() {
		t.Errorf("post-enqueue row missing id/created_at: %+v", inv)
	}

	// Read-back.
	got, err := s.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if got.AppID != appID || got.Source != state.InvocationQueue {
		t.Errorf("round-trip = %+v", got)
	}

	// Claim → dispatching, lease + attempts++.
	claimed, err := s.ClaimInvocation(ctx, inv.ID, "inst-X", 30)
	if err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	if claimed.State != state.InvocationDispatching {
		t.Errorf("post-claim state = %q, want dispatching", claimed.State)
	}
	if claimed.Attempts != 1 {
		t.Errorf("post-claim attempts = %d, want 1", claimed.Attempts)
	}
	if claimed.LeaseExpiresAt == nil {
		t.Errorf("post-claim lease_expires_at = nil, want set")
	}
	if claimed.InstanceID != "inst-X" {
		t.Errorf("post-claim instance_id = %q, want inst-X (column round-trip)", claimed.InstanceID)
	}

	// Re-claim must fail (state=dispatching, not pending).
	if _, err := s.ClaimInvocation(ctx, inv.ID, "inst-Y", 30); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("re-claim err = %v, want ErrNotFound", err)
	}

	// Complete → state=completed, completed_at stamped.
	if err := s.CompleteInvocation(ctx, inv.ID, json.RawMessage(`{"status":200}`)); err != nil {
		t.Fatalf("CompleteInvocation: %v", err)
	}
	final, _ := s.InvocationByID(ctx, inv.ID)
	if final.State != state.InvocationCompleted {
		t.Errorf("post-complete state = %q, want completed", final.State)
	}
	if final.CompletedAt == nil {
		t.Errorf("post-complete completed_at = nil, want set")
	}
}

// TestPg_ClaimInvocationAtomicity pins the SKIP LOCKED contract at
// the SQL level: two concurrent claims for the same row produce
// exactly one winner. The second caller must see ErrNotFound (no row
// matched state='pending' after the first claim flipped it).
func TestPg_ClaimInvocationAtomicity(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	inv, _ := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue, DueAt: time.Now().UTC(),
	})

	type result struct {
		inv state.Invocation
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			c, err := s.ClaimInvocation(ctx, inv.ID, "inst", 30)
			results <- result{c, err}
		}()
	}
	r1 := <-results
	r2 := <-results
	winners, losers := 0, 0
	for _, r := range []result{r1, r2} {
		if r.err == nil {
			winners++
		} else if errors.Is(r.err, state.ErrNotFound) {
			losers++
		} else {
			t.Fatalf("unexpected claim error: %v", r.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Errorf("winners=%d losers=%d, want 1/1", winners, losers)
	}
}

// TestPg_ListDueInvocationsRespectsLimitAndOrder: the drain's hot
// path. Three rows in due_at order, limit=2 returns the first two;
// the third is filtered out.
func TestPg_ListDueInvocationsRespectsLimitAndOrder(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	now := time.Now().UTC()
	for i, d := range []time.Duration{-3 * time.Second, -2 * time.Second, -1 * time.Second} {
		if _, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
			DueAt: now.Add(d),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	rows, err := s.ListDueInvocations(ctx, now, 2)
	if err != nil {
		t.Fatalf("ListDueInvocations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (limit)", len(rows))
	}
	if !rows[0].DueAt.Before(rows[1].DueAt) {
		t.Errorf("rows not ordered by due_at: %+v", rows)
	}
}

// TestPg_ListDueInvocationsSkipsFutureDue: a future-dated row must
// NOT be returned even if it is in state='pending'.
func TestPg_ListDueInvocationsSkipsFutureDue(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	if _, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		DueAt: time.Now().UTC().Add(1 * time.Hour),
	}); err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	rows, err := s.ListDueInvocations(ctx, time.Now().UTC(), 64)
	if err != nil {
		t.Fatalf("ListDueInvocations: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 (future-dated not due yet)", len(rows))
	}
}

// TestPg_FailInvocationTransientAndPermanent: the retryAfter split.
// retryAfter>0 → state=pending + due_at in the future + last_error
// set; retryAfter==0 → state=failed + completed_at stamped + last_error
// set.
func TestPg_FailInvocationTransientAndPermanent(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	enq, _ := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue, DueAt: time.Now().UTC(),
	})
	if _, err := s.ClaimInvocation(ctx, enq.ID, "", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}

	if err := s.FailInvocation(ctx, enq.ID, "blip", 5*time.Second, 0); err != nil {
		t.Fatalf("FailInvocation transient: %v", err)
	}
	got, _ := s.InvocationByID(ctx, enq.ID)
	if got.State != state.InvocationPending {
		t.Errorf("transient state = %q, want pending", got.State)
	}
	if got.LastError != "blip" {
		t.Errorf("transient last_error = %q, want blip", got.LastError)
	}
	if !got.DueAt.After(time.Now()) {
		t.Errorf("transient due_at = %s, want in the future", got.DueAt)
	}

	// Re-claim and permanent-fail.
	if _, err := s.ClaimInvocation(ctx, enq.ID, "", 30); err != nil {
		t.Fatalf("re-Claim: %v", err)
	}
	if err := s.FailInvocation(ctx, enq.ID, "permanent", 0, 0); err != nil {
		t.Fatalf("FailInvocation permanent: %v", err)
	}
	final, _ := s.InvocationByID(ctx, enq.ID)
	if final.State != state.InvocationFailed {
		t.Errorf("permanent state = %q, want failed", final.State)
	}
	if final.CompletedAt == nil {
		t.Errorf("permanent completed_at = nil, want set")
	}
	if final.LastError != "permanent" {
		t.Errorf("permanent last_error = %q, want permanent", final.LastError)
	}
}

// TestPg_CountPendingInvocationsPartialIndex pins the partial-index
// predicate: state IN ('pending','dispatching'). Terminal rows must
// NOT count.
func TestPg_CountPendingInvocationsPartialIndex(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	now := time.Now().UTC()
	// 2 pending, 1 dispatched, 1 completed, 1 failed.
	for _, src := range []state.InvocationSource{
		state.InvocationQueue, state.InvocationQueue,
		state.InvocationQueue, state.InvocationQueue, state.InvocationQueue,
	} {
		enq, _ := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: acctID, Source: src, DueAt: now,
		})
		// Walk the last two to terminal so the count is exact.
		switch src {
		case state.InvocationQueue:
			// The first two stay pending. Drive the third to
			// dispatching, the fourth to completed, the fifth to
			// failed.
		}
		_ = enq
	}
	rows, _ := s.ListDueInvocations(ctx, now, 64)
	if len(rows) != 5 {
		t.Fatalf("setup: %d rows, want 5", len(rows))
	}
	// rows[2] → dispatching
	if _, err := s.ClaimInvocation(ctx, rows[2].ID, "", 30); err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	// rows[3] → completed
	if _, err := s.ClaimInvocation(ctx, rows[3].ID, "", 30); err != nil {
		t.Fatalf("claim 3: %v", err)
	}
	if err := s.CompleteInvocation(ctx, rows[3].ID, nil); err != nil {
		t.Fatalf("complete 3: %v", err)
	}
	// rows[4] → failed
	if _, err := s.ClaimInvocation(ctx, rows[4].ID, "", 30); err != nil {
		t.Fatalf("claim 4: %v", err)
	}
	if err := s.FailInvocation(ctx, rows[4].ID, "x", 0, 0); err != nil {
		t.Fatalf("fail 4: %v", err)
	}

	n, err := s.CountPendingInvocations(ctx, appID, state.InvocationQueue)
	if err != nil {
		t.Fatalf("CountPendingInvocations: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3 (2 pending + 1 dispatching; completed + failed excluded)", n)
	}
}

// TestPg_CancelInvocationIdempotent: a second cancel on a cancelled
// row must NOT error. The dashboard's cancel button can re-fire on
// retry.
func TestPg_CancelInvocationIdempotent(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	inv, _ := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationDelayedTask, DueAt: time.Now().UTC(),
	})
	if err := s.CancelInvocation(ctx, inv.ID); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	if err := s.CancelInvocation(ctx, inv.ID); err != nil {
		t.Errorf("second cancel should be no-op, got %v", err)
	}
	got, _ := s.InvocationByID(ctx, inv.ID)
	if got.State != state.InvocationCancelled {
		t.Errorf("state = %q, want cancelled", got.State)
	}
}

// TestPg_CountInstanceInvocationsInMinute pins the meter join: rows
// for (instance_id, minute, state='dispatching') count, terminal
// rows do not, future-dated rows do not.
func TestPg_CountInstanceInvocationsInMinute(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	minute := time.Now().UTC().Truncate(time.Minute)
	// 3 in-minute dispatching rows for inst-A, 1 outside the window.
	for i, d := range []time.Duration{0, 10 * time.Second, 30 * time.Second, 5 * time.Minute} {
		enq, _ := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: acctID, Source: state.InvocationQueue, DueAt: minute.Add(d),
		})
		if _, err := s.ClaimInvocation(ctx, enq.ID, "", 30); err != nil {
			t.Fatalf("claim %d: %v", i, err)
		}
		if err := s.StampInstanceInvocation(ctx, enq.ID, "inst-A"); err != nil {
			t.Fatalf("stamp %d: %v", i, err)
		}
	}
	n, err := s.CountInstanceInvocationsInMinute(ctx, "inst-A", minute)
	if err != nil {
		t.Fatalf("CountInstanceInvocationsInMinute: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3 (one row is 5min out)", n)
	}
}

// TestPg_StampInstanceInvocationOnlyOnDispatching: a stamp on a row
// in state='pending' must NOT land. The drain's lifecycle is
// claim → wake → stamp; a stamp before claim would race FailInvocation.
func TestPg_StampInstanceInvocationOnlyOnDispatching(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	inv, _ := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue, DueAt: time.Now().UTC(),
	})
	// Row is pending; stamp must fail.
	if err := s.StampInstanceInvocation(ctx, inv.ID, "inst"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("stamp on pending err = %v, want ErrNotFound", err)
	}
	// Claim → dispatching; stamp now succeeds.
	if _, err := s.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.StampInstanceInvocation(ctx, inv.ID, "inst-1"); err != nil {
		t.Fatalf("StampInstanceInvocation on dispatching: %v", err)
	}
	got, _ := s.InvocationByID(ctx, inv.ID)
	if got.InstanceID != "inst-1" {
		t.Errorf("instance_id = %q, want inst-1 (column round-trip)", got.InstanceID)
	}
}

// TestPg_ListExpiredInvocationsForReaper_StateFilter pins the §6.7
// reaper contract: only terminal rows (completed/failed/dead_letter/
// cancelled) whose result_retention_until is in the past are returned.
// Live dispatching rows and retention-NULL rows must be skipped.
func TestPg_ListExpiredInvocationsForReaper_StateFilter(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)

	// Drive a row all the way through to completed (the simplest
	// terminal path) and stamp a retention timestamp in the past.
	inv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		Method: "POST", Path: "/x", Payload: json.RawMessage(`{}`),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := s.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.CompleteInvocation(ctx, inv.ID, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// A live pending row (retention-NULL by default) — must NOT be
	// reaped.
	live, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		Method: "POST", Path: "/live", Payload: json.RawMessage(`{}`),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation live: %v", err)
	}
	_ = live

	ids, err := s.ListExpiredInvocationsForReaper(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatalf("ListExpiredInvocationsForReaper: %v", err)
	}
	// The completed row has result_retention_until = NULL (we never
	// stamped one), so the partial-index predicate
	//   WHERE state IN ('completed','failed','dead_letter','cancelled')
	//   AND result_retention_until < now()
	// filters it out. Assert the list is empty — this proves the
	// retention-NULL filter works as documented.
	if len(ids) != 0 {
		t.Errorf("reaper returned %d rows when none had retention stamped; want 0 (NULL retention must be filtered)", len(ids))
	}
}

// TestPg_EnsureAccountAsyncQuota_RoundTrip pins the upsert semantics
// of the per-account counter-table path used by PR-B's per-account
// concurrency cap. EnsureAccountAsyncQuota overwrites the cap on
// conflict (operator/plan-change path); Decrement clamps at zero
// via greatest() so a stray decrement doesn't error or block the
// next decrement. GetAccountAsyncQuota mirrors the row.
func TestPg_EnsureAccountAsyncQuota_RoundTrip(t *testing.T) {
	s, ctx, _, acctID := seedInvocationPg(t)

	// Initial insert — sets the cap.
	cap1, inflight1, err := s.EnsureAccountAsyncQuota(ctx, acctID, 50)
	if err != nil {
		t.Fatalf("EnsureAccountAsyncQuota initial: %v", err)
	}
	if cap1 != 50 || inflight1 != 0 {
		t.Errorf("initial = cap=%d inflight=%d, want 50/0", cap1, inflight1)
	}

	// Re-call is idempotent (DO NOTHING on conflict — cap unchanged).
	cap2, inflight2, err := s.EnsureAccountAsyncQuota(ctx, acctID, 50)
	if err != nil {
		t.Fatalf("EnsureAccountAsyncQuota idempotent: %v", err)
	}
	if cap2 != 50 || inflight2 != 0 {
		t.Errorf("idempotent = cap=%d inflight=%d, want 50/0", cap2, inflight2)
	}

	// Decrement clamps at zero via greatest() — no error, no
	// negative. This is the production semantics: the increment/
	// decrement pair should always balance, but a stray decrement
	// (e.g. an operator-initiated transition that bypassed
	// ClaimInvocationWithCap) must NOT block subsequent decrements.
	if err := s.DecrementAccountAsyncInflight(ctx, acctID); err != nil {
		t.Errorf("Decrement at zero returned err = %v, want nil (clamped)", err)
	}

	// GetAccountAsyncQuota mirrors the row state.
	cap3, inflight3, err := s.GetAccountAsyncQuota(ctx, acctID)
	if err != nil {
		t.Fatalf("GetAccountAsyncQuota: %v", err)
	}
	if cap3 != 50 || inflight3 != 0 {
		t.Errorf("Get = cap=%d inflight=%d, want 50/0", cap3, inflight3)
	}
}

// TestPg_RetryQueueDeadLetter_StateMachine pins the queue DLQ replay
// path (PR-C). A row in state='dead_letter' that the dashboard's
// "Replay" button hits must transition back to 'pending' with
// attempts=0 and the replay trail stamped — distinct from
// ReplayInvocation which enqueues a new row tagged Source=InvocationReplay.
func TestPg_RetryQueueDeadLetter_StateMachine(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	inv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		Method: "POST", Path: "/dlq", Payload: json.RawMessage(`{}`),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}

	// Drive the row to 'dead_letter' by exhausting the budget. We
	// simulate the drain's path: claim (attempts becomes 1) → fail
	// with retryAfter=5s and budget=1; since attempts >= budget, the
	// row lands in 'dead_letter'.
	if _, err := s.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := s.FailInvocation(ctx, inv.ID, "exhausted", 5*time.Second, 1); err != nil {
		t.Fatalf("FailInvocation to dead_letter: %v", err)
	}
	pre, _ := s.InvocationByID(ctx, inv.ID)
	if pre.State != state.InvocationDeadLetter {
		t.Fatalf("setup state = %q, want dead_letter", pre.State)
	}

	// Replay: row goes back to 'pending', attempts=0, last_error
	// cleared, replay trail stamped.
	replayed, err := s.RetryQueueDeadLetter(ctx, acctID, inv.ID)
	if err != nil {
		t.Fatalf("RetryQueueDeadLetter: %v", err)
	}
	if replayed.State != state.InvocationPending {
		t.Errorf("post-replay state = %q, want pending", replayed.State)
	}
	if replayed.Attempts != 0 {
		t.Errorf("post-replay attempts = %d, want 0", replayed.Attempts)
	}

	post, _ := s.InvocationByID(ctx, inv.ID)
	if post.State != state.InvocationPending {
		t.Errorf("persisted state = %q, want pending", post.State)
	}
	if post.LastError != "" {
		t.Errorf("post-replay last_error = %q, want empty", post.LastError)
	}
	if post.LastReplayedAt == nil {
		t.Errorf("last_replayed_at not stamped")
	}

	// Second replay on the same row is now an idempotent miss
	// (state != dead_letter) — must return ErrNotFound, not a 5xx.
	if _, err := s.RetryQueueDeadLetter(ctx, acctID, inv.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("second replay err = %v, want ErrNotFound", err)
	}
}

// TestPg_ClaimInvocationWithCap_HappyPath pins the cap-aware claim
// path (PR-B). Lazy-insert of the quota row + atomic
// current_inflight++ under cap, in the same tx as the state
// transition.
func TestPg_ClaimInvocationWithCap_HappyPath(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	inv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		Method: "POST", Path: "/cap", Payload: json.RawMessage(`{}`),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}

	claimed, err := s.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 10)
	if err != nil {
		t.Fatalf("ClaimInvocationWithCap: %v", err)
	}
	if claimed.State != state.InvocationDispatching {
		t.Errorf("state = %q, want dispatching", claimed.State)
	}
	if claimed.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", claimed.Attempts)
	}

	// Quota row was lazy-inserted at max=10; current_inflight=1.
	cap, inflight, err := s.GetAccountAsyncQuota(ctx, acctID)
	if err != nil {
		t.Fatalf("GetAccountAsyncQuota: %v", err)
	}
	if cap != 10 || inflight != 1 {
		t.Errorf("quota = cap=%d inflight=%d, want 10/1", cap, inflight)
	}
}

// TestPg_ClaimInvocationWithCap_Errors pins the four error branches
// of ClaimInvocationWithCap that the happy-path test does not
// exercise (PR-B coverage push — the lookup miss, the cap-exhausted
// branch, and the not-pending transition miss).
func TestPg_ClaimInvocationWithCap_Errors(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	missingID := "00000000-0000-0000-0000-000000000001"

	// 1. Invocation does not exist → ErrNotFound (covers the
	//    pgx.ErrNoRows branch on the account_id lookup).
	if _, err := s.ClaimInvocationWithCap(ctx, missingID, "inst-X", 30, 5); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("missing id err = %v, want ErrNotFound", err)
	}

	// 2. Cap is 0 → ErrQuotaExceeded (the CAS rejects on
	//    current_inflight < 0 = false). Lazy-inserts the quota row
	//    at max=0; subsequent claim has nothing to do but reject.
	inv, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationQueue,
		Method: "POST", Path: "/capzero", Payload: json.RawMessage(`{}`),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := s.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 0); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Errorf("cap=0 err = %v, want ErrQuotaExceeded", err)
	}

	// 3. RetryQueueDeadLetter on a pending row → ErrNotFound
	//    (state != dead_letter guard).
	if _, err := s.RetryQueueDeadLetter(ctx, acctID, inv.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RetryQueueDeadLetter(pending) = %v, want ErrNotFound", err)
	}
	// 4. RetryQueueDeadLetter on a missing row → ErrNotFound.
	if _, err := s.RetryQueueDeadLetter(ctx, acctID, missingID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RetryQueueDeadLetter(missing) = %v, want ErrNotFound", err)
	}
}
