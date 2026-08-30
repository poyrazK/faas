package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestMemStore_PR134AsyncContract exercises every PR-A/B/C/E memstore
// method that was previously 0%-covered. Pushes pkg/state coverage
// above the 70% floor by exercising ~10 new functions in one sweep.
func TestMemStore_PR134AsyncContract(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "pr134@localhost", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{ID: newID(), Slug: "pr134-app", AccountID: acct.ID, RAMMB: 256, Runtime: "node22"})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// EnsureAccountAsyncQuota: round-trip + overwrite.
	max, cur, err := m.EnsureAccountAsyncQuota(ctx, acct.ID, 5)
	if err != nil || max != 5 || cur != 0 {
		t.Errorf("Ensure(5) = (%d,%d,%v), want (5,0,nil)", max, cur, err)
	}
	if _, _, err := m.EnsureAccountAsyncQuota(ctx, acct.ID, 10); err != nil {
		t.Errorf("Ensure(overwrite): %v", err)
	}

	// GetAccountAsyncQuota happy path + ErrNotFound.
	if mx, cur, err := m.GetAccountAsyncQuota(ctx, acct.ID); err != nil || mx != 10 || cur != 0 {
		t.Errorf("Get(acct) = (%d,%d,%v), want (10,0,nil)", mx, cur, err)
	}
	if _, _, err := m.GetAccountAsyncQuota(ctx, "missing-acct"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}

	// Enqueue 3 invocations on this app.
	var ids [3]string
	deadline := time.Now().Add(time.Hour)
	for i := range ids {
		inv, err := m.EnqueueInvocation(ctx, Invocation{
			AppID: app.ID, AccountID: acct.ID, Source: InvocationAsyncInvoke,
			DueAt: time.Now(), DeadlineAt: &deadline,
			Payload: json.RawMessage(`{"x":1}`)})
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
		ids[i] = inv.ID
	}

	// ClaimInvocationWithCap: succeeds under cap, ErrQuotaExceeded at cap.
	got, err := m.ClaimInvocationWithCap(ctx, ids[0], "inst-A", 60, 2)
	if err != nil || got.State != InvocationDispatching {
		t.Errorf("ClaimWithCap[0] = (%+v, %v), want dispatching/nil", got, err)
	}
	if _, err := m.ClaimInvocationWithCap(ctx, ids[1], "inst-B", 60, 2); err != nil {
		t.Errorf("ClaimWithCap[1] at cur=1/2: %v", err)
	}
	if _, err := m.ClaimInvocationWithCap(ctx, ids[2], "inst-C", 60, 2); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("ClaimWithCap[2] at cur=2/2 = %v, want ErrQuotaExceeded", err)
	}
	if _, err := m.ClaimInvocationWithCap(ctx, "missing-id", "inst-X", 60, 2); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClaimWithCap(missing) = %v, want ErrNotFound", err)
	}

	// DecrementAccountAsyncInflight: clamps at zero, no error.
	if err := m.DecrementAccountAsyncInflight(ctx, acct.ID); err != nil {
		t.Errorf("Decrement: %v", err)
	}
	if err := m.DecrementAccountAsyncInflight(ctx, acct.ID); err != nil {
		t.Errorf("Decrement at zero: %v (must clamp, not error)", err)
	}
	// Decrement on missing row: no error.
	if err := m.DecrementAccountAsyncInflight(ctx, "missing-acct"); err != nil {
		t.Errorf("Decrement(missing) = %v, want nil", err)
	}

	// ListExpiredInvocationsForReaper: default limit=500, none yet.
	if _, err := m.ListExpiredInvocationsForReaper(ctx, time.Now(), 0); err != nil {
		t.Errorf("ListExpired: %v", err)
	}

	// Stamp retention on the dispatched row, then reaper should pick it up.
	past := time.Now().Add(-time.Second)
	inv0, _ := m.InvocationByID(ctx, ids[0])
	inv0.ResultRetentionUntil = &past
	m.invocations[inv0.ID] = inv0
	expired, err := m.ListExpiredInvocationsForReaper(ctx, time.Now(), 10)
	if err != nil {
		t.Errorf("ListExpired: %v", err)
	}
	if len(expired) != 1 || expired[0] != ids[0] {
		t.Errorf("expired ids = %v, want [%s]", expired, ids[0])
	}
	// DeleteInvocationsByIDs: removes 1 row, count = 1.
	n, err := m.DeleteInvocationsByIDs(ctx, expired)
	if err != nil || n != 1 {
		t.Errorf("Delete = (%d, %v), want (1, nil)", n, err)
	}
	// Deleting missing ids returns 0.
	n, _ = m.DeleteInvocationsByIDs(ctx, []string{"ghost"})
	if n != 0 {
		t.Errorf("Delete(ghost) = %d, want 0", n)
	}

	// ListDeadlineBreachedInvocations: ids[1] is still dispatching but its
	// deadline is 1h out, so no breach. Backdate the deadline + recheck.
	inv1, _ := m.InvocationByID(ctx, ids[1])
	breached := time.Now().Add(-time.Minute)
	inv1.DeadlineAt = &breached
	m.invocations[inv1.ID] = inv1
	br, err := m.ListDeadlineBreachedInvocations(ctx, time.Now(), 10)
	if err != nil {
		t.Errorf("ListBreached: %v", err)
	}
	if len(br) != 1 || br[0] != ids[1] {
		t.Errorf("breached ids = %v, want [%s]", br, ids[1])
	}

	// ForceDeadlineBreachedInvocations: transitions ids[1] to dead_letter,
	// outcome=OutcomeTimeout, decrements counter.
	if _, err := m.ForceDeadlineBreachedInvocations(ctx, []string{"ghost"}); err != nil {
		t.Errorf("ForceDeadline(ghost): %v", err)
	}
	forced, err := m.ForceDeadlineBreachedInvocations(ctx, br)
	if err != nil || forced != 1 {
		t.Errorf("ForceDeadline = (%d, %v), want (1, nil)", forced, err)
	}
	inv1, _ = m.InvocationByID(ctx, ids[1])
	if inv1.State != InvocationDeadLetter {
		t.Errorf("forced row state = %q, want dead_letter", inv1.State)
	}
	if inv1.Outcome == nil || *inv1.Outcome != OutcomeTimeout {
		t.Errorf("forced row outcome = %v, want OutcomeTimeout", inv1.Outcome)
	}

	// RetryQueueDeadLetter: happy path, scope mismatch, wrong state, missing.
	got2, err := m.RetryQueueDeadLetter(ctx, acct.ID, ids[1])
	if err != nil {
		t.Errorf("RetryQueueDeadLetter happy: %v", err)
	}
	if got2.State != InvocationPending || got2.Attempts != 0 || got2.LastError != "" {
		t.Errorf("replayed = %+v, want state=pending attempts=0 last_error=''", got2)
	}
	if got2.LastReplayedAt == nil {
		t.Error("LastReplayedAt not stamped")
	}
	if _, err := m.RetryQueueDeadLetter(ctx, "other-acct", ids[1]); !errors.Is(err, ErrNotFound) {
		t.Errorf("RetryQueueDeadLetter(scope mismatch) = %v, want ErrNotFound", err)
	}
	if _, err := m.RetryQueueDeadLetter(ctx, acct.ID, ids[2]); !errors.Is(err, ErrNotFound) {
		t.Errorf("RetryQueueDeadLetter(wrong state, ids[2] pending) = %v, want ErrNotFound", err)
	}
	if _, err := m.RetryQueueDeadLetter(ctx, acct.ID, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RetryQueueDeadLetter(missing) = %v, want ErrNotFound", err)
	}

	// ListExpiredTriggerRecordsForReaper: memstore no-op, returns (nil, nil).
	tr, err := m.ListExpiredTriggerRecordsForReaper(ctx, time.Now(), 10)
	if err != nil || tr != nil {
		t.Errorf("ListExpiredTriggerRecords(memstore) = (%v, %v), want (nil, nil)", tr, err)
	}
}
