//go:build metal

// adr: 031
// adr: 361
package netns

import (
	"context"
	"io"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// An outbound allowlist must preserve published ingress and port retargeting,
// while leaving unpublished guest ports and unlisted outbound flows denied.
func TestMetalEgressAllowlistPreservesPublishedIngress(t *testing.T) {
	topo := newEgressTopology(t, "pub", func(c *Config) {
		c.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.11/32")}
		c.DNSGated = true
	})
	const server = `
import socket, threading, time
def serve(s, tag):
    while True:
        c, _ = s.accept(); c.sendall(tag); c.close()
for port, tag in [(8080, b"published"), (9090, b"retargeted"), (9091, b"private")]:
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind(("10.0.0.2", port)); s.listen(16)
    threading.Thread(target=serve, args=(s, tag), daemon=True).start()
print("ready", flush=True)
time.sleep(120)
`
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "ip", "netns", "exec", topo.guest, "python3", "-c", server)
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = command.Wait() })
	ready := make([]byte, 6)
	if _, err := io.ReadFull(stdout, ready); err != nil || string(ready) != "ready\n" {
		t.Fatal("guest TCP listeners did not start")
	}
	fromOutside := func(address string, port int) (string, bool) {
		output, err := exec.Command("ip", "netns", "exec", topo.outside, "python3", "-c", egressClient, "tcp", address, strconv.Itoa(port)).CombinedOutput()
		return strings.TrimSpace(string(output)), err == nil
	}
	if reply, ok := fromOutside(topo.cfg.HostIP.String(), AppPort); !ok || reply != "published" {
		t.Fatal("egress allowlist blocked the published application port")
	}
	topo.cfg.GuestAppPort = 9090
	for _, argv := range topo.cfg.RetargetAppPortCommands() {
		runIn(t, argv...)
	}
	if reply, ok := fromOutside(topo.cfg.HostIP.String(), AppPort); !ok || reply != "retargeted" {
		t.Fatal("published ingress did not follow the retargeted application port")
	}
	// Ensure the negative probe has a route to a real listening guest port.
	runIn(t, "ip", "-n", topo.outside, "route", "add", "10.0.0.0/30", "via", topo.cfg.HostIP.String())
	if _, ok := fromOutside(GuestIP, 9091); ok {
		t.Fatal("unpublished guest port became reachable")
	}
	if _, ok := topo.try("tcp", "198.51.100.10", 443); ok || topo.counter(EgressUnresolvedCounter) == 0 {
		t.Fatal("published ingress weakened the outbound DNS/allowlist policy")
	}
}
