//go:build metal

// adr: 576 — private TCP service addresses. Drives real packets from a guest
// through the rendered instance ruleset and the rendered host ruleset, and
// reads the original destination back with SO_ORIGINAL_DST the way the
// gatewayd-internal service TCP proxy does.
package netns

import (
	"context"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// serviceHostServer listens where the host policy sends service traffic:
// the TCP proxy port reports the original destination, the HTTP mesh port
// and a stand-in for a public edge on :443 report a fixed tag.
const serviceHostServer = `
import socket, struct, threading, time
SO_ORIGINAL_DST = 80
def listen(port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("10.100.0.1", port)); s.listen(64)
    return s
def proxy():
    s = listen(10082)
    while True:
        c, _ = s.accept()
        raw = c.getsockopt(socket.SOL_IP, SO_ORIGINAL_DST, 16)
        port, ip = struct.unpack("!2xH4s8x", raw)
        c.sendall(("orig:%s:%d" % (socket.inet_ntoa(ip), port)).encode()); c.close()
def tagged(port, tag):
    s = listen(port)
    while True:
        c, _ = s.accept(); c.sendall(tag); c.close()
threading.Thread(target=proxy, daemon=True).start()
threading.Thread(target=tagged, args=(10081, b"http-mesh"), daemon=True).start()
threading.Thread(target=tagged, args=(443, b"public-edge"), daemon=True).start()
print("ready", flush=True)
time.sleep(120)
`

const serviceGuestClient = `
import socket, sys
kind, ip, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
if kind == "tcp":
    s = socket.create_connection((ip, port), timeout=2); print(s.recv(64).decode())
else:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(2)
    s.sendto(b"q", (ip, port)); print(s.recvfrom(64)[0].decode())
`

// loadServiceHostPolicy renders the production host ruleset with the
// service TCP policy into the topology's outside namespace, which plays the
// root namespace: its veth stands in for br-tenants.
func loadServiceHostPolicy(t *testing.T, topo *egressTopology, httpsEnabled bool) {
	t.Helper()
	h := DefaultHostPolicy
	h.BridgeName = topo.cfg.VethHost
	h.ServiceTCP = NewServiceTCPHostPolicy(testServiceAddressCIDR, topo.cfg.HostBridgeIP, 10082, httpsEnabled)
	load := exec.Command("ip", "netns", "exec", topo.outside, "nft", "-f", "-")
	load.Stdin = strings.NewReader(h.Render())
	if out, err := load.CombinedOutput(); err != nil {
		t.Fatalf("load rendered host ruleset: %v\n%s\nruleset:\n%s", err, out, h.Render())
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := exec.CommandContext(ctx, "ip", "netns", "exec", topo.outside, "python3", "-c", serviceHostServer)
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
		t.Fatalf("service host server did not start: %q %v", ready, err)
	}
}

func serviceTry(topo *egressTopology, kind, ip string, port int) (string, bool) {
	out, err := exec.Command("ip", "netns", "exec", topo.guest, "python3", "-c", serviceGuestClient, kind, ip, strconv.Itoa(port)).CombinedOutput()
	return strings.TrimSpace(string(out)), err == nil
}

func TestMetalServiceAddressTCPReachesProxyWithOriginalDestination(t *testing.T) {
	// DNS gating and the 80/443 port policy are on, as for every tenant:
	// service traffic must pass without either.
	topo := newEgressTopology(t, "svc", func(c *Config) {
		c.DNSGated = true
		c.ServiceAddressCIDR = testServiceAddressCIDR
	})
	loadServiceHostPolicy(t, topo, false)

	for _, port := range []int{5432, 6379} {
		want := "orig:198.19.0.7:" + strconv.Itoa(port)
		if reply, ok := serviceTry(topo, "tcp", "198.19.0.7", port); !ok || reply != want {
			t.Fatalf("TCP %d to a service address: ok=%v reply=%q, want %q", port, ok, reply, want)
		}
	}
	if reply, ok := serviceTry(topo, "tcp", "198.19.0.7", 10081); !ok || reply != "http-mesh" {
		t.Fatalf("service address :10081 must reach the HTTP service proxy: ok=%v reply=%q", ok, reply)
	}
	// Without the private HTTPS listener, :443 must not reach whatever binds
	// the host's :443; it lands on the TCP proxy, which refuses it.
	if reply, ok := serviceTry(topo, "tcp", "198.19.0.7", 443); !ok || reply != "orig:198.19.0.7:443" {
		t.Fatalf("service address :443 without private HTTPS: ok=%v reply=%q, want the TCP proxy", ok, reply)
	}
	if reply, ok := serviceTry(topo, "udp", "198.19.0.7", 9999); ok {
		t.Fatalf("UDP to a service address must be dropped, got %q", reply)
	}
	// Ordinary egress policy is untouched for non-service destinations.
	if reply, ok := topo.try("tcp", "198.51.100.10", 8080); ok {
		t.Fatalf("TCP 8080 to a public host must still be dropped, got %q", reply)
	}
}

func TestMetalServiceAddressHTTPSStaysOnHTTPMesh(t *testing.T) {
	topo := newEgressTopology(t, "svh", func(c *Config) {
		c.ServiceAddressCIDR = testServiceAddressCIDR
	})
	loadServiceHostPolicy(t, topo, true)
	if reply, ok := serviceTry(topo, "tcp", "198.19.0.7", 443); !ok || reply != "public-edge" {
		t.Fatalf("with private HTTPS staged, :443 must reach the bridge :443 listener: ok=%v reply=%q", ok, reply)
	}
}

func TestMetalServiceAddressBlockedWithoutAdmission(t *testing.T) {
	topo := newEgressTopology(t, "svn", nil)
	loadServiceHostPolicy(t, topo, false)
	if topo.cfg.ServiceAddressCIDR != (netip.Prefix{}) {
		t.Fatalf("test topology unexpectedly carries %s", topo.cfg.ServiceAddressCIDR)
	}
	if reply, ok := serviceTry(topo, "tcp", "198.19.0.7", 5432); ok {
		t.Fatalf("a namespace without the admission reached a service address: %q", reply)
	}
}
