package fcvm

// adr: 20261103194727

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
)

func TestPrivateNetworkPolicyPreservesResolvedEgress(t *testing.T) {
	r := &fakeInputRunner{}
	m := newTestManager(r, &fakeVMM{})
	m.egressCircuitEnabled = true
	if _, err := m.UpdateEgressCircuitRevision(t.Context(), "app-r2-dns", netns.EgressCircuitSnapshot{Revision: 1,
		Targets: []netns.EgressCircuitTarget{{Addr: netip.MustParseAddr("198.51.100.44"), Port: 443}}}); err != nil {
		t.Fatal(err)
	}
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "review-r2-dns", AppID: "app-r2-dns", AccountID: "acct-r2-dns",
		BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot(),
		PrivateNetworkID: "net-r2", PrivateNetworkAddress: "10.42.0.2", PrivateNetworkCIDRs: []string{"10.42.0.0/24"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) })
	if !inst.Net.DNSGated {
		t.Fatal("fixture is not DNS-gated")
	}
	target := netip.MustParseAddr("198.51.100.10")
	targets := []netip.Addr{target, netip.MustParseAddr("2001:db8::10")}
	if err = m.AllowResolvedEgress(t.Context(), inst.Net.HostIP, targets, time.Hour, 5432); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.input), "add element ip faas egress_resolved { 198.51.100.10") {
		t.Fatal("fixture did not seed the DNS set")
	}
	if err = m.UpdatePrivateNetworkAttachmentWithPolicy(t.Context(), inst.AppID, "net-r2", netip.MustParseAddr("10.42.0.2"), []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}, []netip.Prefix{netip.MustParsePrefix("10.42.0.0/25")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.input), "add set ip faas egress_resolved") {
		t.Fatal("fixture did not rebuild the DNS set")
	}
	for _, want := range []string{
		"add element ip faas egress_resolved { 198.51.100.10",
		"add element ip6 faas egress_resolved { 2001:db8::10",
		"add element ip faas egress_db { 198.51.100.10 . 5432",
		"add element ip6 faas egress_db { 2001:db8::10 . 5432",
		"add element ip faas egress_circuit_open { 198.51.100.44 . 443",
	} {
		if !strings.Contains(string(r.input), want) {
			t.Fatalf("replacement lost dynamic policy %q:\n%s", want, r.input)
		}
	}
	before := r.inputRuns
	if err = m.AllowResolvedEgress(t.Context(), inst.Net.HostIP, targets, time.Hour, 5432); err != nil {
		t.Fatal(err)
	}
	if r.inputRuns != before {
		t.Fatal("replacement unnecessarily invalidated unexpired DNS cache entries")
	}
}
