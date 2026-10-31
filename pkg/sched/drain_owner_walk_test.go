package sched_test

// adr: 080

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A tick walks at most MaxDuePagesPerTick pages. When more than that many
// rows of another node's apps sat at the head, every tick spent its walk on
// them and this node's own due work never ran. The walk now lists only apps
// this node may claim.
func TestDrain_OtherNodesBacklogBeyondWalkDoesNotStarveOwnWork(t *testing.T) {
	t.Parallel()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanHobby, true)
	ctx := context.Background()
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	owned := apps[0]
	foreign, err := store.CreateApp(ctx, state.App{
		AccountID: owned.AccountID, Slug: "foreign-backlog", RAMMB: 256, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppNodeID(ctx, foreign.ID, "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppNodeID(ctx, owned.ID, "node-b"); err != nil {
		t.Fatal(err)
	}
	(*d.Engine()).WithOwnerNodeID("node-b")

	head := time.Now().Add(-time.Minute)
	for i := 0; i < sched.MaxDuePagesPerTick*64+5; i++ {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: foreign.ID, AccountID: foreign.AccountID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/x", DueAt: head,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mine, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: owned.ID, AccountID: owned.AccountID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}

	d.Tick(ctx)
	if got := ds.Calls().Load(); got != 1 {
		t.Fatalf("synth calls = %d, want 1 for the owned row behind another node's backlog", got)
	}
	got, err := store.InvocationByID(ctx, mine.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.InvocationCompleted {
		t.Fatalf("owned row state = %q, want completed", got.State)
	}
}
