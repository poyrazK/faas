package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 134 — concurrent claims of one account never take more slots than its
// async cap, whether the cap row exists or the first claims race to create
// it; a refused claim leaves its invocation pending, and a completion frees
// its slot for the next claim.
func TestPg_ClaimInvocationWithCapConcurrentClaimsHoldTheCap(t *testing.T) {
	s, ctx, appID, acctID := seedInvocationPg(t)
	enqueue := func(app, account string, n int) []string {
		t.Helper()
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			inv, err := s.EnqueueInvocation(ctx, state.Invocation{
				AppID: app, AccountID: account, Source: state.InvocationQueue,
				Method: "POST", Path: "/cap", Payload: json.RawMessage(`{}`),
				DueAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatalf("EnqueueInvocation: %v", err)
			}
			ids = append(ids, inv.ID)
		}
		return ids
	}
	// claimAll claims every id at once and returns the claimed ones.
	claimAll := func(ids []string, maxInflight int) []string {
		t.Helper()
		var mu sync.Mutex
		var claimed []string
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.ClaimInvocationWithCap(ctx, id, "", 30, maxInflight)
				if err != nil && !errors.Is(err, state.ErrQuotaExceeded) {
					t.Errorf("claim %s: %v", id, err)
					return
				}
				if err == nil {
					mu.Lock()
					claimed = append(claimed, id)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		return claimed
	}
	requireQuota := func(account string, wantMax, wantInflight int) {
		t.Helper()
		max, inflight, err := s.GetAccountAsyncQuota(ctx, account)
		if err != nil || max != wantMax || inflight != wantInflight {
			t.Fatalf("quota = %d/%d, %v; want %d/%d", max, inflight, err, wantMax, wantInflight)
		}
	}
	requirePending := func(ids, claimed []string) {
		t.Helper()
		taken := map[string]bool{}
		for _, id := range claimed {
			taken[id] = true
		}
		for _, id := range ids {
			inv, err := s.InvocationByID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			want := state.InvocationPending
			if taken[id] {
				want = state.InvocationDispatching
			}
			if inv.State != want {
				t.Fatalf("invocation %s is %s, want %s", id, inv.State, want)
			}
		}
	}

	// An existing cap row.
	if _, _, err := s.EnsureAccountAsyncQuota(ctx, acctID, 3); err != nil {
		t.Fatal(err)
	}
	ids := enqueue(appID, acctID, 12)
	claimed := claimAll(ids, 3)
	if len(claimed) != 3 {
		t.Fatalf("%d claims succeeded under a cap of 3", len(claimed))
	}
	requireQuota(acctID, 3, 3)
	requirePending(ids, claimed)
	if err := s.CompleteInvocation(context.Background(), claimed[0], nil); err != nil {
		t.Fatal(err)
	}
	requireQuota(acctID, 3, 2)
	var rest []string
	for _, id := range ids {
		if inv, err := s.InvocationByID(ctx, id); err == nil && inv.State == state.InvocationPending {
			rest = append(rest, id)
		}
	}
	if again := claimAll(rest, 3); len(again) != 1 {
		t.Fatalf("%d claims succeeded into one freed slot", len(again))
	}
	requireQuota(acctID, 3, 3)

	// No cap row yet: the first claims race to create it.
	otherAccount, otherApp, _ := seedLiveDeploy(t, s, ctx, "-cap-race", "cap-race")
	fresh := enqueue(otherApp, otherAccount, 8)
	claimed = claimAll(fresh, 2)
	if len(claimed) != 2 {
		t.Fatalf("%d first claims succeeded under a cap of 2", len(claimed))
	}
	requireQuota(otherAccount, 2, 2)
	requirePending(fresh, claimed)
}
