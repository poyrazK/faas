package main

import (
	"net"
	"testing"
)

// adr: 1170 — a watch-mode development server is bridged from the runtime
// port unless the host can already reach it there.
func TestDevWatchBridgeTarget(t *testing.T) {
	guest := net.IPv4(10, 0, 0, 2)
	sock := func(ip string, port int) listenSocket { return listenSocket{IP: net.ParseIP(ip), Port: port} }
	for _, tc := range []struct {
		name      string
		listeners []listenSocket
		want      string // "" means no bridge
	}{
		{name: "no listener yet"},
		{name: "runtime port on every interface", listeners: []listenSocket{sock("0.0.0.0", 3000)}},
		{name: "runtime port dual-stack", listeners: []listenSocket{sock("::", 3000)}},
		{name: "runtime port on the guest address", listeners: []listenSocket{sock("10.0.0.2", 3000)}},
		{name: "vite default", listeners: []listenSocket{sock("::1", 5173)}, want: "[::1]:5173"},
		{name: "vite --host", listeners: []listenSocket{sock("0.0.0.0", 5173)}, want: "127.0.0.1:5173"},
		{name: "astro --host dual-stack", listeners: []listenSocket{sock("::", 4321)}, want: "[::1]:4321"},
		{name: "runtime port on loopback", listeners: []listenSocket{sock("127.0.0.1", 3000)}, want: "127.0.0.1:3000"},
		{name: "runtime port preferred", listeners: []listenSocket{sock("127.0.0.1", 24678), sock("127.0.0.1", 3000)}, want: "127.0.0.1:3000"},
		{name: "first listener otherwise", listeners: []listenSocket{sock("::1", 5173), sock("127.0.0.1", 24678)}, want: "[::1]:5173"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := devWatchBridgeTarget(3000, guest, tc.listeners)
			if ok != (tc.want != "") || got != tc.want {
				t.Fatalf("devWatchBridgeTarget = %q, %t; want %q", got, ok, tc.want)
			}
		})
	}
}

func TestSameListeners(t *testing.T) {
	a := []listenSocket{{IP: net.ParseIP("::1"), Port: 5173}}
	if !sameListeners(a, []listenSocket{{IP: net.ParseIP("::1"), Port: 5173}}) {
		t.Fatal("identical scans differ")
	}
	if sameListeners(a, nil) || sameListeners(a, []listenSocket{{IP: net.ParseIP("::1"), Port: 5174}}) {
		t.Fatal("different scans compare equal")
	}
}
