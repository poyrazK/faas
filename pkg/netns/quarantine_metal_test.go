//go:build metal

// adr: 732
//
// Metal data-plane regression for the production-fork quarantine. The unit
// tests pin the rendered argv; this file proves the packet behaviour with
// real namespaces, veths and the full NftCommands ruleset (no Firecracker
// needed). Topology, built fresh for each case:
//
//	guest ns  qg0 10.0.0.2/30 ──veth── tap0 10.0.0.1/30   fork ns (rules here)
//	host ns   vh  10.100.0.1/16 ─veth── vp   10.100.0.240/16
//	          lo  203.0.113.1/32  ("the internet", TCP/443 open)
//
// The same three flows run with Quarantine off (control) and on:
//
//	platform → guest  (host ns → 10.100.0.240:8080, DNAT to the guest)  both: open
//	guest → internet  (guest ns → 203.0.113.1:443)                       off: open, on: blocked
//	guest → fork ns   (guest ns → 10.0.0.1:9000, the input hook)          off: open, on: blocked
//
// The control proves each flow is otherwise reachable, so a blocked result
// is the quarantine and not a broken topology.
//
// Skips when not root on Linux with ip, nft and python3 on PATH.
package netns

import (
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const quarantineMetalPy = `
import socket, sys
mode, host, port = sys.argv[1], sys.argv[2], int(sys.argv[3])
if mode == "serve":
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((host, port)); s.listen(16)
    while True:
        c, _ = s.accept(); c.sendall(b"ok"); c.close()
c = socket.create_connection((host, port), timeout=2)
sys.exit(0 if c.recv(2) == b"ok" else 1)
`

func TestMetalQuarantineDataPlane(t *testing.T) {
	for _, tool := range []string{"ip", "nft", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	if out, err := exec.Command("ip", "netns", "add", "fq_probe").CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	_, _ = exec.Command("ip", "netns", "del", "fq_probe").CombinedOutput()

	for _, tc := range []struct {
		name       string
		quarantine bool
	}{{"open", false}, {"quarantined", true}} {
		t.Run(tc.name, func(t *testing.T) {
			q := newQuarantineTopology(t, "fq"+tc.name[:1], tc.quarantine)
			if !q.connect("h", "10.100.0.240", 8080) {
				t.Errorf("platform → guest via DNAT blocked; the fork must stay reachable from the platform")
			}
			// vmmd's per-instance bridge dials the guest from inside the
			// fork namespace; its replies arrive on the input hook.
			if !q.connect("n", "10.0.0.2", 8080) {
				t.Errorf("fork namespace → guest blocked; vmmd's bridge could not reach the fork")
			}
			if got := q.connect("g", "203.0.113.1", 443); got == tc.quarantine {
				t.Errorf("guest → internet reachable=%v with quarantine=%v", got, tc.quarantine)
			}
			if got := q.connect("g", "10.0.0.1", 9000); got == tc.quarantine {
				t.Errorf("guest → fork namespace reachable=%v with quarantine=%v", got, tc.quarantine)
			}
		})
	}
}

type quarantineTopology struct {
	t        *testing.T
	n, g, h  string
	children []*exec.Cmd
}

func newQuarantineTopology(t *testing.T, prefix string, quarantine bool) *quarantineTopology {
	t.Helper()
	q := &quarantineTopology{t: t, n: prefix + "_n", g: prefix + "_g", h: prefix + "_h"}
	t.Cleanup(q.close)
	for _, ns := range []string{q.n, q.g, q.h} {
		q.must("ip", "netns", "add", ns)
		q.must("ip", "-n", ns, "link", "set", "lo", "up")
	}
	q.must("ip", "-n", q.n, "link", "add", "tap0", "type", "veth", "peer", "name", "qg0", "netns", q.g)
	q.must("ip", "-n", q.n, "link", "add", "vp", "type", "veth", "peer", "name", "vh", "netns", q.h)
	for _, step := range [][]string{
		{"-n", q.n, "addr", "add", "10.0.0.1/30", "dev", "tap0"},
		{"-n", q.n, "addr", "add", "10.100.0.240/16", "dev", "vp"},
		{"-n", q.n, "link", "set", "tap0", "up"},
		{"-n", q.n, "link", "set", "vp", "up"},
		{"-n", q.n, "route", "add", "default", "via", "10.100.0.1"},
		{"-n", q.g, "addr", "add", "10.0.0.2/30", "dev", "qg0"},
		{"-n", q.g, "link", "set", "qg0", "up"},
		{"-n", q.g, "route", "add", "default", "via", "10.0.0.1"},
		{"-n", q.h, "addr", "add", "10.100.0.1/16", "dev", "vh"},
		{"-n", q.h, "addr", "add", "203.0.113.1/32", "dev", "lo"},
		{"-n", q.h, "link", "set", "vh", "up"},
	} {
		q.must(append([]string{"ip"}, step...)...)
	}
	q.must("ip", "netns", "exec", q.n, "sysctl", "-qw", "net.ipv4.ip_forward=1")

	c := NewConfigWithBridge("fork-metal", q.n, "vh", "vp", netip.MustParseAddr("10.100.0.240"), netip.MustParseAddr("10.100.0.1"))
	c.EgressPorts = []uint16{443}
	c.Quarantine = quarantine
	for _, argv := range c.NftCommands() {
		q.must(argv...)
	}

	q.serve(q.g, "10.0.0.2", 8080)
	q.serve(q.h, "203.0.113.1", 443)
	q.serve(q.n, "10.0.0.1", 9000)
	time.Sleep(500 * time.Millisecond)
	return q
}

func (q *quarantineTopology) must(argv ...string) {
	q.t.Helper()
	if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		q.t.Fatalf("%s: %v\n%s", strings.Join(argv, " "), err, out)
	}
}

func (q *quarantineTopology) serve(ns, host string, port int) {
	q.t.Helper()
	cmd := exec.Command("ip", "netns", "exec", ns, "python3", "-c", quarantineMetalPy, "serve", host, fmt.Sprint(port))
	if err := cmd.Start(); err != nil {
		q.t.Fatalf("serve %s %s:%d: %v", ns, host, port, err)
	}
	q.children = append(q.children, cmd)
}

// connect reports whether a TCP connection from the named side ("g" guest,
// "h" host) completes and reads the server's reply within two seconds.
func (q *quarantineTopology) connect(side, host string, port int) bool {
	ns := map[string]string{"g": q.g, "h": q.h, "n": q.n}[side]
	return exec.Command("ip", "netns", "exec", ns, "python3", "-c", quarantineMetalPy, "connect", host, fmt.Sprint(port)).Run() == nil
}

func (q *quarantineTopology) close() {
	for _, cmd := range q.children {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	for _, ns := range []string{q.n, q.g, q.h} {
		_, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput()
	}
}
