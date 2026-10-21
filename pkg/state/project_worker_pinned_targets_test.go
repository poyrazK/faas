package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectWorkerPinnedTargetsMemory(t *testing.T) {
	testProjectWorkerPinnedTargets(t, state.NewMemStore())
}

func testProjectWorkerPinnedTargets(t *testing.T, store state.Store) {
	f := newWorkerPinFixture(t, store)
	ctx := t.Context()
	inv := f.claim(t, f.enqueue(t, "").ID)
	if _, _, err := state.ResolveInvocationVersion(ctx, store, inv); err != nil {
		t.Fatal(err)
	}
	candidate, _ := f.publish(t, "candidate")
	pins := store.(state.ProjectPinnedWorkerStore)
	assertTargets := func(want int) {
		t.Helper()
		ids, err := pins.PinnedProjectReleaseWorkers(ctx, f.app)
		if err != nil || len(ids) != want || want == 1 && ids[0] != f.baselineDep {
			t.Fatalf("targets=%v want count=%d err=%v", ids, want, err)
		}
	}
	assertTargets(1)
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	create := func() (state.Instance, error) {
		return store.CreateInstanceWithMode(ctx, f.app, f.baselineDep, string(state.StateRunning), 256, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
	}
	first, err := create()
	if err != nil {
		t.Fatalf("retained worker admission: %v", err)
	}
	second, err := create()
	if err != nil {
		t.Fatal(err)
	}
	drains := store.(state.ProjectReleaseWorkerStore)
	claimed, err := drains.ClaimProjectWorkerDrain(ctx, f.app, first.ID, candidate)
	if err != nil || !claimed {
		t.Fatalf("excess worker could not drain: %t %v", claimed, err)
	}
	claimed, err = drains.ClaimProjectWorkerDrain(ctx, f.app, second.ID, candidate)
	if err != nil || claimed {
		t.Fatalf("last pinned worker could drain: %t %v", claimed, err)
	}
	if err := store.UpdateInstanceState(ctx, first.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, inv.ID, "retry", time.Nanosecond, 3, state.WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	assertTargets(1)
	if err := store.CancelInvocation(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	assertTargets(0)
	claimed, err = drains.ClaimProjectWorkerDrain(ctx, f.app, second.ID, candidate)
	if err != nil || !claimed {
		t.Fatalf("terminal work held retained worker: %t %v", claimed, err)
	}
	if _, err := create(); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("terminal pin authorized resurrection: %v", err)
	}
}
