//go:build linux && metal

package fcvm

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A lease IP survives across VMs, but its veth MAC does not. The bridge's
// neighbor cache belongs to the host namespace and survives veth teardown.
func TestMetalReusedLeaseNeighbor(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires private mount/network namespaces and /run/netns")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	runner := wire.ExecRunner{}
	run := func(argv ...string) {
		t.Helper()
		if err := runner.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	run("ip", "link", "add", netns.TenantBridge, "type", "bridge")
	t.Cleanup(func() { _ = runner.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	// Pin the bridge MAC: automatic selection from random port MACs can
	// change it during recreation and independently flush every neighbor.
	run("ip", "link", "set", netns.TenantBridge, "address", "02:00:00:00:00:01")
	run("ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge)
	run("ip", "link", "set", netns.TenantBridge, "up")
	m := newTestManager(runner, &fakeVMM{})
	// Keep another lease attached, as in the mixed-traffic failure. With no
	// remaining bridge ports, carrier loss flushes the whole neighbor cache
	// and masks stale entries from the lease being recycled.
	other := netns.NewConfig("optneighother", "fc-optneighother", "vhnother", "vpnother", netip.MustParseAddr("10.100.0.3"))
	other.TapUID = 29999
	t.Cleanup(func() {
		for _, argv := range other.TeardownCommands() {
			_ = runner.Run(context.Background(), argv)
		}
	})
	if err := m.setupNetwork(ctx, other); err != nil {
		t.Fatal(err)
	}
	nc := netns.NewConfig("optneigh", "fc-optneigh", "vhoptneigh", "vpoptneigh", netip.MustParseAddr("10.100.0.2"))
	nc.TapUID = 29998
	t.Cleanup(func() {
		for _, argv := range nc.TeardownCommands() {
			_ = runner.Run(context.Background(), argv)
		}
	})
	if err := m.setupNetwork(ctx, nc); err != nil {
		t.Fatalf("setup with no previous neighbor: %v", err)
	}
	peerMAC := func() string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "ip", "-n", nc.Netns, "-j", "link", "show", nc.VethPeer).Output()
		if err != nil {
			t.Fatal(err)
		}
		var links []struct{ Address string }
		if err := json.Unmarshal(out, &links); err != nil || len(links) != 1 {
			t.Fatalf("peer link: %s: %v", out, err)
		}
		return links[0].Address
	}
	neighbors := func() map[string]string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "ip", "-j", "neigh", "show", "dev", netns.TenantBridge).Output()
		if err != nil {
			t.Fatal(err)
		}
		var entries []struct{ Dst, Lladdr string }
		if err := json.Unmarshal(out, &entries); err != nil {
			t.Fatal(err)
		}
		result := make(map[string]string, len(entries))
		for _, entry := range entries {
			result[entry.Dst] = entry.Lladdr
		}
		return result
	}
	oldMAC := peerMAC()
	run("ping", "-n", "-c", "1", "-W", "1", nc.HostIP.String())
	// Refresh REACHABLE explicitly so expiration cannot hide the regression.
	run("ip", "neigh", "replace", nc.HostIP.String(), "lladdr", oldMAC, "nud", "reachable", "dev", netns.TenantBridge)
	const otherIP, otherMAC = "10.100.0.3", "02:00:00:00:00:03"
	run("ip", "neigh", "replace", otherIP, "lladdr", otherMAC, "nud", "reachable", "dev", netns.TenantBridge)
	if got := neighbors(); got[nc.HostIP.String()] != oldMAC || got[otherIP] != otherMAC {
		t.Fatalf("fixture did not retain both reachable neighbors: %v", got)
	}
	if err := m.setupNetwork(ctx, nc); err != nil {
		t.Fatal(err)
	}
	newMAC := peerMAC()
	if oldMAC == newMAC {
		t.Fatal("fixture did not replace the veth MAC")
	}
	got := neighbors()
	if got[otherIP] != otherMAC {
		t.Fatalf("recreating one lease removed another lease's neighbor: %v", got)
	}
	if got[nc.HostIP.String()] == oldMAC {
		t.Fatalf("reused IP still maps to previous veth: old=%s new=%s neighbors=%v", oldMAC, newMAC, got)
	}
	start := time.Now()
	run("ping", "-n", "-c", "1", "-W", "1", nc.HostIP.String())
	if got := neighbors()[nc.HostIP.String()]; got != newMAC {
		t.Fatalf("neighbor after traffic = %s, want new MAC %s", got, newMAC)
	}
	t.Logf("reused_lease_first_packet_ms=%.3f old_mac=%s new_mac=%s", float64(time.Since(start))/float64(time.Millisecond), oldMAC, newMAC)
}
