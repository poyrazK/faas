//go:build metal

// adr: 1027 — the app route to managed databases. Drives real packets from a
// guest through the rendered instance ruleset and the rendered host ruleset,
// and reads the original destination back with SO_ORIGINAL_DST the way the
// gatewayd-internal forwarder (pkg/dbroute) checks it.
package netns

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// databaseRouteHostServer listens where the host policy sends database
// traffic and reports the original destination.
const databaseRouteHostServer = `
import socket, struct, time
SO_ORIGINAL_DST = 80
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("10.100.0.1", 10083)); s.listen(64)
print("ready", flush=True)
while True:
    c, _ = s.accept()
    raw = c.getsockopt(socket.SOL_IP, SO_ORIGINAL_DST, 16)
    port, ip = struct.unpack("!2xH4s8x", raw)
    c.sendall(("orig:%s:%d" % (socket.inet_ntoa(ip), port)).encode()); c.close()
`

func loadDatabaseRouteHostPolicy(t *testing.T, topo *egressTopology) {
	t.Helper()
	h := DefaultHostPolicy
	h.BridgeName = topo.cfg.VethHost
	h.DatabaseRoute = NewDatabaseRouteHostPolicy(testDatabaseRouteAddress, topo.cfg.HostBridgeIP, 10083)
	load := exec.Command("ip", "netns", "exec", topo.outside, "nft", "-f", "-")
	load.Stdin = strings.NewReader(h.Render())
	if out, err := load.CombinedOutput(); err != nil {
		t.Fatalf("load rendered host ruleset: %v\n%s\nruleset:\n%s", err, out, h.Render())
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := exec.CommandContext(ctx, "ip", "netns", "exec", topo.outside, "python3", "-c", databaseRouteHostServer)
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Wait() })
	ready := make([]byte, 6)
	if _, err := stdout.Read(ready); err != nil || !strings.HasPrefix(string(ready), "ready") {
		t.Fatalf("database route host server did not start: %q %v", ready, err)
	}
}

func TestMetalDatabaseRouteReachesForwarderWithOriginalDestination(t *testing.T) {
	// A Free-plan guest: DNS gating on, egress ports 80/443 only. The route
	// must pass without either, and only on the PostgreSQL port.
	topo := newEgressTopology(t, "dbr", func(c *Config) {
		c.DNSGated = true
		c.DatabaseRouteAddress = testDatabaseRouteAddress
	})
	loadDatabaseRouteHostPolicy(t, topo)

	if reply, ok := serviceTry(topo, "tcp", "198.18.0.1", 5432); !ok || reply != "orig:198.18.0.1:5432" {
		t.Fatalf("TCP 5432 to the database route: ok=%v reply=%q", ok, reply)
	}
	for _, port := range []int{5433, 10083, 443} {
		if reply, ok := serviceTry(topo, "tcp", "198.18.0.1", port); ok {
			t.Fatalf("TCP %d to the route address must be dropped, got %q", port, reply)
		}
	}
	if reply, ok := serviceTry(topo, "udp", "198.18.0.1", 5432); ok {
		t.Fatalf("UDP to the route address must be dropped, got %q", reply)
	}
	// The forwarder's bridge port is never directly reachable.
	if reply, ok := serviceTry(topo, "tcp", "10.100.0.1", 10083); ok {
		t.Fatalf("the forwarder port on the bridge must not be reachable directly, got %q", reply)
	}
	// Ordinary egress policy is untouched: 5432 to a public host stays closed.
	if reply, ok := topo.try("tcp", "198.51.100.10", 5432); ok {
		t.Fatalf("TCP 5432 to a public host must still be dropped, got %q", reply)
	}
}

func TestMetalDatabaseRouteBlockedWithoutAdmission(t *testing.T) {
	topo := newEgressTopology(t, "dbn", nil)
	loadDatabaseRouteHostPolicy(t, topo)
	if topo.cfg.DatabaseRouteAddress.IsValid() {
		t.Fatalf("test topology unexpectedly carries %s", topo.cfg.DatabaseRouteAddress)
	}
	if reply, ok := serviceTry(topo, "tcp", "198.18.0.1", 5432); ok {
		t.Fatalf("a namespace without the admission reached the database route: %q", reply)
	}
}
