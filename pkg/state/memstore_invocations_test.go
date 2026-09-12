package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// seedInvocationApp returns a one-account, one-app MemStore ready for
// invocation tests. Each test gets its own store to keep the parallel
// safety guarantees obvious.
func seedInvocationApp(t *testing.T) (*MemStore, string /*appID*/, string /*acctID*/) {
	t.Helper()
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "inv@localhost", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{ID: newID(), Slug: "inv-app", AccountID: acct.ID, RAMMB: 256, Runtime: "node22"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return m, app.ID, acct.ID
}

func TestInvocationEnqueueAndLookup(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()

	inv, err := m.EnqueueInvocation(ctx, Invocation{
		AppID: appID, AccountID: acctID, Source: InvocationAsyncInvoke,
		Method: "POST", Path: "/", Payload: json.RawMessage(`{"x":1}`),
		DueAt: time.Now()})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if inv.ID == "" || inv.State != InvocationPending {
		t.Errorf("returned row missing ID or wrong state: id=%q state=%q", inv.ID, inv.State)
	}
	got, err := m.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if got.AppID != appID || got.Source != InvocationAsyncInvoke {
		t.Errorf("round-trip = %+v", got)
	}
}

// Enqueue must reject for an unknown app id; otherwise the dashboard
// cap check could land on rows with no parent.
func TestInvocationEnqueueRejectsUnknownApp(t *testing.T) {
	m, _, acctID := seedInvocationApp(t)
	_, err := m.EnqueueInvocation(context.Background(), Invocation{
		AppID: "not-a-real-id", AccountID: acctID, Source: InvocationAsyncInvoke, DueAt: time.Now()})
	if err == nil {
		t.Errorf("expected error for unknown app, got nil")
	}
}

func TestInvocationListDue_OrdersByDueAtAndCapsLimit(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	now := time.Now()
	for i, d := range []time.Duration{-2 * time.Second, 0, +30 * time.Second} {
		_, err := m.EnqueueInvocation(ctx, Invocation{
			ID: newID(), AppID: appID, AccountID: acctID, Source: InvocationQueue,
			DueAt: now.Add(d)})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	due, err := m.ListDueInvocations(ctx, now.Add(time.Second), 2)
	if err != nil {
		t.Fatalf("ListDueInvocations: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("limit=2 returned %d (want 2); third row is +30s not due", len(due))
	}
	if !due[0].DueAt.Before(due[1].DueAt) && !due[0].DueAt.Equal(due[1].DueAt) {
		t.Errorf("returned rows not ordered by due_at: %+v", due)
	}
}

func TestInvocationClaimCompleteRoundTrip(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	inv, err := m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: InvocationCron, DueAt: time.Now()})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	claimed, err := m.ClaimInvocation(ctx, inv.ID, "inst-X", 30)
	if err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	if claimed.State != InvocationDispatching || claimed.InstanceID != "inst-X" || claimed.Attempts != 1 {
		t.Errorf("claim state mismatch: %+v", claimed)
	}
	if _, err := m.ClaimInvocation(ctx, inv.ID, "inst-Y", 30); !errors.Is(err, ErrNotFound) {
		t.Errorf("re-claim should be ErrNotFound, got %v", err)
	}
	// A transient dispatch error is persisted for operator diagnostics. A
	// later successful attempt must clear it or customer reads expose a
	// contradictory success result plus the stale scheduler error.
	if err := m.FailInvocation(ctx, inv.ID, "stale ownership error", time.Nanosecond, 0); err != nil {
		t.Fatalf("FailInvocation(retry): %v", err)
	}
	if _, err := m.ClaimInvocation(ctx, inv.ID, "inst-Z", 30); err != nil {
		t.Fatalf("ClaimInvocation(retry): %v", err)
	}
	if err := m.CompleteInvocation(ctx, inv.ID, json.RawMessage(`{"status":200}`)); err != nil {
		t.Fatalf("CompleteInvocation: %v", err)
	}
	got, _ := m.InvocationByID(ctx, inv.ID)
	if got.State != InvocationCompleted || got.Result == nil {
		t.Errorf("post-complete row: %+v", got)
	}
	if got.LastError != "" {
		t.Errorf("post-complete last_error = %q, want cleared", got.LastError)
	}
}

func TestInvocationExpiredLeaseIsRequeued(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	now := time.Now().UTC()
	inv, err := m.EnqueueInvocation(ctx, Invocation{
		AppID: appID, AccountID: acctID, Source: InvocationAsyncInvoke, DueAt: now,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	if _, err := m.ClaimInvocation(ctx, inv.ID, "inst-old", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	row, err := m.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	expired := now.Add(-time.Second)
	row.LeaseExpiresAt = &expired
	if err := m.stampInvocationRowForTest(row); err != nil {
		t.Fatalf("stage expired row: %v", err)
	}
	if n, err := m.RequeueExpiredInvocations(ctx, now, 10); err != nil || n != 1 {
		t.Fatalf("RequeueExpiredInvocations = %d, %v", n, err)
	}
	got, err := m.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID after reclaim: %v", err)
	}
	if got.State != InvocationPending || got.LeaseExpiresAt != nil || got.InstanceID != "" || !got.DueAt.Equal(now) {
		t.Fatalf("reclaimed row = %+v", got)
	}
	// A completion from the abandoned dispatch must not terminally complete
	// the requeued row.
	if err := m.CompleteInvocation(ctx, inv.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late completion error = %v, want ErrNotFound", err)
	}
	if _, err := m.ClaimInvocation(ctx, inv.ID, "inst-new", 30); err != nil {
		t.Fatalf("reclaim should permit a fresh claim: %v", err)
	}
}

// FailInvocation with retryAfter>0 must put the row back into pending
// + bump attempts (transient); retryAfter==0 must terminal it.
func TestInvocationFailTransientAndPermanent(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	inv, _ := m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: InvocationQueue, DueAt: time.Now()})
	if _, err := m.ClaimInvocation(ctx, inv.ID, "inst", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	if err := m.FailInvocation(ctx, inv.ID, "blip", 5*time.Second, 0); err != nil {
		t.Fatalf("FailInvocation transient: %v", err)
	}
	got, _ := m.InvocationByID(ctx, inv.ID)
	if got.State != InvocationPending || got.Attempts != 1 {
		t.Errorf("transient row should be pending+attempts=1: %+v", got)
	}

	if _, err := m.ClaimInvocation(ctx, inv.ID, "inst", 30); err != nil {
		t.Fatalf("re-claim after transient fail: %v", err)
	}
	if err := m.FailInvocation(ctx, inv.ID, "bad payload", 0, 0); err != nil {
		t.Fatalf("FailInvocation permanent: %v", err)
	}
	got, _ = m.InvocationByID(ctx, inv.ID)
	if got.State != InvocationFailed || got.CompletedAt == nil {
		t.Errorf("permanent fail row: %+v", got)
	}
}

func TestInvocationCountPending_OnlyLiveRows(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	now := time.Now()
	for _, s := range []InvocationSource{InvocationQueue, InvocationQueue, InvocationDelayedTask, InvocationCron} {
		_, _ = m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: s, DueAt: now})
	}

	// Cancel the cron row and fail the delayed_task row.
	rows, _ := m.ListDueInvocations(ctx, now.Add(time.Second), 100)
	for _, r := range rows {
		switch r.Source {
		case InvocationCron:
			_ = m.CancelInvocation(ctx, r.ID)
		case InvocationDelayedTask:
			if _, err := m.ClaimInvocation(ctx, r.ID, "inst", 30); err == nil {
				_ = m.FailInvocation(ctx, r.ID, "boom", 0, 0)
			}
		}
	}
	// Only the two queue rows should still count, and only under source=queue.
	n, err := m.CountPendingInvocations(ctx, appID, InvocationQueue)
	if err != nil {
		t.Fatalf("CountPendingInvocations: %v", err)
	}
	if n != 2 {
		t.Errorf("queue pending = %d, want 2", n)
	}
	if n, _ := m.CountPendingInvocations(ctx, appID, InvocationCron); n != 0 {
		t.Errorf("cron pending should be 0 after cancel, got %d", n)
	}
}

