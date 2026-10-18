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

// ADR-987: binary shares go124's base, so a node with both stages it once.
func TestAssignedBasePlanStagesSharedBaseOnce(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "shared@example.com", "acct-shared")
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range []state.App{
		{AccountID: account.ID, Slug: "rust", Type: state.AppTypeFunction, Runtime: RuntimeBinary},
		{AccountID: account.ID, Slug: "go", Type: state.AppTypeFunction, Runtime: RuntimeGo124},
	} {
		if _, err := store.CreateApp(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
	runtimes, _, err := assignedBasePlan(ctx, store, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{RuntimeGo124}; !reflect.DeepEqual(runtimes, want) {
		t.Fatalf("runtimes = %v, want %v", runtimes, want)
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

// ADR-1500 (H10-140): a source app built for a shared runner base boots it as
// drive0, so a restarted imaged must re-stage it although the app row carries
// no runtime. A failed deployment's base is not staged.
func TestAssignedBasePlanStagesSharedRuntimeBases(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.UpsertComputeNode(ctx, state.ComputeNode{Name: "node-a", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, "shared-runtime@example.com", "acct-shared-runtime")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "node-source", Type: state.AppTypeApp, NodeID: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		base   string
		status state.DeploymentStatus
	}{
		{RuntimeNode22, state.DeployLive},
		{RuntimeNode24, state.DeployFailed},
	} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRuntimeBase(ctx, dep.ID, tc.base); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dep.ID, tc.status, ""); err != nil {
			t.Fatal(err)
		}
	}

	for _, nodeName := range []string{"node-a", ""} {
		runtimes, minimal, err := assignedBasePlan(ctx, store, nodeName)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{RuntimeNode22}; !reflect.DeepEqual(runtimes, want) {
			t.Fatalf("node %q: runtimes = %v, want %v", nodeName, runtimes, want)
		}
		if !minimal {
			t.Fatalf("node %q: minimal = false, want true for the source app", nodeName)
		}
	}
}
