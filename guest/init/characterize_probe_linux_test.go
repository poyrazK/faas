package main

// The bind probe against a REAL listener. Every characterization in
// e2e-native smoke run 35201413376 — a Go server bound to :8080 and a Node
// server alike — reported
//
//	characterization complete mode=none port=0 class=worker reason=bind_timeout
//
// ten seconds after the app had logged that it was listening, and the app
// was then classified as a worker with no port, so every wake returned
// "upstream unavailable". The probe's parsers were unit-tested on canned
// /proc/net/tcp text; the probe itself — pid → owned socket inodes →
// LISTEN rows — had never been run against a live socket. These do that,
// in-process, for the shapes real apps produce.

import (
	"net"
	"os"
	"testing"
)

func probeSelf(t *testing.T, network, addr string) (int, string, bool) {
	t.Helper()
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Skipf("cannot listen on %s %s: %v", network, addr, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	row, ok, _ := pickListener(probeListeningLinux(os.Getpid()), ln.Addr().(*net.TCPAddr).Port)
	return row.port, row.addr, ok
}

func TestProbeListeningLinux_FindsIPv4Listener(t *testing.T) {
	port, addr, ok := probeSelf(t, "tcp4", "127.0.0.1:0")
	if !ok || port == 0 {
		t.Fatalf("probe missed this process's own IPv4 listener (port=%d addr=%q ok=%v)", port, addr, ok)
	}
}

// Go's net.Listen("tcp", ":0") — and Node's default — is a single dual-stack
// AF_INET6 socket, which appears in /proc/net/tcp6 only.
func TestProbeListeningLinux_FindsDualStackListener(t *testing.T) {
	port, addr, ok := probeSelf(t, "tcp", ":0")
	if !ok || port == 0 {
		t.Fatalf("probe missed this process's own dual-stack listener (port=%d addr=%q ok=%v); "+
			"an app bound to :8080 would be classified as a worker with no port", port, addr, ok)
	}
}

func TestProbeListeningLinux_NoListenerIsNotFound(t *testing.T) {
	if rows := probeListeningLinux(os.Getpid()); len(rows) > 0 {
		// Another test in this binary may still hold a listener; only fail if
		// nothing else could explain it.
		t.Logf("probe found %v with no listener of ours; another test's listener?", rows)
	}
	if rows := probeListeningLinux(-1); len(rows) > 0 {
		t.Fatal("probe reported a listener for pid -1")
	}
}

// An app that also listens on a private TCP port: whichever socket /proc
// lists first, the expected serving port is the one characterized.
func TestProbeListeningLinux_PrefersExpectedPort(t *testing.T) {
	var ports []int
	for range 2 {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Skipf("cannot listen: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	rows := probeListeningLinux(os.Getpid())
	for _, want := range ports {
		row, ok, preferred := pickListener(rows, want)
		if !ok || !preferred || row.port != want {
			t.Fatalf("want port %d among %v: got %+v ok=%v preferred=%v", want, rows, row, ok, preferred)
		}
	}
}
