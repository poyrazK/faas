//go:build metal

// adr: 20261107122928 amendment 2 — on a real bridge, a port with
// TenantPortFloodOffCommand does not take another namespace's broadcast or
// multicast, while the host still reaches it through a fresh ARP.
package netns

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMetalTenantPortFloodOff(t *testing.T) {
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("ip (iproute2) not on PATH; install iproute2 on a Linux host to run this gate")
	}
	suffix := strconv.Itoa(os.Getpid() % 100000)
	bridge := "tpf" + suffix
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if out, err := exec.Command("ip", "link", "add", bridge, "type", "bridge").CombinedOutput(); err != nil {
		t.Skipf("cannot create a bridge (need CAP_NET_ADMIN): %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = exec.Command("ip", "link", "del", bridge).CombinedOutput() })
	run("ip", "addr", "add", "10.199.0.1/24", "dev", bridge)
	run("ip", "link", "set", bridge, "up")

	// noisy sends; quiet has flooding off; open keeps the default.
	type member struct{ ns, host, peer, addr string }
	members := map[string]member{}
	for i, role := range []string{"noisy", "quiet", "open"} {
		m := member{
			ns:   fmt.Sprintf("tpf%s-%d", suffix, i),
			host: fmt.Sprintf("tpfh%s%d", suffix, i),
			peer: fmt.Sprintf("tpfp%s%d", suffix, i),
			addr: fmt.Sprintf("10.199.0.%d", 10+i),
		}
		run("ip", "netns", "add", m.ns)
		t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", m.ns).CombinedOutput() })
		run("ip", "link", "add", m.host, "type", "veth", "peer", "name", m.peer)
		run("ip", "link", "set", m.host, "master", bridge)
		if role == "quiet" {
			run(TenantPortFloodOffCommand(m.host)...)
		}
		run("ip", "link", "set", m.host, "up")
		run("ip", "link", "set", m.peer, "netns", m.ns)
		run("ip", "-n", m.ns, "addr", "add", m.addr+"/24", "dev", m.peer)
		run("ip", "-n", m.ns, "link", "set", m.peer, "up")
		members[role] = m
	}
	// Let link-up autoconfiguration (router solicitations, MLD, DAD) settle.
	time.Sleep(6 * time.Second)

	txPackets := func(dev string) uint64 {
		t.Helper()
		b, err := os.ReadFile("/sys/class/net/" + dev + "/statistics/tx_packets")
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	quiet, open, noisy := members["quiet"], members["open"], members["noisy"]
	quiet0, open0 := txPackets(quiet.host), txPackets(open.host)

	// A namespace coming up again: IPv6 router solicitations, MLD reports
	// and duplicate address checks, then ARP broadcasts for an absent lease.
	run("ip", "-n", noisy.ns, "link", "set", noisy.peer, "down")
	run("ip", "-n", noisy.ns, "link", "set", noisy.peer, "up")
	time.Sleep(4 * time.Second)
	_, _ = exec.Command("ip", "netns", "exec", noisy.ns, "ping", "-c", "3", "-W", "1", "10.199.0.99").CombinedOutput()
	time.Sleep(time.Second)

	quietGot, openGot := txPackets(quiet.host)-quiet0, txPackets(open.host)-open0
	t.Logf("frames delivered to the ports: flooding off %d, default %d", quietGot, openGot)
	if openGot < 5 {
		t.Fatalf("the default port took %d frames; the noise did not reach the bridge", openGot)
	}
	// The bridge device's own frames still flood to every port.
	if quietGot > 2 {
		t.Fatalf("the port with flooding off took %d frames of another namespace's noise", quietGot)
	}

	// The host's own ARP still reaches the quiet port.
	run("ip", "neigh", "flush", "dev", bridge)
	run("ping", "-c", "2", "-W", "1", quiet.addr)
}
