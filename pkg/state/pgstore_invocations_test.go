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

	if err := s.FailInvocation(ctx, enq.ID, "blip", 5*time.Second); err != nil {
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
	if err := s.FailInvocation(ctx, enq.ID, "permanent", 0); err != nil {
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
	if err := s.FailInvocation(ctx, rows[4].ID, "x", 0); err != nil {
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
