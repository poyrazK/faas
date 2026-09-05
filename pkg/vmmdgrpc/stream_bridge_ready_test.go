//go:build linux || darwin

package vmmdgrpc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStreamBridgeReadinessWaitsForListen(t *testing.T) {
	path := bridgeReadyTestPath(t)
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	unix.CloseOnExec(fd)
	t.Cleanup(func() { _ = unix.Close(fd) })
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	// bind creates the path, but a client gets ECONNREFUSED until listen.
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- waitForUnixSock(path, time.Second) }()
	select {
	case err := <-done:
		t.Fatalf("readiness returned before listen: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := unix.Listen(fd, 8); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readiness did not observe the listening socket")
	}
}

func TestStreamBridgeReadinessRejectsUnusablePaths(t *testing.T) {
	for _, kind := range []string{"missing", "regular file", "stale socket"} {
		t.Run(kind, func(t *testing.T) {
			path := bridgeReadyTestPath(t)
			switch kind {
			case "regular file":
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "stale socket":
				ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				ln.SetUnlinkOnClose(false)
				if err := ln.Close(); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now()
			if err := waitForUnixSock(path, 20*time.Millisecond); err == nil {
				t.Fatal("unusable path reported ready")
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("readiness timeout was not bounded: %s", elapsed)
			}
		})
	}
}

func TestStreamBridgeReadinessProbesWithoutRetry(t *testing.T) {
	path := bridgeReadyTestPath(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := waitForUnixSock(path, 0); err != nil {
		t.Fatal(err)
	}
	if err := waitForUnixSock(path+"-missing", 0); err == nil {
		t.Fatal("missing socket reported ready")
	}
}

func bridgeReadyTestPath(t *testing.T) string {
	t.Helper()
	// macOS's default test temp directory can exceed the Unix path limit.
	dir, err := os.MkdirTemp("/tmp", "bridge-ready-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "bridge.sock")
}
