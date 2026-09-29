package state_test

// PgStore coverage gap tests for the billing-dedupe surface.
//
// This file covers Store methods that had no PgStore test before slice 6:
//
//   Stripe: HasStripePushHour, RecordStripePushHour (idempotency under
//           redelivered push).
//   Paddle: ClaimPaddleOverageWindow (fresh / already-claimed /
//           lease-steal), CompletePaddleOverageWindow (happy /
//           already-completed refresh), ReapStalePaddleOverageClaims.
//
// All assertions go through the Store API; the lease-steal test reaches
// into the pool via pgWithPool(t) to backdate claimed_at so the
// stale-pending branch is exercisable without sleeping.
//
// Helpers reused: pgStore(t), createAccount(t,s,ctx,email), pgWithPool(t).

import (
	"testing"
	"time"
)

// --- Stripe hourly push dedupe ---------------------------------------------

// TestPg_HasStripePushHour_AbsentReturnsFalse pins the empty path: a
// freshly-created account has no stripe_push_dedupe rows, and the
// meterd pusher must therefore attempt the Stripe UsageRecord POST.
func TestPg_HasStripePushHour_AbsentReturnsFalse(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	hour := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	got, err := s.HasStripePushHour(ctx, acctID, hour)
	if err != nil {
		t.Fatalf("HasStripePushHour: %v", err)
	}
	if got {
		t.Errorf("HasStripePushHour = true, want false (fresh account)")
	}
}

// TestPg_RecordStripePushHour_FirstSucceedsHasReturnsTrue pins the
// happy round-trip: a fresh RecordStripePushHour is followed by
// HasStripePushHour returning true. The meterd pusher reads this
// value to skip a redelivered push.
func TestPg_RecordStripePushHour_FirstSucceedsHasReturnsTrue(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	hour := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	if err := s.RecordStripePushHour(ctx, acctID, hour); err != nil {
		t.Fatalf("RecordStripePushHour: %v", err)
	}
	got, err := s.HasStripePushHour(ctx, acctID, hour)
	if err != nil {
		t.Fatalf("HasStripePushHour: %v", err)
	}
	if !got {
		t.Errorf("HasStripePushHour = false, want true (after RecordStripePushHour)")
	}
}

// TestPg_RecordStripePushHour_DifferentHoursIsolated pins PK isolation:
// the unique index on (account_id, hour) means a push for one hour
// must not affect a different hour. Cross-account isolation is also
// covered: a sibling account's hour must be unread.
func TestPg_RecordStripePushHour_DifferentHoursIsolated(t *testing.T) {
	s, ctx := pgStore(t)
	acctA := createAccount(t, s, ctx, pgTestEmail(t)+"-a")
	acctB := createAccount(t, s, ctx, pgTestEmail(t)+"-b")

	hourA := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	hourB := hourA.Add(time.Hour)

	if err := s.RecordStripePushHour(ctx, acctA, hourA); err != nil {
		t.Fatalf("RecordStripePushHour(acctA, hourA): %v", err)
	}

	// Same hour on a different account — must NOT see acctA's push.
	got, err := s.HasStripePushHour(ctx, acctB, hourA)
	if err != nil {
		t.Fatalf("HasStripePushHour(acctB, hourA): %v", err)
	}
	if got {
		t.Errorf("HasStripePushHour(acctB, hourA) = true, want false (cross-account isolation)")
	}

	// Different hour on acctA — must NOT see the push from hourA.
	got, err = s.HasStripePushHour(ctx, acctA, hourB)
	if err != nil {
		t.Fatalf("HasStripePushHour(acctA, hourB): %v", err)
	}
	if got {
		t.Errorf("HasStripePushHour(acctA, hourB) = true, want false (PK isolation)")
	}

	// Idempotent re-write — RecordStripePushHour is ON CONFLICT DO NOTHING,
	// so a second call for the same hour must NOT error and must NOT
	// "create" a duplicate row.
	if err := s.RecordStripePushHour(ctx, acctA, hourA); err != nil {
		t.Fatalf("RecordStripePushHour(acctA, hourA) [redelivered]: %v", err)
	}
}

// --- Paddle overage-window claim state machine -----------------------------

// TestPg_ClaimPaddleOverageWindow_FreshSucceeds pins the fresh-INSERT
// path: no row exists yet, ClaimPaddleOverageWindow's Step 1 INSERT
// creates it in 'completed' default state, Step 2 UPDATE flips it to
// 'pending'. The returned bool is true — this pod owns the claim.
func TestPg_ClaimPaddleOverageWindow_FreshSucceeds(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	window := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	claimed, err := s.ClaimPaddleOverageWindow(ctx, acctID, window, "pod-A", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimPaddleOverageWindow: %v", err)
	}
	if !claimed {
		t.Errorf("ClaimPaddleOverageWindow = false, want true (fresh claim)")
	}
}

