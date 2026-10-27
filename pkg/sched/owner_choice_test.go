// adr: 20261026170000 — a new app's owner is the admitting node that owns the
// least app RAM for its size, whichever schedd wins the claim.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func ownerNode(id string, ceilingMB int) state.ComputeNode {
	return state.ComputeNode{ID: id, Name: id, Active: true, Lifecycle: state.NodeLifecycleActive, AdmissionCeilingMB: ceilingMB}
}

func TestChooseOwnerNode(t *testing.T) {
	degraded := ownerNode("a", 8192)
	degraded.Readiness = state.NodeReadinessDegraded
	draining := ownerNode("a", 8192)
	draining.Lifecycle = state.NodeLifecycleDraining
	for _, tc := range []struct {
		name  string
		nodes []state.ComputeNode
		owned map[string]int64
		ramMB int
		want  string
	}{
		{name: "the node owning less", nodes: []state.ComputeNode{ownerNode("a", 8192), ownerNode("b", 8192)},
			owned: map[string]int64{"a": 4096, "b": 1024}, ramMB: 256, want: "b"},
		{name: "share of the ceiling, not raw RAM", nodes: []state.ComputeNode{ownerNode("a", 16384), ownerNode("b", 4096)},
			owned: map[string]int64{"a": 4096, "b": 2048}, ramMB: 256, want: "a"},
		{name: "a tie goes to the lower name", nodes: []state.ComputeNode{ownerNode("b", 8192), ownerNode("a", 8192)},
			ramMB: 256, want: "a"},
		{name: "a node too small for one instance is skipped", nodes: []state.ComputeNode{ownerNode("a", 512), ownerNode("b", 8192)},
			owned: map[string]int64{"b": 6000}, ramMB: 1024, want: "b"},
		{name: "a draining node is skipped", nodes: []state.ComputeNode{draining, ownerNode("b", 8192)},
			owned: map[string]int64{"b": 6000}, ramMB: 256, want: "b"},
		{name: "a degraded node only when nothing else qualifies", nodes: []state.ComputeNode{degraded, ownerNode("b", 8192)},
			owned: map[string]int64{"b": 6000}, ramMB: 256, want: "b"},
		{name: "a degraded node rather than no owner", nodes: []state.ComputeNode{degraded}, ramMB: 256, want: "a"},
		{name: "no admitting node", nodes: []state.ComputeNode{draining}, ramMB: 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sched.ChooseOwnerNode(tc.nodes, tc.owned, tc.ramMB)
			if tc.want == "" {
				if ok {
					t.Fatalf("chose %s, want no owner", got.ID)
				}
				return
			}
			if !ok || got.ID != tc.want {
				t.Fatalf("chose %q (ok %v), want %s", got.ID, ok, tc.want)
			}
		})
	}
}

// The claiming schedd used to stamp itself whenever CPU headroom tied, so a
// busy owner kept collecting new apps. The claim now goes to the node that
// owns less.
func TestClaimUnplacedPrefersTheLessLoadedOwner(t *testing.T) {
	f := newFailoverFleet(t, 2, 4096, func(string) error { return nil })
	for range 3 {
		f.app(t, api.PlanPro, 1024, 1) // owned by node 0, the claiming schedd's own
	}
	app := f.app(t, api.PlanPro, 256, 1, func(a *state.App) { a.NodeID = "" })
	if err := f.e.ClaimUnplaced(f.ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.store.AppByID(f.ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != f.nodes[1].ID {
		t.Fatalf("claimed by %s, want the node owning nothing (%s)", got.NodeID, f.nodes[1].ID)
	}
}
