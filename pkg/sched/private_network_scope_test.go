package sched_test

// adr: 20261103235901

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPrivateNetworkPeeringDoesNotSkipTenantsBeyondGlobalAttachmentLimit(t *testing.T) {
	t.Setenv("FAAS_PRIVATE_NETWORK_FABRIC_ENABLED", "1")
	st := state.NewMemStore()
	ctx := t.Context()
	for i := range 1000 {
		_, err := st.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: fmt.Sprintf("other-%d", i), AppID: fmt.Sprintf("other-app-%d", i), NetworkID: "other", Region: "fra1", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/24")}, Status: api.PrivateNetworkAttachmentStatusReady})
		if err != nil {
			t.Fatal(err)
		}
	}
	const acct, app = "acct-audit4-limit", "app-audit4-limit"
	cidrs := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24"), netip.MustParsePrefix("10.43.0.0/24")}
	for i, id := range []string{"left", "right"} {
		if _, err := st.CreatePrivateNetwork(ctx, state.PrivateNetwork{ID: id, Name: id, AccountID: acct, Region: "fra1", CIDR: cidrs[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.UpsertAppPrivateNetworkAttachment(ctx, state.AppPrivateNetworkAttachment{AccountID: acct, AppID: app, NetworkID: "left", Region: "fra1", CIDRs: cidrs[:1], Status: api.PrivateNetworkAttachmentStatusReady}); err != nil {
		t.Fatal(err)
	}
	region := "fra1"
	node, err := st.CreateComputeNode(ctx, state.ComputeNode{Name: "audit4-node", TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 4, AdmissionCeilingMB: 4096, Active: true, Region: &region})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateInstance(ctx, app, "dep-audit4", string(state.StateRunning), 256, node.ID, ""); err != nil {
		t.Fatal(err)
	}
	r := &peeringRouterRecorder{fabrics: map[string]int{}, policies: map[string][]privatenetwork.PeeringRoute{}}
	routes := []privatenetwork.PeeringRoute{{FromNetworkID: "left", DestinationNetwork: "right", DestinationCIDR: cidrs[1]}, {FromNetworkID: "right", DestinationNetwork: "left", DestinationCIDR: cidrs[0]}}
	if err := sched.NewPrivateNetworkPeeringRouteApplier(st, r, nil).Apply(ctx, acct, region, routes); err != nil {
		t.Fatal(err)
	}
	if r.calls == 0 {
		t.Fatalf("peering apply returned success and installed regional policy, but silently skipped the 1001st attachment: L3 directions=%d member updates=%d", len(r.policies[node.ID]), r.calls)
	}
}
