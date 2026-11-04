package fcvm

// adr: 20261103194727

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

func TestNetworkRuntimeRevisionedAllowlistAcknowledgmentRequiresApply(t *testing.T) {
	m := newTestManager(&fakeInputRunner{}, &fakeVMM{})
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "audit3-egress", AppID: "app-audit3-egress", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) })
	wanted := []netip.Prefix{netip.MustParsePrefix("8.8.8.8/32")}
	err = m.UpdateAppEgressPolicy(t.Context(), inst.AppID, 1, wanted, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(inst.Net.EgressAllowlist) == 0 {
		t.Fatalf("revision 1 acknowledged as applied, but live VM still has unrestricted CIDR policy; remembered revision=%d", m.appEgressPolicies[inst.AppID].revision)
	}
}

type repairFabricHost struct {
	links   map[string]bool
	creates map[string]int
}

func (h *repairFabricHost) Run(_ context.Context, argv []string) error {
	if len(argv) > 5 && strings.Join(argv[:4], " ") == "ip link add dev" {
		h.links[argv[4]] = true
		h.creates[argv[4]]++
		return nil
	}
	if len(argv) > 4 && strings.Join(argv[:3], " ") == "ip addr replace" {
		name := argv[len(argv)-1]
		if !h.links[name] {
			return fmt.Errorf("Cannot find device %s", name)
		}
	}
	return nil
}
func (h *repairFabricHost) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	if len(argv) == 5 && strings.Join(argv[:4], " ") == "ip link show dev" {
		if !h.links[argv[4]] {
			return nil, errors.New("link does not exist")
		}
		return []byte("state UP"), nil
	}
	return nil, nil
}
func TestNetworkRuntimeFabricRepairsDeletedLinks(t *testing.T) {
	for _, transport := range []bool{false, true} {
		t.Run(fmt.Sprintf("transport=%t", transport), func(t *testing.T) {
			host := &repairFabricHost{links: map[string]bool{"tailscale0": true}, creates: map[string]int{}}
			m := newTestManager(host, &fakeVMM{}).WithCaptureRunner(host).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{Enabled: transport, OverlayInterface: "tailscale0", LocalAddress: netip.MustParseAddr("100.64.0.10")})
			prefix := netip.MustParsePrefix("10.42.0.0/24")
			if err := m.ReconcilePrivateNetworkFabric(t.Context(), "acct-audit3", "prod", "fra1", prefix); err != nil {
				t.Fatal(err)
			}
			name := privatenetwork.BridgeName("acct-audit3", "prod")
			if transport {
				name = privatenetwork.FabricTransportLinkName("acct-audit3", "prod")
			}
			host.links[name] = false
			err := m.ReconcilePrivateNetworkFabric(t.Context(), "acct-audit3", "prod", "fra1", prefix)
			if err != nil || !host.links[name] {
				t.Fatalf("cached fabric did not recreate deleted %s: creates=%d error=%v", name, host.creates[name], err)
			}
		})
	}
}

func TestNetworkRuntimeStaticPinCanWakeOnFreshNode(t *testing.T) {
	prior := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(prior) })
	const account, app = "acct-audit3-cold", "app-audit3-cold"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetHostRenderer(&fakeHostRenderer{})
	m.SetStaticEgressIPAliases([]StaticEgressIPEntry{{AccountID: account, AppID: app, IP: netip.MustParseAddr("203.0.113.75")}})
	inst, err := m.Wake(t.Context(), WakeRequest{Instance: "audit3-cold", AccountID: account, AppID: app, StaticEgressIP: "203.0.113.75", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanScale, EgressMbit: 10, Snapshot: usableSnapshot()})
	if err != nil {
		t.Fatalf("operator-provisioned pinned app cannot first wake without a prior live-node drift RPC: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) })
}

func TestNetworkRuntimeClearedPinCanWakeAfterScaleToZero(t *testing.T) {
	prior := *netns.ActiveHostPolicyForRender()
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	t.Cleanup(func() { netns.SwapActiveHostPolicy(prior) })
	const account, app = "acct-audit3-clear", "app-audit3-clear"
	t.Cleanup(func() { _ = ReleaseStaticEgressIP(account, app) })
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	m.SetHostRenderer(&fakeHostRenderer{})
	if err := m.UpdateStaticEgressIP(t.Context(), account, app, "203.0.113.75"); err != nil {
		t.Fatal(err)
	}
	req := WakeRequest{Instance: "audit3-before-clear", AccountID: account, AppID: app, StaticEgressIP: "203.0.113.75", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanScale, EgressMbit: 10, Snapshot: usableSnapshot()}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
	m.WithStaticEgressPolicySource(func(context.Context, string, string) (*netip.Addr, error) { return nil, nil })
	// The API clears durable app intent here. fanOutStaticEgressIP sees no
	// live instance, so this node gets no UpdateStaticEgressIP clear RPC.
	req.Instance = "audit3-after-clear"
	req.StaticEgressIP = ""
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatalf("latest cleared production intent refused after scale-to-zero: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), inst.Lease.Instance) })
}