func TestInvocationCancelIsIdempotent(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	inv, _ := m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: InvocationDelayedTask, DueAt: time.Now()})
	if err := m.CancelInvocation(ctx, inv.ID); err != nil {
		t.Fatalf("first cancel: %v", err)
	}
	if err := m.CancelInvocation(ctx, inv.ID); err != nil {
		t.Errorf("second cancel should be no-op, got %v", err)
	}
	got, _ := m.InvocationByID(ctx, inv.ID)
	if got.State != InvocationCancelled {
		t.Errorf("post-cancel state = %q", got.State)
	}
}

// spec: customer cancellation only prevents work that is still pending.
func TestCancelPendingInvocationReturnsAuthoritativeState(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	pending, _ := m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: InvocationDelayedTask, DueAt: time.Now()})
	state, err := m.CancelPendingInvocation(ctx, pending.ID)
	if err != nil || state != InvocationCancelled {
		t.Fatalf("cancel pending = (%q, %v), want cancelled", state, err)
	}

	dispatching, _ := m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: InvocationDelayedTask, DueAt: time.Now()})
	if _, err := m.ClaimInvocation(ctx, dispatching.ID, "inst", 30); err != nil {
		t.Fatal(err)
	}
	state, err = m.CancelPendingInvocation(ctx, dispatching.ID)
	if err != nil || state != InvocationDispatching {
		t.Fatalf("cancel dispatching = (%q, %v), want dispatching", state, err)
	}
	got, _ := m.InvocationByID(ctx, dispatching.ID)
	if got.State != InvocationDispatching {
		t.Fatalf("dispatching row mutated to %q", got.State)
	}
}

