//go:build metal

// adr: 20261128090000 — a pinned lease leaves from its static egress address,
// every other lease from the node's. Real packets through the rendered host
// policy, in throwaway namespaces (root, no KVM).
package netns

import (
	"context"
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

// The upstream answers each connection with the source address it saw.
const staticEgressEchoServer = `
import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("198.51.100.10", 443)); s.listen(16)
print("ready", flush=True)
while True:
    c, a = s.accept(); c.sendall(a[0].encode()); c.close()
`

const staticEgressEchoClient = `
import socket
s = socket.create_connection(("198.51.100.10", 443), timeout=3); print(s.recv(64).decode())
`

// TestMetalStaticEgressPacketFlow builds: two lease namespaces on the host
// namespace's br-tenants (10.100.0.2 pinned, 10.100.0.3 not), the host
// namespace with the customer address aliased on the bridge as vmmd does, and
// an upstream namespace behind the host's public interface. The host policy
// is DefaultHostPolicy with one StaticEgressRule, loaded in one nft
// transaction.
func TestMetalStaticEgressPacketFlow(t *testing.T) {
	requireNetnsTools(t)
	const (
		hostNS, pinnedNS, sharedNS, upstreamNS = "fse_host", "fse_pin", "fse_shr", "fse_up"
		customer                               = "203.0.113.42"
		nodePublic                             = "198.51.100.1"
	)
	for _, ns := range []string{hostNS, pinnedNS, sharedNS, upstreamNS} {
		ns := ns
		_, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput()
		runIn(t, "ip", "netns", "add", ns)
		t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput() })
		runIn(t, "ip", "-n", ns, "link", "set", "lo", "up")
	}
	bridge := DefaultHostPolicy.BridgeName
	runIn(t, "ip", "-n", hostNS, "link", "add", bridge, "type", "bridge")
	runIn(t, "ip", "-n", hostNS, "addr", "add", "10.100.0.1/16", "dev", bridge)
	runIn(t, "ip", "-n", hostNS, "addr", "add", customer+"/32", "dev", bridge, "label", bridge+":se")
	runIn(t, "ip", "-n", hostNS, "link", "set", bridge, "up")
	runIn(t, "ip", "netns", "exec", hostNS, "sysctl", "-qw", "net.ipv4.ip_forward=1")
	for i, lease := range []struct{ ns, addr string }{{pinnedNS, "10.100.0.2"}, {sharedNS, "10.100.0.3"}} {
		hostEnd, leaseEnd := []string{"fseh0", "fseh1"}[i], []string{"fsel0", "fsel1"}[i]
		runIn(t, "ip", "link", "add", hostEnd, "netns", hostNS, "type", "veth", "peer", "name", leaseEnd, "netns", lease.ns)
		runIn(t, "ip", "-n", hostNS, "link", "set", hostEnd, "master", bridge)
		runIn(t, "ip", "-n", hostNS, "link", "set", hostEnd, "up")
		runIn(t, "ip", "-n", lease.ns, "addr", "add", lease.addr+"/16", "dev", leaseEnd)
		runIn(t, "ip", "-n", lease.ns, "link", "set", leaseEnd, "up")
		runIn(t, "ip", "-n", lease.ns, "route", "add", "default", "via", "10.100.0.1")
	}
	runIn(t, "ip", "link", "add", "fsepub", "netns", hostNS, "type", "veth", "peer", "name", "fseup", "netns", upstreamNS)
	runIn(t, "ip", "-n", hostNS, "addr", "add", nodePublic+"/24", "dev", "fsepub")
	runIn(t, "ip", "-n", hostNS, "link", "set", "fsepub", "up")
	runIn(t, "ip", "-n", upstreamNS, "addr", "add", "198.51.100.10/24", "dev", "fseup")
	runIn(t, "ip", "-n", upstreamNS, "link", "set", "fseup", "up")
	// The provider routes the additional address to the node.
	runIn(t, "ip", "-n", upstreamNS, "route", "add", customer+"/32", "via", nodePublic)

	policy := DefaultHostPolicy
	policy.PublicIface = "fsepub"
	policy.StaticEgressRules = []StaticEgressRule{{
		PerVMHostIP: netip.MustParseAddr("10.100.0.2"), CustomerIP: netip.MustParseAddr(customer),
		AccountID: "acct-packet", AppID: "app-packet",
	}}
	ruleset := policy.Render()
	load := exec.Command("ip", "netns", "exec", hostNS, "nft", "-f", "-")
	load.Stdin = strings.NewReader(ruleset)
	if out, err := load.CombinedOutput(); err != nil {
		t.Fatalf("load host policy: %v\n%s\nruleset:\n%s", err, out, ruleset)
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := exec.CommandContext(ctx, "ip", "netns", "exec", upstreamNS, "python3", "-c", staticEgressEchoServer)
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Wait() })
	ready := make([]byte, 5)
	if _, err := stdout.Read(ready); err != nil || string(ready) != "ready" {
		t.Fatalf("upstream did not start: %q %v", ready, err)
	}

	source := func(ns string) string {
		t.Helper()
		out, err := exec.Command("ip", "netns", "exec", ns, "python3", "-c", staticEgressEchoClient).CombinedOutput()
		if err != nil {
			t.Fatalf("connect from %s: %v\n%s\nruleset:\n%s", ns, err, out, ruleset)
		}
		return strings.TrimSpace(string(out))
	}
	if got := source(pinnedNS); got != customer {
		t.Fatalf("pinned lease reached the upstream from %s, want its static egress address %s", got, customer)
	}
	if got := source(sharedNS); got != nodePublic {
		t.Fatalf("unpinned lease reached the upstream from %s, want the node address %s", got, nodePublic)
	}
}
