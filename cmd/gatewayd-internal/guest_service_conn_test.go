// adr: 384 amendment 1 — guest connections to the service proxy carry no TCP
// keepalive.
package main

import (
	"net"
	"syscall"
	"testing"
)

// acceptedKeepAlive reports SO_KEEPALIVE on the server side of one
// connection accepted through ln.
func acceptedKeepAlive(t *testing.T, ln net.Listener) bool {
	t.Helper()
	t.Cleanup(func() { _ = ln.Close() })
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatalf("accepted %T, want *net.TCPConn", conn)
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var on int
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		on, sockErr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_KEEPALIVE)
	}); err != nil {
		t.Fatal(err)
	}
	if sockErr != nil {
		t.Fatal(sockErr)
	}
	return on != 0
}

func TestGuestServiceListenerTurnsKeepAliveOff(t *testing.T) {
	plain, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// Go's default, which probed idle guests every 15 s.
	if !acceptedKeepAlive(t, plain) {
		t.Fatal("a plain listener's connection has no keepalive; the test no longer shows the default it overrides")
	}
	guest, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if acceptedKeepAlive(t, guestServiceListener{guest}) {
		t.Fatal("guest service connection keeps TCP keepalive on")
	}
}