func TestInvocationListForAccount_OrdersDescAndCaps(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	now := time.Now()
	for i := 0; i < 3; i++ {
		inv, _ := m.EnqueueInvocation(ctx, Invocation{
			AppID: appID, AccountID: acctID, Source: InvocationAsyncInvoke, DueAt: now.Add(time.Duration(i) * time.Second)})
		// mutate created_at monotonically so ordering is deterministic
		inv.CreatedAt = now.Add(time.Duration(i) * time.Second)
		m.invocations[inv.ID] = inv
	}
	rows, err := m.ListInvocationsForAccount(ctx, acctID, 2, "")
	if err != nil {
		t.Fatalf("ListInvocationsForAccount: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("limit=2 returned %d", len(rows))
	}
	if !rows[0].CreatedAt.After(rows[1].CreatedAt) {
		t.Errorf("rows not desc by created_at: %+v", rows)
	}
}

func TestInvocationCountInstanceInMinute(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	now := time.Now()
	// Three rows inside [now, now+1m) on the same instance, one outside.
	for _, d := range []time.Duration{0, 10 * time.Second, 30 * time.Second, 5 * time.Minute} {
		inv, _ := m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: InvocationQueue, DueAt: now.Add(d)})
		if _, err := m.ClaimInvocation(ctx, inv.ID, "inst-Z", 60); err != nil {
			t.Fatalf("claim %v: %v", d, err)
		}
	}
	n, err := m.CountInstanceInvocationsInMinute(ctx, "inst-Z", now)
	if err != nil {
		t.Fatalf("CountInstanceInvocationsInMinute: %v", err)
	}
	if n != 3 {
		t.Errorf("in-minute count = %d, want 3 (one row is 5 minutes out)", n)
	}
}

// Concurrent enqueue/drain safety smoke — MemStore must be safe for
// parallel drain goroutines (the Schedd test suite uses t.Parallel()).
func TestInvocationConcurrentEnqueueAndDrain(t *testing.T) {
	m, appID, acctID := seedInvocationApp(t)
	ctx := context.Background()
	const N = 100
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() {
			for j := 0; j < N; j++ {
				_, _ = m.EnqueueInvocation(ctx, Invocation{AppID: appID, AccountID: acctID, Source: InvocationQueue, DueAt: time.Now()})
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	rows, err := m.ListDueInvocations(ctx, time.Now().Add(time.Second), 4*N)
	if err != nil {
		t.Fatalf("ListDueInvocations: %v", err)
	}
	if len(rows) != 4*N {
		t.Errorf("survived concurrent enqueue = %d, want %d", len(rows), 4*N)
	}
}
