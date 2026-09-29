//go:build metal

// adr: 361 — tenant egress is default-deny: web ports, pinned DNS, per-VM
// rate limits. This drives real packets through the rendered ruleset.
package netns

import (
	"context"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// egressTopology wires three namespaces around one rendered instance
// netns: guest (10.0.0.2) --tap0-- instance --vethPeer-- outside. The
// outside namespace plays both the node bridge (10.100.0.1, where the
// resolver lives) and an internet host (198.51.100.10).
type egressTopology struct {
	t                    *testing.T
	guest, inst, outside string
	cfg                  Config
	serverCancel         context.CancelFunc
}

const egressServer = `
import socket, threading, time
def tcp(ip, port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((ip, port)); s.listen(512)
    while True:
        c, _ = s.accept(); c.sendall(b"ok"); c.close()
def udp(ip, port, tag):
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.bind((ip, port))
    while True:
        d, a = s.recvfrom(2048); s.sendto(tag + d, a)
for args in [(tcp, ("198.51.100.10", 443)), (tcp, ("198.51.100.10", 8080)),
             (tcp, ("10.100.0.1", 53)), (udp, ("198.51.100.10", 9999, b"udp:")),
             (udp, ("10.100.0.1", 53, b"dns:"))]:
    threading.Thread(target=args[0], args=args[1], daemon=True).start()
print("ready", flush=True)
time.sleep(120)
`

const egressClient = `
import socket, sys
kind, ip, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
if kind == "tcp":
    s = socket.create_connection((ip, port), timeout=2); print(s.recv(16).decode())
elif kind == "burst":
    # Parallel connects with a timeout shorter than the 1 s SYN retry, so a
    # rate-limited SYN counts as a failure instead of a delayed success.
    import concurrent.futures
    def one(_):
        try:
            socket.create_connection((ip, port), timeout=0.5).close(); return 1
        except OSError:
            return 0
    with concurrent.futures.ThreadPoolExecutor(20) as ex:
        print(sum(ex.map(one, range(20))))
else:
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM); s.settimeout(2)
    s.sendto(b"q", (ip, port)); print(s.recvfrom(64)[0].decode())
`

func requireNetnsTools(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ip", "nft", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH; this gate needs a Linux host with iproute2, nftables and python3", bin)
		}
	}
	probe := "faas_egress_probe"
	if out, err := exec.Command("ip", "netns", "add", probe).CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", probe).CombinedOutput()
}

func runIn(t *testing.T, argv ...string) string {
	t.Helper()
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, out)
	}
	return string(out)
}

func newEgressTopology(t *testing.T, suffix string, mutate func(*Config)) *egressTopology {
	t.Helper()
	requireNetnsTools(t)
	topo := &egressTopology{t: t, guest: "fe_g_" + suffix, inst: "fe_i_" + suffix, outside: "fe_o_" + suffix}
	for _, ns := range []string{topo.guest, topo.inst, topo.outside} {
		ns := ns
		_, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput()
		runIn(t, "ip", "netns", "add", ns)
		t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput() })
	}
	cfg := NewConfig("egress-"+suffix, topo.inst, "fvh"+suffix, "fvp"+suffix, netip.MustParseAddr("10.100.0.250"))
	cfg.EgressPorts = []uint16{80, 443}
	if mutate != nil {
		mutate(&cfg)
	}
	topo.cfg = cfg

	// guest <-> instance: a veth stands in for the Firecracker TAP.
	runIn(t, "ip", "link", "add", "fgst"+suffix, "netns", topo.guest, "type", "veth", "peer", "name", cfg.Tap, "netns", topo.inst)
	runIn(t, "ip", "-n", topo.guest, "addr", "add", GuestIP+"/30", "dev", "fgst"+suffix)
	runIn(t, "ip", "-n", topo.guest, "link", "set", "fgst"+suffix, "up")
	runIn(t, "ip", "-n", topo.guest, "link", "set", "lo", "up")
	runIn(t, "ip", "-n", topo.guest, "route", "add", "default", "via", "10.0.0.1")
	runIn(t, "ip", "-n", topo.inst, "addr", "add", "10.0.0.1/30", "dev", cfg.Tap)
	runIn(t, "ip", "-n", topo.inst, "link", "set", cfg.Tap, "up")

	// instance <-> outside (bridge + internet host).
	runIn(t, "ip", "link", "add", cfg.VethPeer, "netns", topo.inst, "type", "veth", "peer", "name", cfg.VethHost, "netns", topo.outside)
	runIn(t, "ip", "-n", topo.inst, "addr", "add", cfg.hostCIDR(), "dev", cfg.VethPeer)
	runIn(t, "ip", "-n", topo.inst, "link", "set", cfg.VethPeer, "up")
	runIn(t, "ip", "-n", topo.inst, "route", "add", "default", "via", cfg.HostBridgeIP.String())
	runIn(t, "ip", "netns", "exec", topo.inst, "sysctl", "-qw", "net.ipv4.ip_forward=1")
	runIn(t, "ip", "-n", topo.outside, "addr", "add", cfg.HostBridgeIP.String()+"/16", "dev", cfg.VethHost)
	runIn(t, "ip", "-n", topo.outside, "addr", "add", "198.51.100.10/32", "dev", cfg.VethHost)
	runIn(t, "ip", "-n", topo.outside, "link", "set", cfg.VethHost, "up")
	runIn(t, "ip", "-n", topo.outside, "link", "set", "lo", "up")

	// Load the rendered ruleset the way vmmd does: one nft -f transaction.
	prefix := []string{"ip", "netns", "exec", cfg.Netns, "nft"}
	var script strings.Builder
	for _, argv := range cfg.NftCommands() {
		if len(argv) <= len(prefix) || strings.Join(argv[:len(prefix)], " ") != strings.Join(prefix, " ") {
			t.Fatalf("unexpected nft argv prefix: %v", argv)
		}
		script.WriteString(strings.Join(argv[len(prefix):], " "))
		script.WriteByte('\n')
	}
	load := exec.Command("ip", "netns", "exec", topo.inst, "nft", "-f", "-")
	load.Stdin = strings.NewReader(script.String())
	if out, err := load.CombinedOutput(); err != nil {
		t.Fatalf("load rendered ruleset: %v\n%s\nscript:\n%s", err, out, script.String())
	}

	ctx, cancel := context.WithCancel(context.Background())
	topo.serverCancel = cancel
	server := exec.CommandContext(ctx, "ip", "netns", "exec", topo.outside, "python3", "-c", egressServer)
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
		t.Fatalf("outside server did not start: %q %v", ready, err)
	}
	return topo
}

