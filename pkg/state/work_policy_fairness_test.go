package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemWorkPolicyFairnessCap(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "fairness-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "fairness-test"})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkPolicyFairnessCap(t, ctx, store, account.ID, app.ID)
}

func TestPgWorkPolicyFairnessCap(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertWorkPolicyFairnessCap(t, ctx, store, accountID, appID)
}

func assertWorkPolicyFairnessCap(t *testing.T, ctx context.Context, store state.Store, accountID, appID string) {
	t.Helper()
	policy := workpolicy.Policy{Name: "tenant-work", MaxRunningPerKey: 1, MaxRunningPerFairnessKey: 2}
	newWork := func(key, tenant string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
			ID: uuid.NewString(), AppID: appID, AccountID: accountID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/",
			DueAt: time.Now().Add(-time.Second),
		}, policy, "s:"+key, "s:"+tenant)
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	a1, a2, a3 := newWork("doc-a1", "tenant-a"), newWork("doc-a2", "tenant-a"), newWork("doc-a3", "tenant-a")
	b1 := newWork("doc-b1", "tenant-b")
	for _, inv := range []state.Invocation{a1, a2} {
		if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 10); err != nil {
			t.Fatalf("claim tenant-a %s: %v", inv.ID, err)
		}
	}
	if _, err := store.ClaimInvocationWithCap(ctx, a3.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("third tenant-a claim = %v, want fairness cap", err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, b1.ID, "", 30, 10); err != nil {
		t.Fatalf("other tenant claim: %v", err)
	}
	b2 := newWork("doc-b2", "tenant-b")
	pager := store.(interface {
		ListDueInvocationsAfter(context.Context, time.Time, state.InvocationDueCursor, int) ([]state.Invocation, error)
	})
	due, err := pager.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range due {
		seen[row.ID] = true
	}
	if seen[a3.ID] || !seen[b2.ID] {
		t.Fatalf("saturated tenant remained due or other tenant vanished: %+v", seen)
	}
	if err := store.CompleteKeyedInvocation(ctx, a1.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, a3.ID, "", 30, 10); err != nil {
		t.Fatalf("tenant-a did not resume after release: %v", err)
	}
	var concurrent []state.Invocation
	for index := 0; index < 8; index++ {
		concurrent = append(concurrent, newWork(uuid.NewString(), "tenant-c"))
	}
	start := make(chan struct{})
	results := make(chan error, len(concurrent))
	var wg sync.WaitGroup
	for _, inv := range concurrent {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, claimErr := store.ClaimInvocationWithCap(ctx, id, "", 30, 10)
			results <- claimErr
		}(inv.ID)
	}
	close(start)
	wg.Wait()
	close(results)
	claimed := 0
	for claimErr := range results {
		if claimErr == nil {
			claimed++
		} else if !errors.Is(claimErr, state.ErrConflict) {
			t.Fatalf("concurrent fairness claim: %v", claimErr)
		}
	}
	if claimed != 2 {
		t.Fatalf("concurrent tenant-c claims = %d, want 2", claimed)
	}
	defaultPolicy := workpolicy.Policy{Name: "default-group", MaxRunningPerKey: 1,
		MaxRunningPerFairnessKey: 1}
	for _, key := range []string{"s:default-a", "s:default-b"} {
		inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
			ID: uuid.NewString(), AppID: appID, AccountID: accountID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/",
			DueAt: time.Now().Add(-time.Second),
		}, defaultPolicy, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 10); err != nil {
			t.Fatalf("default fairness group %s: %v", key, err)
		}
	}
}
