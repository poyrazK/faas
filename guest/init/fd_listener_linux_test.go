//go:build linux

package main

import (
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestFDListenerServesRawSocket proves the listener works on a socket that
// net.FileListener never sees, with working deadlines on accepted conns.
func TestFDListenerServesRawSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s")
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		t.Fatal(err)
	}
	ln := newFDListener(fd, vsockAddr{port: 1031})
	defer ln.Close()
	if ln.Addr().String() != "vsock:1031" {
		t.Fatalf("addr = %s", ln.Addr())
	}
	go func() {
		client, err := net.Dial("unix", path)
		if err != nil {
			return
		}
		defer client.Close()
		_, _ = client.Write([]byte("ping"))
		buf := make([]byte, 4)
		_, _ = io.ReadFull(client, buf)
		time.Sleep(300 * time.Millisecond)
	}()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("read %q, %v", buf, err)
	}
	if _, err := conn.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := conn.Read(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read past deadline = %v, want deadline exceeded", err)
	}
}
