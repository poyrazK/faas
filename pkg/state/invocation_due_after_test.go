package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMem_ListDueInvocationsAfterPagesByKeyset(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "due-after@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "due-after"})
	if err != nil {
		t.Fatal(err)
	}
	testListDueInvocationsAfterPagesByKeyset(t, ctx, store, app.ID, acct.ID)
}

type dueInvocationPager interface {
	EnqueueInvocation(context.Context, state.Invocation) (state.Invocation, error)
	ListDueInvocationsAfter(context.Context, time.Time, state.InvocationDueCursor, int) ([]state.Invocation, error)
}

func testListDueInvocationsAfterPagesByKeyset(t *testing.T, ctx context.Context, s dueInvocationPager, appID, acctID string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		// i/2: pairs share a due_at so the id tiebreak is exercised.
		inv, err := s.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: acctID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/x", DueAt: now.Add(-time.Duration(10-i/2) * time.Second),
		})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		want[inv.ID] = true
	}
	if _, err := s.EnqueueInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: acctID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/x", DueAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed future: %v", err)
	}
	seen := map[string]bool{}
	var after state.InvocationDueCursor
	for pages := 0; ; pages++ {
		if pages > len(want) {
			t.Fatalf("paging did not terminate; seen %d of %d", len(seen), len(want))
		}
		page, err := s.ListDueInvocationsAfter(ctx, now, after, 2)
		if err != nil {
			t.Fatalf("ListDueInvocationsAfter: %v", err)
		}
		for _, inv := range page {
			if !want[inv.ID] {
				t.Fatalf("listed %s, which is not a due row", inv.ID)
			}
			if seen[inv.ID] {
				t.Fatalf("row %s listed twice", inv.ID)
			}
			seen[inv.ID] = true
		}
		if len(page) < 2 {
			break
		}
		last := page[len(page)-1]
		after = state.InvocationDueCursor{DueAt: last.DueAt, ID: last.ID}
	}
	if len(seen) != len(want) {
		t.Fatalf("saw %d of %d due rows", len(seen), len(want))
	}
}
