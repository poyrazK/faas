//go:build metal

// adr: 20261107122928 amendment 4 — on a real bridge, the host's ARP
// broadcasts and unicast flooded to every port reach a tenant-style port's
// veth but do not move its namespace's Ip InReceives; a packet addressed to
// the namespace does.
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
	inReceives := func() uint64 {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "net", "snmp"))
		if err != nil {
			t.Fatal(err)
		}
		v, err := parseIPInReceives(raw)
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
	time.Sleep(200 * time.Millisecond)
	baseIn, baseTX := inReceives(), vethTX()

	// The host's ARP for an address no namespace answers: broadcast.
	try("ping", "-c", "1", "-W", "1", "10.250.77.77")
	// Unicast to a MAC no port has: the bridge floods it to every port.
	run("ip", "neigh", "replace", "10.250.77.88", "lladdr", "02:00:00:00:77:88", "dev", br, "nud", "permanent")
	try("ping", "-c", "1", "-W", "1", "10.250.77.88")
	time.Sleep(200 * time.Millisecond)

	if tx := vethTX(); tx <= baseTX {
		t.Fatalf("veth tx bytes %d → %d: the broadcast and the flood never reached the port", baseTX, tx)
	}
	if in := inReceives(); in != baseIn {
		t.Fatalf("namespace Ip InReceives %d → %d on traffic not addressed to it", baseIn, in)
	}

	run("ping", "-c", "1", "-W", "1", "10.250.77.2")
	if in := inReceives(); in <= baseIn {
		t.Fatalf("namespace Ip InReceives %d → %d on a ping addressed to it", baseIn, in)
	}
}
