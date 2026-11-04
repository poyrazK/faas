package fcvm

// adr: 20261104000101

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/netip"
	"strings"
	"testing"
)

func TestPrivateReplicaAddressesAndConfirmedPromotion(t *testing.T) {
	store := state.NewMemStore()
	network, err := store.CreatePrivateNetwork(t.Context(), state.PrivateNetwork{ID: "net-replicas", Name: "replicas", AccountID: "account", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertAppPrivateNetworkAttachment(t.Context(), state.AppPrivateNetworkAttachment{AccountID: "account", AppID: "app", NetworkID: network.ID, Region: "fra1", Status: "ready", CIDRs: []netip.Prefix{network.CIDR}})
	if err != nil {
		t.Fatal(err)
	}
	runner, vmm := &fakeRunner{}, &fakeVMM{}
	m := newTestManager(runner, vmm).WithPrivateNetworkMembers(func(ctx context.Context, req PrivateNetworkMemberRequest) (PrivateNetworkMemberIdentity, error) {
		return store.EnsurePrivateNetworkMember(ctx, req.AccountID, req.AppID, req.NetworkID, req.InstanceID, req.Eligible)
	}, store.ReleasePrivateNetworkMember)
	wake := func(id string, excluded bool) *Instance {
		t.Helper()
		inst, err := m.Wake(t.Context(), WakeRequest{Instance: id, AccountID: "account", AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot(), PrivateNetworkID: network.ID, PrivateNetworkAddress: "10.42.0.2", PrivateNetworkCIDRs: []string{network.CIDR.String()}, StaticEgressExcluded: excluded})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		return inst
	}
	primary, secondary, stage := wake("replica-one", false), wake("replica-two", false), wake("replica-stage", true)
	if primary.Net.PrivateNetworkAddress == secondary.Net.PrivateNetworkAddress || primary.Net.PrivateNetworkAddress == stage.Net.PrivateNetworkAddress {
		t.Fatal("replica addresses collided")
	}
	if !primary.Net.PrivateNetworkServiceAddress.IsValid() || secondary.Net.PrivateNetworkServiceAddress.IsValid() || stage.Net.PrivateNetworkServiceAddress.IsValid() {
		t.Fatal("service address has multiple/ineligible owners")
	}
	vmm.killErr = errors.New("exit unconfirmed")
	if err := m.Destroy(t.Context(), primary.Lease.Instance); err == nil {
		t.Fatal("failed kill acknowledged cleanup")
	}
	_ = m.ReconcilePrivateNetworkServices(t.Context()) // retained teardown also prevents the repair flight

	if secondary.Net.PrivateNetworkServiceAddress.IsValid() {
		t.Fatal("promoted before previous owner cleanup")
	}
	vmm.killErr = nil
	if err := m.Destroy(t.Context(), primary.Lease.Instance); err != nil {
		t.Fatal(err)
	}
	if err := m.ReconcilePrivateNetworkServices(t.Context()); err != nil {
		t.Fatal(err)
	}
	if secondary.Net.PrivateNetworkServiceAddress.String() != "10.42.0.2" || stage.Net.PrivateNetworkServiceAddress.IsValid() {
		t.Fatal("eligible replica did not take service ownership")
	}
	var policy bool
	for _, argv := range runner.commands {
		if strings.Contains(strings.Join(argv, " "), "ip daddr 10.42.0.2 tcp dport 8080 dnat") {
			policy = true
		}
	}
	if !policy {
		t.Fatal("service owner has no ingress DNAT")
	}
}

func TestStandalonePrivateReplicaRejectsDuplicateAddress(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	req := WakeRequest{Instance: "owner", AccountID: "account", AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot(), PrivateNetworkID: "net-standalone", PrivateNetworkAddress: "10.42.0.2", PrivateNetworkCIDRs: []string{"10.42.0.0/24"}}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "owner") })
	req.Instance = "duplicate"
	if _, err := m.Wake(t.Context(), req); err == nil {
		t.Fatal("duplicate app address admitted without allocator")
	}
}
