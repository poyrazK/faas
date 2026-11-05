package fcvm

// adr: 20261103194727

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type policyReplacementRunner struct {
	fakeInputRunner
	failReplacement bool
	separateDeletes []string
}

func (r *policyReplacementRunner) Run(ctx context.Context, argv []string) error {
	if r.failReplacement && strings.Contains(strings.Join(argv, " "), "nft delete table") {
		r.separateDeletes = append(r.separateDeletes, strings.Join(argv, " "))
	}
	return r.fakeRunner.Run(ctx, argv)
}

func (r *policyReplacementRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	if r.failReplacement {
		return errors.New("injected replacement transaction failure")
	}
	return r.fakeInputRunner.RunInput(ctx, argv, input)
}

func TestPrivateNetworkPolicyFailureKeepsLiveTables(t *testing.T) {
	r := &policyReplacementRunner{}
	m := newTestManager(r, &fakeVMM{})
	cidrs := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "review-r2-policy", AccountID: "acct-r2", AppID: "app-r2",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10,
		Snapshot: usableSnapshot(), PrivateNetworkID: "net-r2", PrivateNetworkAddress: "10.42.0.2", PrivateNetworkCIDRs: []string{"10.42.0.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.failReplacement = false; _ = m.Destroy(context.Background(), inst.Lease.Instance) })
	r.failReplacement = true
	err = m.UpdatePrivateNetworkAttachmentWithPolicy(t.Context(), inst.AppID, "net-r2", netip.MustParseAddr("10.42.0.2"), cidrs, []netip.Prefix{netip.MustParsePrefix("10.42.0.0/25")})
	if err == nil {
		t.Fatal("fixture did not fail the replacement")
	}
	if len(r.separateDeletes) > 0 {
		t.Fatalf("live tables deleted before rejected replacement: %v; error=%v", r.separateDeletes, err)
	}
}

func TestPrivateNetworkPeeringAcceptsCanonicalCIDRs(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	cidrs := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24"), netip.MustParsePrefix("10.43.0.0/24")}
	if err := m.UpdatePrivateNetworkAttachment(t.Context(), "app-r2-right", "right", netip.MustParseAddr("10.43.0.2"), cidrs); err != nil {
		t.Fatalf("valid canonical peering projection rejected: %v", err)
	}
}

func TestPrivateNetworkPeeringWakeUsesMemberSubnetForFirewallAndInterface(t *testing.T) {
	runner := &fakeRunner{}
	m := newTestManager(runner, &fakeVMM{})
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "peering-member", AccountID: "acct-peer", AppID: "app-peer",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10,
		Snapshot: usableSnapshot(), PrivateNetworkID: "net-right", PrivateNetworkAddress: "10.43.0.2",
		PrivateNetworkCIDRs:         []string{"10.42.0.0/24", "10.43.0.0/24"},
		PrivateNetworkFirewallRules: []api.PrivateNetworkFirewallRule{{Direction: "egress", Protocol: "tcp", CIDRs: []string{"10.43.0.0/25"}, Ports: []string{"443"}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) })
	if inst.Net.PrivateNetworkCIDRs[0].String() != "10.43.0.0/24" || !runner.ran("ip addr add 10.43.0.2/24 dev "+inst.Net.PrivateVethPeer) {
		t.Fatalf("peer prefix replaced the member subnet: %+v", inst.Net)
	}
	if err := m.UpdatePrivateNetworkAttachmentWithFirewall(t.Context(), inst.AppID, "net-right", inst.Net.PrivateNetworkAddress,
		[]netip.Prefix{netip.MustParsePrefix("10.42.0.0/24"), netip.MustParsePrefix("10.43.0.0/24")}, nil,
		[]api.PrivateNetworkFirewallRule{{Direction: "egress", Protocol: "tcp", CIDRs: []string{"10.43.0.0/25"}, Ports: []string{"443"}}}); err != nil {
		t.Fatalf("canonical replay rejected the member's firewall: %v", err)
	}
}
