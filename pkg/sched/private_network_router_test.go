package sched_test

// adr: 20261104001001

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type peeringRouterRecorder struct {
	reviewPolicyRouter
	fabrics  map[string]int
	policies map[string][]privatenetwork.PeeringRoute
	fail     bool
}

func (r *peeringRouterRecorder) ReconcilePrivateNetworkFabric(_ context.Context, nodeID, _, _, _ string, _ netip.Prefix) error {
	r.fabrics[nodeID]++
	return nil
}
func (r *peeringRouterRecorder) ReconcilePrivateNetworkPeerings(_ context.Context, nodeID, _, _ string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	if r.fabrics[nodeID] != len(networks) {
		return errors.New("L3 policy preceded fabric setup")
	}
	if r.fail {
		return errors.New("router transaction failed")
	}
	r.policies[nodeID] = append([]privatenetwork.PeeringRoute{}, routes...)
	return nil
}

func TestPrivateNetworkPeeringRequiresRegionalL3Policy(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "converged", true: "failed"}[fail], func(t *testing.T) {
			store, app, _ := reviewNetworkFixture(t)
			ctx := t.Context()
			region := "fra1"
			node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "router-node", TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &region})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateInstance(ctx, app.ID, "dep-router", string(state.StateRunning), 256, node.ID, ""); err != nil {
				t.Fatal(err)
			}
			peer, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{ID: "review-peer", AccountID: app.AccountID, Name: "peer", Region: region, CIDR: netip.MustParsePrefix("10.43.0.0/16")})
			if err != nil {
				t.Fatal(err)
			}
			routes := []privatenetwork.PeeringRoute{
				{FromNetworkID: "review-net", DestinationNetwork: peer.ID, DestinationCIDR: peer.CIDR},
				{FromNetworkID: peer.ID, DestinationNetwork: "review-net", DestinationCIDR: netip.MustParsePrefix("10.42.0.0/16")},
			}
			router := &peeringRouterRecorder{fabrics: map[string]int{}, policies: map[string][]privatenetwork.PeeringRoute{}, fail: fail}
			applier := sched.NewPrivateNetworkPeeringRouteApplier(store, router, nil)
			err = applier.Apply(ctx, app.AccountID, region, routes)
			if fail {
				if err == nil || router.calls != 0 {
					t.Fatalf("failed L3 transaction published app routes: err=%v calls=%d", err, router.calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(router.policies[node.ID]) != 2 || router.calls == 0 {
				t.Fatal("successful sweep omitted fabric policy or app routes")
			}
			router.fabrics = map[string]int{}
			if err := applier.Apply(ctx, app.AccountID, region, nil); err != nil {
				t.Fatal(err)
			}
			if len(router.policies[node.ID]) != 0 {
				t.Fatal("revocation retained old L3 rules")
			}
		})
	}
}

func TestPrivateNetworkPeeringUnsupportedNodeFailsClosed(t *testing.T) {
	store, app, _ := reviewNetworkFixture(t)
	region := "fra1"
	_, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "legacy-router-node", TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &region})
	if err != nil {
		t.Fatal(err)
	}
	router := &reviewPolicyRouter{}
	if err := sched.NewPrivateNetworkPeeringRouteApplier(store, router, nil).Apply(t.Context(), app.AccountID, region, nil); err == nil {
		t.Fatal("unsupported node acknowledged L3 convergence")
	}
	if router.calls != 0 {
		t.Fatal("app routes changed before L3 capability check")
	}
}
