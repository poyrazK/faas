//go:build metal

// adr: 1349
package netns

import (
	"context"
	"net/netip"
	"os/exec"
	"strings"
	"testing"
)

// This uses real packets. The public listener is a test-owned endpoint in the
// outside namespace; no request reaches an internet service.
func TestMetalRehearsalNetworkGuardEnforced(t *testing.T) {
	topo := newEgressTopology(t, "rhr", func(c *Config) {
		c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("2001:db8::/32")}
		c.OperatorExceptions = []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}
	})
	if _, ok := topo.try("tcp", "198.51.100.10", 443); !ok {
		t.Fatal("control public TCP 443 is unreachable")
	}
	// Keep a routed reply path during an ordinary-table reset as well; denial
	// must come from the independent guard rather than a missing masquerade.
	runIn(t, "ip", "-n", topo.outside, "route", "add", "10.0.0.0/30", "via", topo.cfg.HostIP.String())
	// Give the same routed topology IPv6, and expose a mock canonical proxy.
	runIn(t, "ip", "-n", topo.guest, "addr", "add", "2001:db8:10::2/64", "dev", "fgstrhr", "nodad")
	runIn(t, "ip", "-n", topo.inst, "addr", "add", "2001:db8:10::1/64", "dev", topo.cfg.Tap, "nodad")
	runIn(t, "ip", "-n", topo.inst, "addr", "add", "2001:db8:20::2/64", "dev", topo.cfg.VethPeer, "nodad")
	runIn(t, "ip", "-n", topo.outside, "addr", "add", "2001:db8:20::1/64", "dev", topo.cfg.VethHost, "nodad")
	runIn(t, "ip", "-n", topo.guest, "-6", "route", "add", "default", "via", "2001:db8:10::1")
	runIn(t, "ip", "-n", topo.outside, "-6", "route", "add", "2001:db8:10::/64", "via", "2001:db8:20::2")
	runIn(t, "ip", "netns", "exec", topo.inst, "sysctl", "-qw", "net.ipv6.conf.all.forwarding=1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := exec.CommandContext(ctx, "ip", "netns", "exec", topo.outside, "python3", "-u", "-c", `
import socket, threading, time
def serve(s):
    while True:
        c,_=s.accept(); c.sendall(b"ok"); c.close()
for args in [(socket.AF_INET,"10.100.0.1",10081),(socket.AF_INET,"10.100.0.1",10083),(socket.AF_INET,"10.100.0.1",10084),(socket.AF_INET,"10.100.0.1",5354),(socket.AF_INET6,"2001:db8:20::1",443)]:
    family,ip,port=args
    s=socket.socket(family); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
    s.bind((ip,port)); s.listen(8)
    threading.Thread(target=serve,args=(s,),daemon=True).start()
def private_dns(s):
    while True:
        d,a=s.recvfrom(2048); s.sendto(b"private:"+d,a)
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.bind(("10.100.0.1",5354))
threading.Thread(target=private_dns,args=(s,),daemon=True).start()
print("ready",flush=True)
time.sleep(120)
`)
	out, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = server.Wait() })
	buf := make([]byte, 6)
	if _, err := out.Read(buf); err != nil {
		t.Fatal(err)
	}
	// Control probes establish connectivity before containment is installed.
	for _, addr := range []string{"10.100.0.1", "2001:db8:20::1"} {
		port := 443
		if addr == "10.100.0.1" {
			port = 10081
		}
		if _, ok := topo.try("tcp", addr, port); !ok {
			t.Fatalf("control listener %s is unreachable", addr)
		}
	}
	cfg := topo.cfg
	cfg.RehearsalRunID = strings.Repeat("a", 32)
	// Apply the new base-chain exception from the actual renderer too. An
	// accept in the independent guard alone cannot override another drop hook.
	for _, argv := range cfg.NftCommands() {
		if strings.Contains(strings.Join(argv, " "), "add rule ip faas forward") && (strings.Contains(strings.Join(argv, " "), "tcp dport 10083 accept") || strings.Contains(strings.Join(argv, " "), "tcp dport 10084 accept")) {
			runIn(t, argv...)
		}
	}
	install := func() {
		t.Helper()
		var script strings.Builder
		for _, argv := range cfg.RehearsalNftCommands() {
			script.WriteString(strings.Join(argv[5:], " "))
			script.WriteByte('\n')
		}
		cmd := exec.Command("ip", "netns", "exec", cfg.Netns, "nft", "-f", "-")
		cmd.Stdin = strings.NewReader(script.String())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("guard: %v %s", err, out)
		}
	}
	install()
	check := func() {
		t.Helper()
		for _, addr := range []string{"198.51.100.10", "2001:db8:20::1"} {
			if _, ok := topo.try("tcp", addr, 443); ok {
				t.Fatalf("public connection escaped: %s", addr)
			}
		}
		if _, ok := topo.try("tcp", "10.100.0.1", 10081); !ok {
			t.Fatal("canonical private proxy is blocked")
		}
		if _, ok := topo.try("tcp", "10.100.0.1", 10083); !ok {
			t.Fatal("contained PostgreSQL private proxy is blocked")
		}
		if _, ok := topo.try("tcp", "10.100.0.1", 10084); !ok {
			t.Fatal("contained object private proxy is blocked")
		}
		if reply, ok := topo.try("udp", "10.100.0.1", 53); !ok || reply != "private:q" {
			t.Fatal("guest DNS was not redirected to the private-only listener")
		}
	}
	check()
	// A full ordinary-policy reset, including its temporary absence, cannot
	// remove containment. Reinstalling the guard is also idempotent.
	for _, argv := range cfg.NftResetCommands() {
		runIn(t, argv...)
	}
	install()
	check()
}
