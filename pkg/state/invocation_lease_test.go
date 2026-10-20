package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type invocationLeaseFixtureStore = state.ContractTestStore

// ADR-1180: a dispatcher keeps a long invocation's claim by renewing it, but
// a stale attempt or a lapsed lease is never revived.
func testInvocationLease(t *testing.T, store invocationLeaseFixtureStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "invocation-lease@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "invocation-lease"})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: acct.ID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/", DueAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimInvocation(ctx, inv.ID, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if renewed, err := store.RenewInvocationLease(ctx, claimed.ID, claimed.Attempts+1, 60); err != nil || renewed {
		t.Fatalf("stale attempt renewed the claim: %v %v", renewed, err)
	}
	if renewed, err := store.RenewInvocationLease(ctx, claimed.ID, claimed.Attempts, 60); err != nil || !renewed {
		t.Fatalf("live claim was not renewed: %v %v", renewed, err)
	}
	current, err := store.InvocationByID(ctx, claimed.ID)
	if err != nil || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(claimed.LeaseExpiresAt.Add(30*time.Second)) {
		t.Fatalf("renewal did not extend the lease: %+v %v", current.LeaseExpiresAt, err)
	}
	if n, err := store.RequeueExpiredInvocations(ctx, claimed.LeaseExpiresAt.Add(time.Millisecond), 10); err != nil || n != 0 {
		t.Fatalf("renewed invocation was requeued at its old expiry: %d %v", n, err)
	}
	if n, err := store.RequeueExpiredInvocations(ctx, current.LeaseExpiresAt.Add(time.Millisecond), 10); err != nil || n != 1 {
		t.Fatalf("lapsed invocation requeued %d, err %v; want 1", n, err)
	}
	if renewed, err := store.RenewInvocationLease(ctx, claimed.ID, claimed.Attempts, 60); err != nil || renewed {
		t.Fatalf("requeued invocation was revived: %v %v", renewed, err)
	}
	if _, err := store.RenewInvocationLease(ctx, claimed.ID, claimed.Attempts, 0); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("zero lease accepted: %v", err)
	}
}

func TestMemInvocationLease(t *testing.T) {
	testInvocationLease(t, state.NewMemStore())
}

func TestPgInvocationLease(t *testing.T) {
	testInvocationLease(t, state.XNewPgStore(t))
}
