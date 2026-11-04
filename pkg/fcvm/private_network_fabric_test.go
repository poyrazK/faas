// adr: 025

package fcvm

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestReconcilePrivateNetworkFabricIsIdempotent(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{})
	cidr := netip.MustParsePrefix("10.42.0.0/16")

	if err := m.ReconcilePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if err := m.ReconcilePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	run.mu.Lock()
	defer run.mu.Unlock()
	var linkAdds int
	for _, command := range run.commands {
		if strings.Contains(strings.Join(command, " "), "ip link add dev "+privatenetwork.BridgeName("acct-a", "prod")) {
			linkAdds++
		}
	}
	if linkAdds != 1 {
		t.Fatalf("bridge link-add calls = %d, want 1; commands=%v", linkAdds, run.commands)
	}
}

func TestReconcilePrivateNetworkFabricAddsTransportOnceAndTearsItDownFirst(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{}).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{
		Enabled:          true,
		OverlayInterface: "tailscale0",
		LocalAddress:     netip.MustParseAddr("100.64.0.10"),
		PeerAddresses:    []netip.Addr{netip.MustParseAddr("100.64.0.12"), netip.MustParseAddr("100.64.0.11")},
	})
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	if err := m.ReconcilePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if err := m.ReconcilePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	link := privatenetwork.FabricTransportLinkName("acct-a", "prod")
	run.mu.Lock()
	commands := append([][]string(nil), run.commands...)
	run.mu.Unlock()
	var transportAdds, flushes, fdbEntries int
	for _, command := range commands {
		joined := strings.Join(command, " ")
		if strings.Contains(joined, "ip link add dev "+link) {
			transportAdds++
		}
		if joined == "bridge fdb flush dev "+link {
			flushes++
		}
		if strings.Contains(joined, "bridge fdb append") {
			fdbEntries++
		}
	}
	if transportAdds != 1 {
		t.Fatalf("transport link-add calls = %d, want 1; commands=%v", transportAdds, commands)
	}
	if flushes != 0 || fdbEntries != 2 {
		t.Fatalf("transport FDB sync counts flush=%d entries=%d, want 0/2; commands=%v", flushes, fdbEntries, commands)
	}

	if err := m.RemovePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("remove: %v", err)
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	transportDelete, bridgeDelete := -1, -1
	for i, command := range run.commands {
		joined := strings.Join(command, " ")
		if strings.Contains(joined, "ip link del dev "+link) {
			transportDelete = i
		}
		if strings.Contains(joined, "ip link del dev "+privatenetwork.BridgeName("acct-a", "prod")) {
			bridgeDelete = i
		}
	}
	if transportDelete < 0 || bridgeDelete < 0 || transportDelete > bridgeDelete {
		t.Fatalf("teardown order transport=%d bridge=%d; commands=%v", transportDelete, bridgeDelete, run.commands)
	}
}

func TestReconcilePrivateNetworkFabricWithPeersRefreshesFDB(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{}).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{
		Enabled:          true,
		OverlayInterface: "tailscale0",
		LocalAddress:     netip.MustParseAddr("100.64.0.10"),
	})
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	firstPeers := []netip.Addr{netip.MustParseAddr("100.64.0.11"), netip.MustParseAddr("100.64.0.12")}
	secondPeers := []netip.Addr{netip.MustParseAddr("100.64.0.11")}
	if err := m.ReconcilePrivateNetworkFabricWithPeers(context.Background(), "acct-a", "prod", "fra1", cidr, firstPeers); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if err := m.ReconcilePrivateNetworkFabricWithPeers(context.Background(), "acct-a", "prod", "fra1", cidr, secondPeers); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	link := privatenetwork.FabricTransportLinkName("acct-a", "prod")
	run.mu.Lock()
	commands := append([][]string(nil), run.commands...)
	run.mu.Unlock()
	var flushes, peer11, peer12 int
	for _, command := range commands {
		joined := strings.Join(command, " ")
		switch {
		case joined == "bridge fdb flush dev "+link:
			flushes++
		case strings.Contains(joined, "dst 100.64.0.11"):
			peer11++
		case strings.Contains(joined, "dst 100.64.0.12"):
			peer12++
		}
	}
	if flushes != 1 || peer11 != 2 || peer12 != 1 {
		t.Fatalf("FDB refresh counts flush=%d peer11=%d peer12=%d; commands=%v", flushes, peer11, peer12, commands)
	}
}

func TestReconcilePrivateNetworkFabricWithEmptyPeersRemovesStaleFloodAfterRestart(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(&fabricStalePeerCaptureRunner{}).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{
		Enabled:          true,
		OverlayInterface: "tailscale0",
		LocalAddress:     netip.MustParseAddr("100.64.0.10"),
	})
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	if err := m.ReconcilePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("reconcile after restart: %v", err)
	}

	link := privatenetwork.FabricTransportLinkName("acct-a", "prod")
	run.mu.Lock()
	commands := append([][]string(nil), run.commands...)
	run.mu.Unlock()
	for _, command := range commands {
		if strings.Join(command, " ") == "bridge fdb del 00:00:00:00:00:00 dev "+link+" dst 100.64.0.11" {
			return
		}
	}
	t.Fatalf("reconcile after restart did not remove stale peer from empty roster; commands=%v", commands)
}

