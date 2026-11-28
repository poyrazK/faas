// adr: 119
package sched_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the production lane carries an app's static egress IP; a stage leaves
// through the shared egress pool so a partner allowlist for production's
// address never accepts stage traffic.
func TestDeploymentStaticEgressIPProductionLaneOnly(t *testing.T) {
	ip := netip.MustParseAddr("203.0.113.7")
	app := state.App{StaticEgressIP: &ip}
	for _, tc := range []struct {
		scope, want string
	}{
		{"", "203.0.113.7"},
		{state.DefaultEnvScope, "203.0.113.7"},
		{"production", "203.0.113.7"},
		{"staging", ""},
		{"pr-12", ""},
	} {
		if got := sched.DeploymentStaticEgressIP(app, state.Deployment{Scope: tc.scope}, api.PlanScale); got != tc.want {
			t.Errorf("scope %q: static egress IP = %q, want %q", tc.scope, got, tc.want)
		}
	}
	if got := sched.DeploymentStaticEgressIP(state.App{}, state.Deployment{Scope: "production"}, api.PlanScale); got != "" {
		t.Errorf("unpinned app got static egress IP %q", got)
	}
	// ADR-20261128090000: a pin kept after a downgrade is inactive.
	if got := sched.DeploymentStaticEgressIP(app, state.Deployment{Scope: "production"}, api.PlanPro); got != "" {
		t.Errorf("downgraded account's app got static egress IP %q", got)
	}
}

func TestStaticEgressWireRetainsProductionAndStagePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, pin string
		excluded  bool
	}{{"production", "203.0.113.7", false}, {"stage", "", true}, {"legacy", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			pin, excluded := sched.StaticEgressWirePolicyForTest(sched.AppSpec{StaticEgressIP: tc.pin, StaticEgressExcluded: tc.excluded})
			if pin != tc.pin || excluded != tc.excluded {
				t.Fatalf("wire lost egress eligibility: pin=%q excluded=%t", pin, excluded)
			}
		})
	}
}

// ADR-20261128090000: a pinned app is placed only on a node that holds its
// static egress address, even when another node has more headroom.
func TestChoosePlacementHonoursStaticEgressNode(t *testing.T) {
	node := func(id string) state.ComputeNode {
		return state.ComputeNode{ID: id, Name: id, Active: true, AdmissionCeilingMB: 4096, VCPUBudget: 16}
	}
	nodes := []state.ComputeNode{node("busy"), node("idle")}
	used := map[string]int64{"busy": 3000}
	base := sched.Request{AppID: "app", RAMMB: 256}

	if p, err := sched.ChoosePlacement(nodes, used, nil, base); err != nil || p.NodeID != "idle" {
		t.Fatalf("unpinned placement = %+v, %v; want idle", p, err)
	}
	pinned := base
	pinned.StaticEgressPinned, pinned.StaticEgressNodeIDs = true, []string{"busy"}
	if p, err := sched.ChoosePlacement(nodes, used, nil, pinned); err != nil || p.NodeID != "busy" {
		t.Fatalf("pinned placement = %+v, %v; want busy", p, err)
	}
	pinned.StaticEgressNodeIDs = []string{"gone"}
	if _, err := sched.ChoosePlacement(nodes, used, nil, pinned); err == nil || !strings.Contains(fmt.Sprint(sched.AdmissionBlockers(err)), "static_egress_node") {
		t.Fatalf("pinned to an absent node: err = %v; want a static_egress_node refusal", err)
	}
}

func TestStaticEgressNodesReadTheInventory(t *testing.T) {
	ctx := t.Context()
	st := state.NewMemStore()
	acct, err := st.CreateAccount(ctx, "se-placement@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	node, err := st.CreateComputeNode(ctx, state.ComputeNode{Name: "ip-node", TargetURL: "tcp://127.0.0.1:1", AdmissionCeilingMB: 1024, VCPUBudget: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if pinned, _, err := sched.StaticEgressNodesForTest(ctx, st, acct.ID, "203.0.113.50"); err != nil || pinned {
		t.Fatalf("legacy (no inventory row) pin constrained placement: pinned=%v err=%v", pinned, err)
	}
	if err := st.AddStaticEgressAddress(ctx, state.StaticEgressAddress{IP: netip.MustParseAddr("203.0.113.50"), NodeID: node.ID, AccountID: acct.ID}); err != nil {
		t.Fatal(err)
	}
	pinned, nodes, err := sched.StaticEgressNodesForTest(ctx, st, acct.ID, "203.0.113.50")
	if err != nil || !pinned || len(nodes) != 1 || nodes[0] != node.ID {
		t.Fatalf("inventory pin = %v %v %v; want [%s]", pinned, nodes, err, node.ID)
	}
	if pinned, _, _ := sched.StaticEgressNodesForTest(ctx, st, acct.ID, ""); pinned {
		t.Fatal("an unpinned spec was constrained")
	}
}
