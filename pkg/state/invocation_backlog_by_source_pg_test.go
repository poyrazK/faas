package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Queue state counted queue messages only, so ~1,000 async invocations
// waiting for the drain showed as depth=0 (prod throughput test
// 2026-11-12). The backlog counters report every source; settled rows drop
// out.
func TestPgInvocationBacklogBySource(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	enqueue := func(source state.InvocationSource) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, Source: source,
			QueueName: "default", Payload: json.RawMessage(`{}`), DueAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	for range 3 {
		enqueue(state.InvocationAsyncInvoke)
	}
	settled := enqueue(state.InvocationQueue)
	enqueue(state.InvocationQueue)
	if _, err := store.ClaimInvocation(ctx, settled.ID, "", 60); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInvocation(ctx, settled.ID, nil); err != nil {
		t.Fatal(err)
	}

	var reader state.InvocationBacklogReader = store
	got, err := reader.InvocationBacklogBySource(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	if got[string(state.InvocationAsyncInvoke)] != 3 || got[string(state.InvocationQueue)] != 1 || len(got) != 2 {
		t.Fatalf("backlog by source = %v, want async_invoke=3 queue=1", got)
	}
	if _, err := reader.InvocationBacklogBySource(ctx, "not-a-uuid"); err == nil {
		t.Fatal("an invalid app id was accepted")
	}
}