// TestPg_ClaimPaddleOverageWindow_AlreadyClaimedReturnsFalse pins the
// losing-race path: after pod-A claims successfully, pod-B's claim
// must observe the row in state='pending' with claimed_at within the
// lease window, and therefore return false.
func TestPg_ClaimPaddleOverageWindow_AlreadyClaimedReturnsFalse(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	window := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if claimed, err := s.ClaimPaddleOverageWindow(ctx, acctID, window, "pod-A", 5*time.Minute); err != nil {
		t.Fatalf("ClaimPaddleOverageWindow(pod-A): %v", err)
	} else if !claimed {
		t.Fatalf("ClaimPaddleOverageWindow(pod-A) = false, want true (setup)")
	}

	// Pod B attempts to claim within the lease — must lose the race.
	claimed, err := s.ClaimPaddleOverageWindow(ctx, acctID, window, "pod-B", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimPaddleOverageWindow(pod-B): %v", err)
	}
	if claimed {
		t.Errorf("ClaimPaddleOverageWindow(pod-B) = true, want false (lease held by pod-A)")
	}
}

// TestPg_ClaimPaddleOverageWindow_LeaseBoundarySteals pins the
// stale-pending steal: a pod claims, then crashes; the lease expires;
// a fresh pod can claim by stealing the row. We backdate claimed_at
// via pgWithPool to skip the sleep — the WHERE clause
// `claimed_at < now() - lease` is what we're pinning.
func TestPg_ClaimPaddleOverageWindow_LeaseBoundarySteals(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	window := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// Pod A claims successfully.
	if claimed, err := s.ClaimPaddleOverageWindow(ctx, acctID, window, "pod-A", 5*time.Minute); err != nil {
		t.Fatalf("ClaimPaddleOverageWindow(pod-A): %v", err)
	} else if !claimed {
		t.Fatalf("ClaimPaddleOverageWindow(pod-A) = false, want true (setup)")
	}

	// Backdate claimed_at to 10 minutes ago — well past the 5-minute
	// lease. The pod is "crashed".
	if _, err := pool.Exec(ctx,
		`update paddle_overage_dedupe set claimed_at = now() - interval '10 minutes' where account_id = $1 and window_start = $2`,
		acctID, window); err != nil {
		t.Fatalf("backdate claimed_at: %v", err)
	}

	// Pod B's claim must steal the stale row.
	claimed, err := s.ClaimPaddleOverageWindow(ctx, acctID, window, "pod-B", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimPaddleOverageWindow(pod-B): %v", err)
	}
	if !claimed {
		t.Errorf("ClaimPaddleOverageWindow(pod-B) = false, want true (lease stolen)")
	}
}

// TestPg_CompletePaddleOverageWindow_FlipsPendingToCompleted pins the
// happy path: pod-A claims, then completes. The row ends in
// state='completed' and pushed_at is stamped.
//
// Note: a follow-up claim from a second pod is intentionally permitted
// after Complete (see pgstore.go:3875-3897 — re-claim on a 'completed'
// row is the design-comment-documented retry-after-failure path).
// That assertion lives in
// pgstore_paddle_claim_regression_test.go:ReclaimAfterCompleteIsPermitted
// (PR #382). This test pins the state-transition shape directly via
// the row readback, since the re-claim side is covered elsewhere.
func TestPg_CompletePaddleOverageWindow_FlipsPendingToCompleted(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	window := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if _, err := s.ClaimPaddleOverageWindow(ctx, acctID, window, "pod-A", 5*time.Minute); err != nil {
		t.Fatalf("ClaimPaddleOverageWindow: %v", err)
	}
	if err := s.CompletePaddleOverageWindow(ctx, acctID, window, 1024); err != nil {
		t.Fatalf("CompletePaddleOverageWindow: %v", err)
	}

	// Read back the row directly: state must be 'completed' (the
	// happy path), pushed_at must be non-NULL.
	var state string
	var pushedAt *time.Time
	if err := pool.QueryRow(ctx,
		`select state, pushed_at from paddle_overage_dedupe where account_id = $1 and window_start = $2`,
		acctID, window).Scan(&state, &pushedAt); err != nil {
		t.Fatalf("read row state: %v", err)
	}
	if state != "completed" {
		t.Errorf("state = %q, want completed", state)
	}
	if pushedAt == nil {
		t.Errorf("pushed_at is NULL, want non-NULL after Complete")
	}
}

