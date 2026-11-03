// adr: 20261031190030 — the floor's headroom counts every admitting node.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// The ledger knows only nodes it holds reservations on: with its one booked
// node full, every floor refill reported ram_ceiling although an empty node
// had room. A draining node's spare RAM does not count.
func TestFloorHeadroomCountsAdmittingNodesWithoutBookings(t *testing.T) {
	e := sched.XNewEngine(t, state.NewMemStore(), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	if err := e.Ledger().Admit(sched.Request{Instance: "i1", AppID: "a1", Plan: api.PlanScale, RAMMB: 2040, VCPU: 2,
		CPUMillicores: 1000, MaxConcurrency: 4, NodeID: "full", NodeCeilingMB: 2048}); err != nil {
		t.Fatal(err)
	}
	if got := e.FloorHeadroomMB(); got != 0 {
		t.Fatalf("before any node list FloorHeadroomMB = %d, want the ledger's 0", got)
	}
	e.ObserveNodeLifecycles([]state.ComputeNode{
		{ID: "full", Active: true, Lifecycle: state.NodeLifecycleActive, AdmissionCeilingMB: 2048},
		{ID: "empty", Active: true, Lifecycle: state.NodeLifecycleActive, AdmissionCeilingMB: 8000},
		{ID: "leaving", Active: true, Lifecycle: state.NodeLifecycleDraining, AdmissionCeilingMB: 16000},
	})
	if got := e.FloorHeadroomMB(); got != 8000 {
		t.Fatalf("FloorHeadroomMB = %d, want the empty node's 8000", got)
	}
}
