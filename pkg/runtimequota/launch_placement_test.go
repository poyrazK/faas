package runtimequota

import (
	"testing"

	"github.com/google/uuid"
)

func TestInitialLaunchPlacementStableAttemptIdentity(t *testing.T) {
	g, _, _, _ := fixture(t)
	h := HostEpoch{NodeID: uuid.NewString(), HostEpochID: uuid.NewString()}
	b := LifecycleBinding{InstanceID: uuid.NewString(), AppID: g.Binding.RegionalAppID, AccountID: g.Binding.RegionalAccountID, DeploymentID: uuid.NewString(), Plan: g.Policy.Plan, Purpose: CapacityServing}
	run := uuid.NewString()
	original, err := NewLaunchPlacement(g, h, b, run, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"allocation", "deployment", "purpose", "rollout"} {
		t.Run(change, func(t *testing.T) {
			grant, binding := g, b
			rollout := false
			switch change {
			case "allocation":
				grant.AllocationID = uuid.NewString()
				grant.Generation++
			case "deployment":
				binding.DeploymentID = uuid.NewString()
			case "purpose":
				binding.Purpose = CapacityWarm
			case "rollout":
				grant.RolloutConcurrency = 1
				rollout = true
			}
			next, err := NewLaunchPlacement(grant, h, binding, run, rollout)
			if err != nil || next.Permit.ID != original.Permit.ID {
				t.Fatalf("mutable claim obtained a new physical ID: %+v %v", next, err)
			}
			if next.Seat != nil && next.Seat.ID != original.Seat.ID {
				t.Fatal("mutable claim obtained a new initial seat ID")
			}
		})
	}
	for _, change := range []string{"instance", "run", "node", "epoch"} {
		t.Run(change, func(t *testing.T) {
			epoch, binding, runID := h, b, run
			switch change {
			case "instance":
				binding.InstanceID = uuid.NewString()
			case "run":
				runID = uuid.NewString()
			case "node":
				epoch.NodeID = uuid.NewString()
			case "epoch":
				epoch.HostEpochID = uuid.NewString()
			}
			next, err := NewLaunchPlacement(g, epoch, binding, runID, false)
			if err != nil || next.Permit.ID == original.Permit.ID || next.Seat.ID == original.Seat.ID {
				t.Fatalf("new attempt reused old IDs: %+v %v", next, err)
			}
		})
	}
	for _, purpose := range []string{CapacityServing, CapacityWarm, CapacityPrime, CapacityAppTask} {
		b.Purpose = purpose
		p, err := NewLaunchPlacement(g, h, b, run, false)
		if err != nil || (p.Seat != nil) != (purpose == CapacityServing) || p.Permit.Kind != purpose {
			t.Fatal("initial purpose/seat projection", p, err)
		}
	}
	if _, err := NewLaunchPlacement(g, h, b, run, true); err == nil {
		t.Fatal("task acquired a rollout seat")
	}
	if _, err := NewLaunchPlacement(g, HostEpoch{}, b, run, false); err == nil {
		t.Fatal("missing epoch obtained an attempt")
	}
}