// try runs one client probe from the guest; ok reports whether it got a reply.
func (topo *egressTopology) try(kind, ip string, port int) (reply string, ok bool) {
	out, err := exec.Command("ip", "netns", "exec", topo.guest, "python3", "-c", egressClient, kind, ip, strconv.Itoa(port)).CombinedOutput()
	return strings.TrimSpace(string(out)), err == nil
}

func (topo *egressTopology) counter(name string) int {
	topo.t.Helper()
	out := runIn(topo.t, "ip", "netns", "exec", topo.inst, "nft", "list", "counter", "ip", "faas", name)
	i := strings.Index(out, "packets ")
	if i < 0 {
		topo.t.Fatalf("counter %s has no packets field:\n%s", name, out)
	}
	fields := strings.Fields(out[i+len("packets "):])
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		topo.t.Fatalf("counter %s: %v", name, err)
	}
	return n
}

func TestMetalTenantEgressPolicyEnforced(t *testing.T) {
	topo := newEgressTopology(t, "pol", nil)

	if reply, ok := topo.try("tcp", "198.51.100.10", 443); !ok || reply != "ok" {
		t.Fatalf("TCP 443 to a public host must pass: ok=%v reply=%q", ok, reply)
	}
	for _, tc := range []struct {
		kind string
		port int
	}{{"tcp", 8080}, {"udp", 9999}} {
		if reply, ok := topo.try(tc.kind, "198.51.100.10", tc.port); ok {
			t.Fatalf("%s %d must be dropped, got reply %q", tc.kind, tc.port, reply)
		}
	}
	// DNS to any resolver is answered by the bridge resolver.
	if reply, ok := topo.try("udp", "8.8.8.8", 53); !ok || reply != "dns:q" {
		t.Fatalf("UDP DNS must be pinned to the bridge resolver: ok=%v reply=%q", ok, reply)
	}
	if reply, ok := topo.try("tcp", "8.8.8.8", 53); !ok || reply != "ok" {
		t.Fatalf("TCP DNS must be pinned to the bridge resolver: ok=%v reply=%q", ok, reply)
	}
	if n := topo.counter(EgressDenyCounterPolicy); n == 0 {
		t.Fatalf("%s did not count the dropped probes", EgressDenyCounterPolicy)
	}
}

func TestMetalTenantEgressEmptyPortSetFailsClosed(t *testing.T) {
	topo := newEgressTopology(t, "emp", func(c *Config) { c.EgressPorts = nil })
	if reply, ok := topo.try("tcp", "198.51.100.10", 443); ok {
		t.Fatalf("an unpopulated egress_ports set must block TCP 443, got %q", reply)
	}
	// Pinned DNS is a platform service and still works.
	if reply, ok := topo.try("udp", "8.8.8.8", 53); !ok || reply != "dns:q" {
		t.Fatalf("pinned DNS must work without egress ports: ok=%v reply=%q", ok, reply)
	}
}

