//go:build unix

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// TestListenRealtimeSocketReplacesStaleSocketPrivately covers the path used
// without faas-realtimed.socket: a stale socket file from a crashed process is
// replaced and the new socket is reachable only by its group (ADR-015).
func TestListenRealtimeSocketReplacesStaleSocketPrivately(t *testing.T) {
	dir, err := os.MkdirTemp("", "rt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "realtimed.sock")
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()

	listener, err := listenRealtimeSocket(path)
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	defer listener.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o660 {
		t.Fatalf("socket mode = %o, want 660", mode)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
}
