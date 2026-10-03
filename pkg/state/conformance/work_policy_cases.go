package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func testKeyedInvocationReplacementAndCompletion(t *testing.T, fx *Fixture) {
	policy := workpolicy.Policy{
		Name: "document-index", MaxRunningPerKey: 1,
		PendingUpdates: workpolicy.PendingKeepLatest,
	}
	enqueue := func(key string) state.Invocation {
		t.Helper()
		inv, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, state.Invocation{
			AppID: fx.App.ID, AccountID: fx.Account.ID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/index",
			DueAt: time.Now().Add(-time.Second),
		}, policy, key)
		if err != nil {
			t.Fatalf("EnqueueKeyedInvocation(%q): %v", key, err)
		}
		return inv
	}
	old := enqueue("s:document-1")
	newest := enqueue("s:document-1")
	other := enqueue("s:document-2")
	if old.WorkSequence != 1 || newest.WorkSequence != 2 || other.WorkSequence != 1 {
		t.Fatalf("lane sequences: old=%d newest=%d other=%d", old.WorkSequence, newest.WorkSequence, other.WorkSequence)
	}
	if got, err := fx.Store.InvocationByID(fx.Ctx, old.ID); err != nil || got.State != state.InvocationSuperseded {
		t.Fatalf("replaced row = %+v, %v; want superseded", got, err)
	}
	claimed, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, newest.ID, "", 30, 10)
	if err != nil || claimed.Attempts != 1 {
		t.Fatalf("claim newest = %+v, %v; want attempt 1", claimed, err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, other.ID, "", 30, 10); err != nil {
		t.Fatalf("different document could not run: %v", err)
	}
	if err := fx.Store.CompleteKeyedInvocation(fx.Ctx, newest.ID, 0, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale completion = %v; want ErrNotFound", err)
	}
	if err := fx.Store.CompleteKeyedInvocation(fx.Ctx, newest.ID, 1, nil); err != nil {
		t.Fatalf("CompleteKeyedInvocation: %v", err)
	}
	if got, err := fx.Store.InvocationByID(fx.Ctx, newest.ID); err != nil || got.State != state.InvocationCompleted {
		t.Fatalf("completed row = %+v, %v; want completed", got, err)
	}
}
