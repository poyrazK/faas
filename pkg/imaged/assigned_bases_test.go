package imaged

import (
	"context"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestAssignedBasePlanScopesAndDeduplicatesRuntimes(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	nodeA, err := store.UpsertComputeNode(ctx, state.ComputeNode{Name: "node-a", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	nodeB, err := store.UpsertComputeNode(ctx, state.ComputeNode{Name: "node-b", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, "bases@example.com", "acct-bases")
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{AccountID: account.ID, Slug: "node-a-js-1", Type: state.AppTypeFunction, Runtime: RuntimeNode22, NodeID: nodeA.ID},
		{AccountID: account.ID, Slug: "node-a-js-2", Type: state.AppTypeFunction, Runtime: RuntimeNode22, NodeID: nodeA.ID},
		{AccountID: account.ID, Slug: "node-a-py", Type: state.AppTypeFunction, Runtime: RuntimePython313, NodeID: nodeA.ID},
		{AccountID: account.ID, Slug: "node-a-oci", Type: state.AppTypeApp, NodeID: nodeA.ID},
		{AccountID: account.ID, Slug: "node-b-go", Type: state.AppTypeFunction, Runtime: RuntimeGo124, NodeID: nodeB.ID},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}

	runtimes, minimal, err := assignedBasePlan(ctx, store, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{RuntimeNode22, RuntimePython313}; !reflect.DeepEqual(runtimes, want) {
		t.Fatalf("runtimes = %v, want %v", runtimes, want)
	}
	if !minimal {
		t.Fatal("minimal = false, want true for assigned OCI app")
	}
}

func TestAssignedBasePlanSingleBoxIncludesAllApps(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "single@example.com", "acct-single")
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{AccountID: account.ID, Slug: "js", Type: state.AppTypeFunction, Runtime: RuntimeNode24},
		{AccountID: account.ID, Slug: "go", Type: state.AppTypeFunction, Runtime: RuntimeGo124Alpine},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
	runtimes, minimal, err := assignedBasePlan(ctx, store, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{RuntimeGo124Alpine, RuntimeNode24}; !reflect.DeepEqual(runtimes, want) {
		t.Fatalf("runtimes = %v, want %v", runtimes, want)
	}
	if minimal {
		t.Fatal("minimal = true, want false")
	}
}
