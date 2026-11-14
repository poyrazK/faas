// adr: 20261104160000
package sched_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type revocationFirstRouter struct {
	reviewPolicyRouter
	restricted, fabrics, activated int
	failRestriction, failFabric    bool
}

func (r *revocationFirstRouter) RestrictPrivateNetworkPeerings(context.Context, string, string, string, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) error {
	r.restricted++
	if r.fabrics != 0 {
		return errors.New("fabric repair preceded regional restriction")
	}
	if r.failRestriction && r.restricted == 1 {
		return errors.New("first node unreachable")
	}
	return nil
}

func (r *revocationFirstRouter) ReconcilePrivateNetworkFabric(context.Context, string, string, string, string, netip.Prefix) error {
	r.fabrics++
	if r.restricted != 2 {
		return errors.New("repair preceded restriction on all nodes")
	}
	if r.failFabric {
		return errors.New("unrelated fabric failed")
	}
	return nil
}

func (r *revocationFirstRouter) ReconcilePrivateNetworkPeerings(context.Context, string, string, string, []privatenetwork.FabricSpec, []privatenetwork.PeeringRoute) error {
	r.activated++
	return nil
}

func TestPrivatePeeringRestrictsEveryNodeBeforeRepair(t *testing.T) {
	for _, scenario := range []string{"pure-revocation", "repair-failed", "restriction-failed"} {
		t.Run(scenario, func(t *testing.T) {
			store, app, _ := reviewNetworkFixture(t)
			region := "fra1"
			for _, name := range []string{"node-a", "node-b"} {
				_, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: name, TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &region})
				if err != nil {
					t.Fatal(err)
				}
			}
			peer, err := store.CreatePrivateNetwork(t.Context(), state.PrivateNetwork{ID: "peer", AccountID: app.AccountID, Name: "peer", Region: region, CIDR: netip.MustParsePrefix("10.43.0.0/16")})
			if err != nil {
				t.Fatal(err)
			}
			routes := []privatenetwork.PeeringRoute{
				{FromNetworkID: "review-net", DestinationNetwork: peer.ID, DestinationCIDR: peer.CIDR},
				{FromNetworkID: peer.ID, DestinationNetwork: "review-net", DestinationCIDR: netip.MustParsePrefix("10.42.0.0/16")},
			}
			if scenario == "pure-revocation" {
				routes = nil
			}
			router := &revocationFirstRouter{failFabric: true, failRestriction: scenario == "restriction-failed"}
			err = sched.NewPrivateNetworkPeeringRouteApplier(store, router, nil).Apply(t.Context(), app.AccountID, region, routes)
			if router.restricted != 2 || router.activated != 0 || (scenario != "pure-revocation" && (err == nil || router.calls != 0)) {
				t.Fatalf("regional revocation failed: err=%v router=%+v", err, router)
			}
			if scenario == "repair-failed" && router.fabrics != 2 {
				t.Fatal("fixture did not attempt repairs after regional revocation")
			}
			if scenario != "repair-failed" && router.fabrics != 0 || scenario == "pure-revocation" && err != nil {
				t.Fatalf("unnecessary fabric repair: err=%v router=%+v", err, router)
			}
		})
	}
}