func TestMetalTenantEgressRateLimit(t *testing.T) {
	topo := newEgressTopology(t, "rat", func(c *Config) { c.EgressConnRate, c.EgressConnBurst = 1, 2 })
	reply, _ := topo.try("burst", "198.51.100.10", 443)
	connected, err := strconv.Atoi(reply)
	if err != nil {
		t.Fatalf("burst client output %q: %v", reply, err)
	}
	if connected >= 20 {
		t.Fatal("20 parallel connections at 1/s burst 2 all succeeded; the rate limit is not enforced")
	}
	if n := topo.counter(EgressDenyCounterRate); n == 0 {
		t.Fatalf("%s did not count rate-limited flows (%d of 20 connected)", EgressDenyCounterRate, connected)
	}
	t.Logf("connected under the rate limit: %d of 20", connected)
}

// TestMetalTenantEgressFanoutCounted drives distinct and repeated
// destinations through the fan-out rule (ADR-361 decision 6): each new
// destination address counts once whether the flow is accepted, refused or
// dropped, and pinned DNS never counts.
func TestMetalTenantEgressFanoutCounted(t *testing.T) {
	topo := newEgressTopology(t, "fan", nil)
	for _, ip := range []string{"198.51.100.11", "198.51.100.12"} {
		runIn(t, "ip", "-n", topo.outside, "addr", "add", ip+"/32", "dev", topo.cfg.VethHost)
	}
	topo.try("tcp", "198.51.100.10", 443) // accepted
	topo.try("tcp", "198.51.100.10", 443) // same destination
	topo.try("tcp", "198.51.100.11", 443) // refused (no listener), still a new flow
	topo.try("tcp", "198.51.100.12", 8080)
	topo.try("udp", "198.51.100.12", 9999) // dropped, destination already seen
	topo.try("udp", "8.8.8.8", 53)         // pinned to the bridge resolver
	if n := topo.counter(EgressNewDstCounter); n != 3 {
		t.Fatalf("%s = %d, want 3 distinct destinations", EgressNewDstCounter, n)
	}
}

// TestMetalTenantEgressFloodLimited sends a parallel burst at one
// destination: the per-destination bucket drops the excess (ADR-361
// decision 9) and counts it, while a second destination stays reachable.
func TestMetalTenantEgressFloodLimited(t *testing.T) {
	topo := newEgressTopology(t, "fld", func(c *Config) {
		c.EgressConnRate, c.EgressConnBurst = 1000, 1000
		c.EgressDestConnRate, c.EgressDestConnBurst = 1, 2
	})
	runIn(t, "ip", "-n", topo.outside, "addr", "add", "198.51.100.11/32", "dev", topo.cfg.VethHost)
	reply, _ := topo.try("burst", "198.51.100.10", 443)
	connected, err := strconv.Atoi(reply)
	if err != nil {
		t.Fatalf("burst client output %q: %v", reply, err)
	}
	if connected >= 20 {
		t.Fatal("20 parallel connections to one address at 1/s burst 2 all succeeded; the per-destination limit is not enforced")
	}
	if n := topo.counter(EgressFloodCounter); n == 0 {
		t.Fatalf("%s did not count the dropped flows (%d of 20 connected)", EgressFloodCounter, connected)
	}
	// A different destination has its own bucket.
	if _, ok := topo.try("tcp", "198.51.100.11", 443); !ok {
		// Refused (no listener) still proves the SYN left; a timeout does not.
		if out := runIn(t, "ip", "netns", "exec", topo.inst, "nft", "list", "set", "ip", "faas", EgressDstRateSet); !strings.Contains(out, "198.51.100.11") {
			t.Fatalf("second destination never reached its own bucket:\n%s", out)
		}
	}
}

// TestMetalTenantEgressDNSGated: with DNS gating, TCP to an address the
// guest never resolved is dropped and counted; once vmmd adds it to
// egress_resolved the same connection succeeds, and pinned DNS keeps
// working throughout (ADR-370).
func TestMetalTenantEgressDNSGated(t *testing.T) {
	topo := newEgressTopology(t, "dng", func(c *Config) { c.DNSGated = true })
	if reply, ok := topo.try("tcp", "198.51.100.10", 443); ok {
		t.Fatalf("TCP to an unresolved address must be dropped, got %q", reply)
	}
	if n := topo.counter(EgressUnresolvedCounter); n == 0 {
		t.Fatalf("%s did not count the dropped flow", EgressUnresolvedCounter)
	}
	if reply, ok := topo.try("udp", "8.8.8.8", 53); !ok || reply != "dns:q" {
		t.Fatalf("pinned DNS must work under DNS gating: ok=%v reply=%q", ok, reply)
	}
	for _, argv := range topo.cfg.ResolvedEgressAddCommands([]netip.Addr{netip.MustParseAddr("198.51.100.10")}, 10*time.Minute) {
		runIn(t, argv...)
	}
	if reply, ok := topo.try("tcp", "198.51.100.10", 443); !ok || reply != "ok" {
		t.Fatalf("TCP to a resolved address must pass: ok=%v reply=%q", ok, reply)
	}
}
