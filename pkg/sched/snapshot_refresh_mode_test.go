package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// A platform snapshot refresh serves nothing, so a node drain has no routes
// to withdraw for it, as for a fork (ADR-20261106214916).
func TestDrainWithdrawsNoRoutesForASnapshotRefresh(t *testing.T) {
	running := state.Instance{ID: "i-1", NodeID: "n-1", State: string(state.StateRunning)}
	for mode, want := range map[state.InstanceMode]bool{
		state.InstanceModeNormal:  true,
		state.InstanceModeService: true,
		state.InstanceModeFork:    false,
		state.InstanceModeRefresh: false,
	} {
		ins := running
		ins.Mode = string(mode)
		if got := sched.DrainWithdrawsRoutes(ins); got != want {
			t.Errorf("mode %s: drain withdraws routes = %v, want %v", mode, got, want)
		}
	}
}