func TestRemovePrivateNetworkFabricIsReplaySafeWithCaptureRunner(t *testing.T) {
	run := &fakeRunner{failOn: "ip link del"}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(&fabricMissingCaptureRunner{})
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	if err := m.RemovePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("replayed remove: %v", err)
	}
}

func TestCheckPrivateNetworkFabricReportsReadyTransport(t *testing.T) {
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	peers := []netip.Addr{netip.MustParseAddr("100.64.0.11"), netip.MustParseAddr("100.64.0.12")}
	link := privatenetwork.FabricTransportLinkName("acct-a", "prod")
	bridge := privatenetwork.BridgeName("acct-a", "prod")
	cap := &fabricReadinessCaptureRunner{outputs: map[string][]byte{
		"ip link show dev " + bridge:                                              nil,
		"ip -d -j link show dev " + link:                                          transportIdentityFixture("acct-a", "prod"),
		"bridge link show dev " + privatenetwork.RouterHostLink("acct-a", "prod"): []byte("master " + bridge),
		"ip -n " + privatenetwork.RouterNamespace("acct-a", "fra1") + " -4 -o addr show dev " + privatenetwork.RouterPeerLink("acct-a", "prod"): []byte("inet 10.42.0.1/16 scope global "),
		"ip link show dev " + link:     nil,
		"bridge link show dev " + link: []byte("6: " + link + ": <BROADCAST> master " + bridge + " state forwarding\n"),
		"bridge fdb show dev " + link: []byte("00:00:00:00:00:00 dst 100.64.0.12 dev " + link + "\n" +
			"00:00:00:00:00:00 dst 100.64.0.11 dev " + link + "\n"),
	}}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithCaptureRunner(cap).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{
		Enabled:          true,
		OverlayInterface: "tailscale0",
		LocalAddress:     netip.MustParseAddr("100.64.0.10"),
	})
	readiness, err := m.CheckPrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr, peers, true)
	if err != nil {
		t.Fatalf("check readiness: %v", err)
	}
	if !readiness.Supported || !readiness.Ready {
		t.Fatalf("readiness = %+v, want supported and ready", readiness)
	}
	if readiness.Detail != "fabric transport ready" {
		t.Fatalf("detail = %q, want ready detail", readiness.Detail)
	}
}

func TestCheckPrivateNetworkFabricReportsFDBDrift(t *testing.T) {
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	peer := netip.MustParseAddr("100.64.0.11")
	link := privatenetwork.FabricTransportLinkName("acct-a", "prod")
	bridge := privatenetwork.BridgeName("acct-a", "prod")
	cap := &fabricReadinessCaptureRunner{outputs: map[string][]byte{
		"ip link show dev " + bridge:                                              nil,
		"ip -d -j link show dev " + link:                                          transportIdentityFixture("acct-a", "prod"),
		"bridge link show dev " + privatenetwork.RouterHostLink("acct-a", "prod"): []byte("master " + bridge),
		"ip -n " + privatenetwork.RouterNamespace("acct-a", "fra1") + " -4 -o addr show dev " + privatenetwork.RouterPeerLink("acct-a", "prod"): []byte("inet 10.42.0.1/16 scope global "),
		"ip link show dev " + link:     nil,
		"bridge link show dev " + link: []byte("6: " + link + ": <BROADCAST> master " + bridge + " state forwarding\n"),
		"bridge fdb show dev " + link:  []byte("00:00:00:00:00:00 dst 100.64.0.12 dev " + link + "\n"),
	}}
	m := newTestManager(&fakeRunner{}, &fakeVMM{}).WithCaptureRunner(cap).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{
		Enabled:          true,
		OverlayInterface: "tailscale0",
		LocalAddress:     netip.MustParseAddr("100.64.0.10"),
	})
	readiness, err := m.CheckPrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr, []netip.Addr{peer}, true)
	if err != nil {
		t.Fatalf("check readiness: %v", err)
	}
	if !readiness.Supported || readiness.Ready || !strings.Contains(readiness.Detail, "FDB drift") {
		t.Fatalf("readiness = %+v, want supported, not ready, FDB drift", readiness)
	}
}

type fabricMissingCaptureRunner struct{}

func (*fabricMissingCaptureRunner) RunCapture(context.Context, []string) ([]byte, error) {
	return nil, context.Canceled
}

type fabricReadinessCaptureRunner struct {
	outputs map[string][]byte
}

func (f *fabricReadinessCaptureRunner) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	if output, ok := f.outputs[strings.Join(argv, " ")]; ok {
		return output, nil
	}
	return nil, fmt.Errorf("unexpected capture command %q", strings.Join(argv, " "))
}

type fabricStalePeerCaptureRunner struct{}

func (*fabricStalePeerCaptureRunner) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	if len(argv) > 3 && argv[0] == "ip" && argv[1] == "-d" {
		return transportIdentityFixture("acct-a", "prod"), nil
	}
	if len(argv) > 2 && argv[0] == "bridge" && argv[1] == "fdb" {
		return []byte("00:00:00:00:00:00 dst 100.64.0.11 self permanent"), nil
	}
	return nil, nil
}

func transportIdentityFixture(account, network string) []byte {
	return []byte(fmt.Sprintf(`[{"link":"tailscale0","linkinfo":{"info_kind":"vxlan","info_data":{"id":%d,"local":"100.64.0.10","dstport":4789,"learning":false}}}]`, state.PreferredPrivateNetworkVNI(account, network)))
}
