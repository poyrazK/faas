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
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func probeSelf(t *testing.T, network, addr string) (int, string, bool) {
	t.Helper()
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Skipf("cannot listen on %s %s: %v", network, addr, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return probeListeningLinux(os.Getpid(), ln.Addr().(*net.TCPAddr).Port)
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
	if port, _, ok := probeListeningLinux(os.Getpid(), api.DefaultAppPort); ok && port != 0 {
		// Another test in this binary may still hold a listener; only fail if
		// nothing else could explain it.
		t.Logf("probe found port %d with no listener of ours; another test's listener?", port)
	}
	if _, _, ok := probeListeningLinux(-1, api.DefaultAppPort); ok {
		t.Fatal("probe reported a listener for pid -1")
	}
}

// listenTwice opens two listeners on free ports and returns their ports.
func listenTwice(t *testing.T) (first, second int) {
	t.Helper()
	var ports []int
	for range 2 {
		ln, err := net.Listen("tcp", ":0")
		if err != nil {
			t.Skipf("cannot listen: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	return ports[0], ports[1]
}

// An app that also listens on a private TCP port: whichever socket /proc
// lists first, the expected serving port is the one characterized (H10-56).
func TestProbeListeningLinux_PrefersExpectedPort(t *testing.T) {
	a, b := listenTwice(t)
	for _, want := range []int{a, b} {
		if port, _, ok := probeListeningLinux(os.Getpid(), want); !ok || port != want {
			t.Fatalf("probe with expected port %d = (%d, %v)", want, port, ok)
		}
	}
}

// waitForBind settles on the expected port at once, and on another listener
// only after CharacterizationPreferredPortGrace.
func TestWaitForBindPrefersExpectedPortWithinGrace(t *testing.T) {
	a, _ := listenTwice(t)
	clock := time.Unix(1_700_000_000, 0)
	now := func() time.Time {
		clock = clock.Add(100 * time.Millisecond)
		return clock
	}
	args := RunArgs{Manifest: api.AppManifest{Port: a}, AppPID: os.Getpid, Now: now}
	var res CharacterizationResult
	start := clock
	if ok, _, _, _ := waitForBind(context.Background(), args, &res); !ok || res.Port != a {
		t.Fatalf("expected port %d listening: ok=%v port=%d", a, ok, res.Port)
	}
	if waited := clock.Sub(start); waited >= api.CharacterizationPreferredPortGrace {
		t.Fatalf("waited %v for a listening expected port", waited)
	}

	missing := freePort(t)
	args.Manifest.Port = missing
	res = CharacterizationResult{}
	start = clock
	ok, _, _, _ := waitForBind(context.Background(), args, &res)
	if !ok || res.Port == 0 || res.Port == missing {
		t.Fatalf("expected port %d never bound: ok=%v port=%d", missing, ok, res.Port)
	}
	if waited := clock.Sub(start); waited < api.CharacterizationPreferredPortGrace {
		t.Fatalf("settled on port %d after %v, before the %v grace", res.Port, waited, api.CharacterizationPreferredPortGrace)
	}
}

// freePort returns a port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