// TestPg_CompletePaddleOverageWindow_AlreadyCompletedStampsPushedAt
// pins the refresh branch: a second Complete call on a row that's
// already 'completed' must NOT error (the call returns nil per the
// implementation), and pushed_at must be re-stamped via the second
// UPDATE statement.
//
// This is the load-bearing branch for Idempotency-Key-collapsed
// redelivered POSTs — a successful Stripe-side collapse means pod-A
// sees two responses for the same window, and the second Complete
// must be a quiet no-op rather than an alert.
func TestPg_CompletePaddleOverageWindow_AlreadyCompletedStampsPushedAt(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	window := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// Seed a row that's already in 'completed' state — we never call
	// Claim, we INSERT directly. This skips the lease machinery and
	// jumps straight to the "row exists in completed state" refresh
	// branch of CompletePaddleOverageWindow.
	if _, err := pool.Exec(ctx,
		`insert into paddle_overage_dedupe (account_id, window_start, state) values ($1, $2, 'completed')`,
		acctID, window); err != nil {
		t.Fatalf("seed completed row: %v", err)
	}

	// Capture pushed_at before — should be the default (now()) at INSERT time.
	var pushedAtBefore time.Time
	if err := pool.QueryRow(ctx,
		`select pushed_at from paddle_overage_dedupe where account_id = $1 and window_start = $2`,
		acctID, window).Scan(&pushedAtBefore); err != nil {
		t.Fatalf("read pushed_at before: %v", err)
	}

	// Wait a few milliseconds so the re-stamp is observable, then
	// call Complete — must NOT error and must NOT escalate state.
	time.Sleep(20 * time.Millisecond)
	if err := s.CompletePaddleOverageWindow(ctx, acctID, window, 1024); err != nil {
		t.Fatalf("CompletePaddleOverageWindow(already-completed): %v", err)
	}

	// Verify pushed_at was refreshed AND state is still 'completed'.
	var pushedAtAfter time.Time
	var stateAfter string
	if err := pool.QueryRow(ctx,
		`select state, pushed_at from paddle_overage_dedupe where account_id = $1 and window_start = $2`,
		acctID, window).Scan(&stateAfter, &pushedAtAfter); err != nil {
		t.Fatalf("read state+pushed_at after: %v", err)
	}
	if stateAfter != "completed" {
		t.Errorf("state = %q, want completed", stateAfter)
	}
	if !pushedAtAfter.After(pushedAtBefore) {
		t.Errorf("pushed_at did not advance: before=%s after=%s", pushedAtBefore, pushedAtAfter)
	}
}

// TestPg_ReapStalePaddleOverageClaims_ResetsOldPending pins the boot-time
// reaper: a stale-pending row gets reset to state='completed' with
// claimed_at=NULL, returning it to the claimable pool. The returned
// count must reflect the number of rows reset.
func TestPg_ReapStalePaddleOverageClaims_ResetsOldPending(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	acctA := createAccount(t, s, ctx, pgTestEmail(t)+"-a")
	acctB := createAccount(t, s, ctx, pgTestEmail(t)+"-b")
	window := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// Acct A: claim, then backdate to make it stale.
	if _, err := s.ClaimPaddleOverageWindow(ctx, acctA, window, "pod-A", 5*time.Minute); err != nil {
		t.Fatalf("ClaimPaddleOverageWindow(acctA): %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update paddle_overage_dedupe set claimed_at = now() - interval '10 minutes' where account_id = $1 and window_start = $2`,
		acctA, window); err != nil {
		t.Fatalf("backdate acctA: %v", err)
	}

	// Acct B: claim (no backdate) — must NOT be reaped because
	// claimed_at is within the lease window.
	if _, err := s.ClaimPaddleOverageWindow(ctx, acctB, window, "pod-B", 5*time.Minute); err != nil {
		t.Fatalf("ClaimPaddleOverageWindow(acctB): %v", err)
	}

	// Reap with a 1-minute threshold — acctA is stale (10min old),
	// acctB is fresh.
	n, err := s.ReapStalePaddleOverageClaims(ctx, 1*time.Minute)
	if err != nil {
		t.Fatalf("ReapStalePaddleOverageClaims: %v", err)
	}
	if n != 1 {
		t.Errorf("ReapStalePaddleOverageClaims = %d, want 1 (only acctA is stale)", n)
	}

	// Verify acctA's row was reset: state='completed', claimed_at=NULL.
	var stateA string
	var claimedAtA *time.Time
	if err := pool.QueryRow(ctx,
		`select state, claimed_at from paddle_overage_dedupe where account_id = $1 and window_start = $2`,
		acctA, window).Scan(&stateA, &claimedAtA); err != nil {
		t.Fatalf("read acctA state: %v", err)
	}
	if stateA != "completed" {
		t.Errorf("acctA state = %q, want completed", stateA)
	}
	if claimedAtA != nil {
		t.Errorf("acctA claimed_at = %v, want NULL (reaped)", claimedAtA)
	}

	// Verify acctB's row is untouched: state='pending' with a fresh claimed_at.
	var stateB string
	if err := pool.QueryRow(ctx,
		`select state from paddle_overage_dedupe where account_id = $1 and window_start = $2`,
		acctB, window).Scan(&stateB); err != nil {
		t.Fatalf("read acctB state: %v", err)
	}
	if stateB != "pending" {
		t.Errorf("acctB state = %q, want pending (fresh claim not reaped)", stateB)
	}
}
