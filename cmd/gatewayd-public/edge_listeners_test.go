package main

// adr: 1736 — gatewayd-public opens every listener before it reports ready, and
// closes what it opened when a later one fails.

import (
	"net"
	"testing"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestOpenEdgeListeners(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	t.Setenv("LISTEN_FDS", "")
	cases := []struct {
		name        string
		withPrivate bool
	}{
		{name: "public and control", withPrivate: false},
		{name: "with the private listener", withPrivate: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub, ctl, priv := freeAddr(t), freeAddr(t), ""
			if tc.withPrivate {
				priv = freeAddr(t)
			}
			l, err := openEdgeListeners(pub, priv, ctl)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				for _, ln := range []net.Listener{l.public, l.private, l.control} {
					if ln != nil {
						_ = ln.Close()
					}
				}
			}()
			if l.public == nil || l.control == nil || (l.private != nil) != tc.withPrivate {
				t.Fatalf("listeners = %+v", l)
			}
		})
	}
}

func TestOpenEdgeListenersClosesOpenedOnesOnFailure(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	t.Setenv("LISTEN_FDS", "")
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	pub, priv := freeAddr(t), freeAddr(t)
	if _, err := openEdgeListeners(pub, priv, busy.Addr().String()); err == nil {
		t.Fatal("opened a control listener on a busy address")
	}
	for _, addr := range []string{pub, priv} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("%s still held after the failed open: %v", addr, err)
		}
		_ = l.Close()
	}
}
