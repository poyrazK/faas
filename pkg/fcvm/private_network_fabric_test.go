// adr: 025

package fcvm

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/privatenetwork"
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
		if strings.Contains(strings.Join(command, " "), "ip link add") {
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
		if strings.Contains(joined, "bridge fdb replace") {
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

func TestReconcilePrivateNetworkFabricWithEmptyPeersFlushesAfterRestart(t *testing.T) {
	run := &fakeRunner{}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(&fabricPresentCaptureRunner{}).WithPrivateNetworkTransport(PrivateNetworkTransportConfig{
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
		if strings.Join(command, " ") == "bridge fdb flush dev "+link {
			return
		}
	}
	t.Fatalf("reconcile after restart did not flush empty peer roster; commands=%v", commands)
}

func TestRemovePrivateNetworkFabricIsReplaySafeWithCaptureRunner(t *testing.T) {
	run := &fakeRunner{failOn: "ip link del"}
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(&fabricMissingCaptureRunner{})
	cidr := netip.MustParsePrefix("10.42.0.0/16")
	if err := m.RemovePrivateNetworkFabric(context.Background(), "acct-a", "prod", "fra1", cidr); err != nil {
		t.Fatalf("replayed remove: %v", err)
	}
}

type fabricMissingCaptureRunner struct{}

func (*fabricMissingCaptureRunner) RunCapture(context.Context, []string) ([]byte, error) {
	return nil, context.Canceled
}

type fabricPresentCaptureRunner struct{}

func (*fabricPresentCaptureRunner) RunCapture(context.Context, []string) ([]byte, error) {
	return nil, nil
}
