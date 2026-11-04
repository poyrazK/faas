//go:build metal

// adr: 20261031193635
package netns

import (
	"context"
	"net/netip"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Real packets through the contained branch profile. Listeners live in the
// test-owned outside namespace; no request reaches an internet service.
func TestMetalEnvironmentBranchContainedProfile(t *testing.T) {
	topo := newEgressTopology(t, "ebr", func(c *Config) {
		c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
		c.EgressPorts = []uint16{80, 443, 5432}
	})
	runIn(t, "ip", "-n", topo.outside, "route", "add", "10.0.0.0/30", "via", topo.cfg.HostIP.String())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := exec.CommandContext(ctx, "ip", "netns", "exec", topo.outside, "python3", "-u", "-c", `
import socket, threading, time
def serve(s):
    while True:
        c,_=s.accept(); c.sendall(b"ok"); c.close()
for ip,port in [("10.100.0.1",10081),("10.100.0.1",10083),("198.51.100.10",5432)]:
    s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
    s.bind((ip,port)); s.listen(8)
    threading.Thread(target=serve,args=(s,),daemon=True).start()
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
	// Control probes: everything is reachable before containment.
	for _, probe := range []struct {
		addr string
		port int
	}{{"198.51.100.10", 443}, {"198.51.100.10", 5432}, {"10.100.0.1", 10081}} {
		if _, ok := topo.try("tcp", probe.addr, probe.port); !ok {
			t.Fatalf("control listener %s:%d is unreachable", probe.addr, probe.port)
		}
	}
	cfg := topo.cfg
	cfg.EnvironmentBranchContained = true
	apply := func(cmds [][]string) {
		t.Helper()
		var script strings.Builder
		for _, argv := range cmds {
			script.WriteString(strings.Join(argv[5:], " "))
			script.WriteByte('\n')
		}
		cmd := exec.Command("ip", "netns", "exec", cfg.Netns, "nft", "-f", "-")
		cmd.Stdin = strings.NewReader(script.String())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("nft: %v %s", err, out)
		}
	}
	apply(cfg.EnvironmentBranchNftCommands())
	contained := func(database bool) {
		t.Helper()
		if _, ok := topo.try("tcp", "198.51.100.10", 443); ok {
			t.Fatal("public TCP 443 escaped the contained profile")
		}
		if _, ok := topo.try("tcp", "10.100.0.1", 10083); ok {
			t.Fatal("a non-branch bridge port escaped the contained profile")
		}
		if _, ok := topo.try("tcp", "10.100.0.1", 10081); !ok {
			t.Fatal("the canonical service proxy is blocked")
		}
		if reply, ok := topo.try("udp", "10.100.0.1", 53); !ok || reply != "dns:q" {
			t.Fatalf("the bridge resolver is blocked: ok=%v reply=%q", ok, reply)
		}
		if _, ok := topo.try("tcp", "198.51.100.10", 5432); ok != database {
			t.Fatalf("branch database reachable = %v, want %v", ok, database)
		}
	}
	contained(false)
	// vmmd opens the branch database once the guest resolves its name.
	apply(cfg.EnvironmentBranchDatabaseAddCommands([]netip.Addr{netip.MustParseAddr("198.51.100.10")}, 5432, time.Minute))
	contained(true)
	// An ordinary reset and a re-render keep the profile and its set.
	for _, argv := range cfg.NftResetCommands() {
		runIn(t, argv...)
	}
	apply(cfg.EnvironmentBranchNftCommands())
	contained(true)
}
