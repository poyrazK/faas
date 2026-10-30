//go:build linux

package main

// adr: 20261029171525

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A socket left by a killed daemon is removed, and a missing one is fine.
func TestRemoveStaleSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "buildkitd.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket missing before the call: %v", err)
	}
	if err := removeStaleSocket(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket still present: %v", err)
	}
	if err := removeStaleSocket(path); err != nil {
		t.Fatalf("missing socket: %v", err)
	}
}
