package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type dueOwnerStore interface {
	dueInvocationPager
	CreateApp(context.Context, state.App) (state.App, error)
	SetAppNodeID(context.Context, string, string) error
}

// A scheduler lists only the due rows it may claim: apps with no node and
// apps on its own node. Other nodes' rows never fill its bounded walk.
func testListDueInvocationsAfterOwnerNode(t *testing.T, ctx context.Context, s dueOwnerStore, acctID, mine, other string) {
	t.Helper()
	app := func(slug, node string) string {
		t.Helper()
		created, err := s.CreateApp(ctx, state.App{AccountID: acctID, Slug: slug + "-" + uuid.NewString()[:8], RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
		if err != nil {
			t.Fatal(err)
		}
		if node != "" {
			if err := s.SetAppNodeID(ctx, created.ID, node); err != nil {
				t.Fatal(err)
			}
		}
		return created.ID
	}
	unplaced, own, foreign := app("due-unplaced", ""), app("due-mine", mine), app("due-other", other)
	now := time.Now().UTC().Truncate(time.Microsecond)
	enqueue := func(appID string, ago time.Duration) string {
		t.Helper()
		inv, err := s.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: acctID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/x", DueAt: now.Add(-ago)})
		if err != nil {
			t.Fatal(err)
		}
		return inv.ID
	}
	for i := 0; i < 3; i++ {
		enqueue(foreign, time.Minute)
	}
	want := map[string]bool{enqueue(unplaced, time.Second): true, enqueue(own, time.Second): true}

	rows, err := s.ListDueInvocationsAfter(ctx, now, state.InvocationDueCursor{OwnerNodeID: mine}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(want) {
		t.Fatalf("owner-scoped walk listed %d rows, want %d (unplaced and own app only)", len(rows), len(want))
	}
	for _, inv := range rows {
		if !want[inv.ID] {
			t.Fatalf("owner-scoped walk listed %s of app %s", inv.ID, inv.AppID)
		}
	}
	if rows, err := s.ListDueInvocationsAfter(ctx, now, state.InvocationDueCursor{}, 10); err != nil || len(rows) != 5 {
		t.Fatalf("unscoped walk = %d rows, %v; want all 5", len(rows), err)
	}
}

func TestMem_ListDueInvocationsAfterOwnerNode(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "due-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	testListDueInvocationsAfterOwnerNode(t, ctx, store, acct.ID, "node-mine", "node-other")
}

func TestPg_ListDueInvocationsAfterOwnerNode(t *testing.T) {
	s, ctx, _, acctID := seedInvocationPg(t)
	mine := resolveDefaultLocal(t, ctx, s)
	other, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: "due-owner-" + uuid.NewString()[:8], TargetURL: "tcp://127.0.0.1:50051",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	testListDueInvocationsAfterOwnerNode(t, ctx, s, acctID, mine, other.ID)
}
