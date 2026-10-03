package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemCancelPendingKeyedWork(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "work-cancel-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "work-cancel"})
	if err != nil {
		t.Fatal(err)
	}
	assertCancelPendingKeyedWork(t, ctx, store, account.ID, app.ID)
}

func TestPgCancelPendingKeyedWork(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertCancelPendingKeyedWork(t, ctx, store, accountID, appID)
}

type keyedCancellationStore interface {
	state.Store
	state.WorkCancellationStore
}

func assertCancelPendingKeyedWork(t *testing.T, ctx context.Context, store keyedCancellationStore, accountID, appID string) {
	t.Helper()
	policy := workpolicy.Policy{Name: "reminders", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
	newInvocation := func(key string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{ID: uuid.NewString(), AppID: appID, AccountID: accountID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/", DueAt: time.Now().Add(-time.Second)}, policy, key)
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	running := newInvocation("s:order-1")
	if _, err := store.ClaimInvocationWithCap(ctx, running.ID, "", 30, 10); err != nil {
		t.Fatal(err)
	}
	pending := newInvocation("s:order-1")
	other := newInvocation("s:order-2")
	id := uuid.NewString()
	receipt, err := store.CancelPendingKeyedInvocations(ctx, appID, policy.Name, "s:order-1", id)
	if err != nil || receipt.CancelledCount != 1 {
		t.Fatalf("cancel = %+v, %v", receipt, err)
	}
	if got, _ := store.InvocationByID(ctx, running.ID); got.State != state.InvocationDispatching {
		t.Fatalf("running work changed: %s", got.State)
	}
	if got, _ := store.InvocationByID(ctx, pending.ID); got.State != state.InvocationCancelled {
		t.Fatalf("pending work = %s", got.State)
	}
	if got, _ := store.InvocationByID(ctx, other.ID); got.State != state.InvocationPending {
		t.Fatalf("other lane = %s", got.State)
	}
	later := newInvocation("s:order-1")
	replayed, err := store.CancelPendingKeyedInvocations(ctx, appID, policy.Name, "s:order-1", id)
	if err != nil || replayed.CancelledCount != 1 {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	if got, _ := store.InvocationByID(ctx, later.ID); got.State != state.InvocationPending {
		t.Fatalf("new work cancelled by replay: %s", got.State)
	}
	if _, err := store.CancelPendingKeyedInvocations(ctx, appID, policy.Name, "s:order-2", id); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("reuse receipt for another key = %v", err)
	}
}
