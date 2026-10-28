// adr: 20261028200212 — an owner scheduler's admission and the pressure
// rebalancer's parked-only move serialize on the app row.
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func checkAppOwnerFence(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	app, dep := completionApplication(t, s)
	a := completionNode(t, s, state.NodeLifecycleActive)
	b := completionNode(t, s, state.NodeLifecycleActive)
	admit := func(owner string) (state.Instance, error) {
		return s.CreateInstanceWithMode(state.WithAppOwnerFence(ctx, owner), app, dep, string(state.StateWaking), 128, a.ID, uuid.NewString(), string(state.InstanceModeNormal))
	}

	// An app with no node is every scheduler's.
	if _, err := admit(b.ID); err != nil {
		t.Fatalf("fenced admission of an unplaced app: %v", err)
	}
	if err := s.SetAppNodeID(ctx, app, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := admit(a.ID); err != nil {
		t.Fatalf("owner's fenced admission: %v", err)
	}
	if _, err := admit(b.ID); !errors.Is(err, state.ErrAppOwnerMoved) {
		t.Fatalf("another node's fenced admission = %v, want ErrAppOwnerMoved", err)
	}
	if _, err := s.CreateInstanceWithMode(ctx, app, dep, string(state.StateParked), 128, a.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
		t.Fatalf("unfenced admission: %v", err)
	}

	reassigner, ok := s.(state.ParkedAppOwnerReassigner)
	if !ok {
		t.Fatalf("%T cannot move parked apps", s)
	}
	if err := reassigner.ReassignParkedAppOwner(ctx, app, a.ID, b.ID); !errors.Is(err, state.ErrAppResident) || !errors.Is(err, state.ErrConflict) {
		t.Fatalf("moving an app with resident instances = %v, want ErrAppResident", err)
	}
	instances, err := s.ListInstancesForApp(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range instances {
		if state.State(in.State).CountsForRAM() {
			if err := s.UpdateInstanceState(ctx, in.ID, string(state.StateStopped)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := reassigner.ReassignParkedAppOwner(ctx, app, b.ID, a.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("moving from a node that does not own the app = %v, want ErrConflict", err)
	}
	if err := reassigner.ReassignParkedAppOwner(ctx, app, a.ID, b.ID); err != nil {
		t.Fatalf("moving a parked app: %v", err)
	}
	moved, err := s.AppByID(ctx, app)
	if err != nil || moved.NodeID != b.ID || moved.ReassignedAt == nil {
		t.Fatalf("app after the move = %+v, %v; want owned by %s with reassigned_at", moved, err, b.ID)
	}
	if _, err := admit(a.ID); !errors.Is(err, state.ErrAppOwnerMoved) {
		t.Fatalf("the previous owner's fenced admission = %v, want ErrAppOwnerMoved", err)
	}
}

func TestAppOwnerFenceMemory(t *testing.T) {
	checkAppOwnerFence(t, state.NewMemStore())
}
