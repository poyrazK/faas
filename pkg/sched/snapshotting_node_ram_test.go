// adr: 20261027172037 — placement does not lend a node the RAM its capturing
// instances still hold.
package sched_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A park wave leaves the owner node full of instances still writing their
// snapshots. Placement used to read that node as empty and send the wake
// there; it now goes to the peer.
func TestPlacementCountsCapturingInstances(t *testing.T) {
	const ceilingMB, ramMB = 1024, 248
	f := newFailoverFleet(t, 2, ceilingMB, func(string) error { return nil })
	owner, peer := f.nodes[0], f.nodes[1]
	other := f.app(t, api.PlanPro, ramMB, 5)
	dep, err := f.store.LiveDeployment(f.ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range ceilingMB / (ramMB + api.PerVMOverheadMB) {
		if _, err := f.store.CreateInstanceWithMode(f.ctx, other.ID, dep.ID, string(state.StateSnapshotting), ramMB, owner.ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
			t.Fatal(err)
		}
	}

	res, err := f.e.Wake(f.ctx, f.app(t, api.PlanPro, ramMB, 1).ID, "", "", sched.TriggerCronSched)
	if err != nil {
		t.Fatalf("wake with the peer free: %v", err)
	}
	if res.NodeID != peer.ID {
		t.Fatalf("wake placed on %s, want the peer %s: the owner's RAM is held by capturing instances", res.NodeID, peer.ID)
	}
}
