package fcvm

// adr: 20261104000101

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

type retryAliasRunner struct {
	bound      map[string]bool
	failDelete bool
	deletes    int
}

func (r *retryAliasRunner) Run(_ context.Context, args []string) error {
	if len(args) >= 4 && args[0] == "ip" && args[1] == "addr" {
		if args[2] == "add" || args[2] == "replace" {
			r.bound[args[3]] = true
		}
		if args[2] == "del" {
			r.deletes++
			if r.failDelete {
				return errors.New("temporary netlink failure")
			}
			delete(r.bound, args[3])
		}
	}
	return nil
}
func TestRuntimeIdentityFailedAliasRemovalIsRetried(t *testing.T) {
	run := &retryAliasRunner{bound: map[string]bool{}}
	m := newTestManager(run, &fakeVMM{})
	ip := netip.MustParseAddr("203.0.113.98")
	m.SetStaticEgressIPAliases([]StaticEgressIPEntry{{AccountID: "acct-audit5", AppID: "app-audit5", IP: ip}})
	run.failDelete = true
	m.SetStaticEgressIPAliases(nil)
	run.failDelete = false
	m.SetStaticEgressIPAliases(nil)
	if run.bound[ip.String()+"/32"] {
		t.Fatalf("revoked alias remains bound after recovery and replay; delete attempts=%d", run.deletes)
	}
}

func TestRuntimeIdentityTransportDriftCannotReportReady(t *testing.T) {
	account, network, region := "acct-drift", "prod", "fra1"
	bridge := privatenetwork.BridgeName(account, network)
	link := privatenetwork.FabricTransportLinkName(account, network)
	namespace := privatenetwork.RouterNamespace(account, region)
	host, peer := privatenetwork.RouterHostLink(account, network), privatenetwork.RouterPeerLink(account, network)
	capture := &fabricReadinessCaptureRunner{outputs: map[string][]byte{
		"ip link show dev tailscale-new":                      []byte("7: tailscale-new: <UP> mtu 1280"),
		"ip link show dev " + bridge:                          []byte("mtu 1230"),
		"ip link show dev " + link:                            []byte("mtu 1230 vxlan id 999 local 100.64.0.99 dev tailscale-old dstport 4789"),
		"ip link show dev " + host:                            []byte("mtu 1230"),
		"ip netns exec " + namespace + " true":                nil,
		"ip -n " + namespace + " link show dev " + peer:       []byte("mtu 1230"),
		"bridge link show dev " + host:                        []byte("master " + bridge),
		"ip -n " + namespace + " -4 -o addr show dev " + peer: []byte("inet 10.42.0.1/24 scope global "),
		"bridge link show dev " + link:                        []byte("master " + bridge),
		"bridge fdb show dev " + link:                         []byte("00:00:00:00:00:00 dst 100.64.0.11 self permanent"),
	}}
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(capture).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{Enabled: true, OverlayInterface: "tailscale-new", LocalAddress: netip.MustParseAddr("100.64.0.10")})
	cidr := netip.MustParsePrefix("10.42.0.0/24")
	peers := []netip.Addr{netip.MustParseAddr("100.64.0.11")}
	if err := m.ReconcilePrivateNetworkFabricWithPeers(t.Context(), account, network, region, cidr, peers); err != nil {
		t.Fatal(err)
	}
	recreated := false
	for _, cmd := range run.commands {
		if strings.Contains(strings.Join(cmd, " "), "type vxlan") {
			recreated = true
		}
	}
	if !recreated {
		t.Fatal("drifted transport was not recreated")
	}
	ready, err := m.CheckPrivateNetworkFabric(t.Context(), account, network, region, cidr, peers, true)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Ready {
		t.Fatalf("stale VXLAN local address, underlay and VNI accepted as ready: %+v", ready)
	}
}

type aliasInventoryCapture struct{ output []byte }

func (c aliasInventoryCapture) RunCapture(context.Context, []string) ([]byte, error) {
	return c.output, nil
}

func TestRuntimeIdentityAliasRemovalRecoversAfterRestart(t *testing.T) {
	run := &retryAliasRunner{bound: map[string]bool{"203.0.113.98/32": true, "203.0.113.99/32": true}}
	capture := aliasInventoryCapture{[]byte("2: br-tenants inet 203.0.113.98/32 scope global br-tenants:se\n2: br-tenants inet 203.0.113.99/32 scope global br-tenants\n")}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(capture)
	m.SetStaticEgressIPAliases(nil)
	if run.bound["203.0.113.98/32"] || !run.bound["203.0.113.99/32"] {
		t.Fatalf("managed recovery changed an operator-owned address: %v", run.bound)
	}
}

func TestRuntimeIdentityUsesDurableTransportVNI(t *testing.T) {
	run := &fakeRunner{}
	called := false
	m := newTestManager(run, &fakeVMM{}).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{Enabled: true, OverlayInterface: "tailscale0", LocalAddress: netip.MustParseAddr("100.64.0.10")}).WithPrivateNetworkTransportIdentitySource(func(_ context.Context, account, network, region string) (uint32, error) {
		called = true
		if account != "account" || network != "network" || region != "fra1" {
			t.Fatal("wrong identity scope")
		}
		return 42, nil
	})
	if err := m.ReconcilePrivateNetworkFabric(t.Context(), "account", "network", "fra1", netip.MustParsePrefix("10.42.0.0/24")); err != nil {
		t.Fatal(err)
	}
	if !called || !run.ran("type vxlan id 42") {
		t.Fatalf("durable transport identity was ignored: %v", run.commands)
	}
}
