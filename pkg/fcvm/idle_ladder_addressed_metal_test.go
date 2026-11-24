//go:build metal

// adr: 20261107122928 amendments 4 and 5 — on a real bridge, the host's ARP
// broadcasts and unicast flooded to every port reach a tenant-style port's
// veth but do not move its namespace's addressed reading, nor does the ICMP
// host unreachable the namespace sends itself when a neighbour it probes never
// answers; a packet addressed to the namespace does.
package fcvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMetalNetnsIngressCountsOnlyAddressedIPv4(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root for a bridge and a network namespace")
	}
	sfx := strconv.Itoa(os.Getpid() % 100000)
	br, ns, vh, vp := "tiabr"+sfx, "tians"+sfx, "tiavh"+sfx, "tiavp"+sfx
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	try := func(args ...string) { _ = exec.Command(args[0], args[1:]...).Run() }
	t.Cleanup(func() {
		try("ip", "link", "del", br)
		try("ip", "link", "del", vh)
		try("ip", "netns", "del", ns)
	})
	run("ip", "link", "add", br, "type", "bridge")
	run("ip", "addr", "add", "10.250.77.1/24", "dev", br)
	run("ip", "link", "set", br, "up")
	run("ip", "netns", "add", ns)
	run("ip", "link", "add", vh, "type", "veth", "peer", "name", vp, "netns", ns)
	run("ip", "link", "set", vh, "master", br)
	run("ip", "link", "set", vh, "up")
	run("ip", "netns", "exec", ns, "ip", "addr", "add", "10.250.77.2/24", "dev", vp)
	run("ip", "netns", "exec", ns, "ip", "link", "set", vp, "up")
	run("ip", "netns", "exec", ns, "ip", "link", "set", "lo", "up")

	// A process inside the namespace, as Firecracker is in production.
	proc := exec.Command("ip", "netns", "exec", ns, "sleep", "120")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	pid := proc.Process.Pid
	self, _ := os.Readlink("/proc/self/ns/net")
	deadline := time.Now().Add(5 * time.Second)
	for {
		link, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "ns", "net"))
		if err == nil && link != self {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the process never entered the namespace")
		}
		time.Sleep(20 * time.Millisecond)
	}
	snmp := func() []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "net", "snmp"))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	addressed := func() uint64 {
		t.Helper()
		v, err := parseAddressedIngress(snmp())
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	settled := func() uint64 {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		last, since := addressed(), time.Now()
		for time.Since(since) < 1500*time.Millisecond {
			if time.Now().After(deadline) {
				t.Fatalf("namespace addressed ingress still moving after 10 s (last %d)", last)
			}
			time.Sleep(100 * time.Millisecond)
			if v := addressed(); v != last {
				last, since = v, time.Now()
			}
		}
		return last
	}
	inReceives := func() uint64 {
		t.Helper()
		v, err := parseSNMPField(snmp(), "Ip:", "InReceives")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	vethTX := func() uint64 {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("/sys/class/net", vh, "statistics", "tx_bytes"))
		if err != nil {
			t.Fatal(err)
		}
		v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	// Resolve the namespace once so the next pings are plain unicast to it.
	run("ping", "-c", "1", "-W", "1", "10.250.77.2")
	// A bridge that has just come up sends its own IGMP membership reports,
	// IPv4 multicast that it floods to every port whatever the port flags, a
	// few times over its first second or so (on gregale-test one landed
	// 0.1-1 s after the ping above). Production's br-tenants has long been
	// up. Take the baseline once the reading has held still.
	baseIn, baseTX := settled(), vethTX()

	// The host's ARP for an address no namespace answers: broadcast.
	try("ping", "-c", "1", "-W", "1", "10.250.77.77")
	// Unicast to a MAC no port has: the bridge floods it to every port.
	run("ip", "neigh", "replace", "10.250.77.88", "lladdr", "02:00:00:00:77:88", "dev", br, "nud", "permanent")
	try("ping", "-c", "1", "-W", "1", "10.250.77.88")
	time.Sleep(200 * time.Millisecond)

	if tx := vethTX(); tx <= baseTX {
		t.Fatalf("veth tx bytes %d → %d: the broadcast and the flood never reached the port", baseTX, tx)
	}
	if in := addressed(); in != baseIn {
		t.Fatalf("namespace addressed ingress %d → %d on traffic not addressed to it", baseIn, in)
	}

	// The namespace probes a neighbour that never answers, as it probes a
	// paused guest; the kernel reports the dropped ping to the namespace's own
	// socket with an ICMP host unreachable over lo.
	rawIn := inReceives()
	try("ip", "netns", "exec", ns, "ping", "-c", "1", "-W", "5", "10.250.77.99")
	if raw := inReceives(); raw <= rawIn {
		t.Fatalf("namespace Ip InReceives %d → %d: no host unreachable was delivered in the namespace", rawIn, raw)
	}
	if in := addressed(); in != baseIn {
		t.Fatalf("namespace addressed ingress %d → %d on its own host unreachable report", baseIn, in)
	}

	run("ping", "-c", "1", "-W", "1", "10.250.77.2")
	if in := addressed(); in <= baseIn {
		t.Fatalf("namespace addressed ingress %d → %d on a ping addressed to it", baseIn, in)
	}
}
