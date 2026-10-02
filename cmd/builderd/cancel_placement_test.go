package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
	"github.com/onebox-faas/faas/pkg/wire"
)

type nodeCancelVM struct {
	countVM
	cancels int
}

func (v *nodeCancelVM) Cancel(context.Context, string) error { v.cancels++; return nil }

func TestCancelNotificationTargetsClaimOwner(t *testing.T) {
	store := state.NewMemStore()
	fx := conformance.Seed(t, store)
	build, err := store.CreateBuild(fx.Ctx, fx.Deployment.ID, state.DeploymentKindTarball, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuildWithNodeAffinity(fx.Ctx, build.ID, fx.Node.Name, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkBuildCancelled(fx.Ctx, build.ID, "test", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	vm := &nodeCancelVM{}
	ops := wire.NewOpsMetrics("builderd")
	for _, name := range []string{"", "unknown-builder", state.DefaultLocalNodeName} {
		cancelBuildOnNode(fx.Ctx, store, name, vm, ops, build.ID, discardLog())
	}
	if vm.cancels != 0 {
		t.Fatal("wrong builder cancelled a foreign claim")
	}
	if err := store.NodeSetLifecycle(fx.Ctx, fx.Node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
		t.Fatal(err)
	}
	cancelBuildOnNode(fx.Ctx, store, fx.Node.Name, vm, ops, build.ID, discardLog())
	if vm.cancels != 1 {
		t.Fatalf("inactive owner cancellation calls: %d, want 1", vm.cancels)
	}
}
