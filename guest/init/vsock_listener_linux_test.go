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

// The adapter's nonblocking descriptor/poller behavior can be exercised with a
// Unix socket on any Linux host. Real AF_VSOCK is required by guest acceptance.
func testGuestVsockListener(t *testing.T) (*guestVsockListener, string) {
	t.Helper()
	// Keep the Unix fixture address below sockaddr_un's limit even when the
	// acceptance runner has a long private TMPDIR.
	dir, err := os.MkdirTemp("/tmp", "gregale-vsock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "listener.sock")
	if err := unix.Bind(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	if err := unix.Listen(fd, 1); err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	ln := &guestVsockListener{socket: os.NewFile(uintptr(fd), "test-vsock-listener")}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, path
}

func TestGuestVsockListenerSupportsDeadlinesAndClose(t *testing.T) {
	ln, path := testGuestVsockListener(t)
	host, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	guest, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	if err := guest.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var data [1]byte
	if _, err := guest.Read(data[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("nonblocking read deadline: %v", err)
	}
	if err := guest.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(guest, data[:]); err != nil || data[0] != 'x' {
		t.Fatalf("accepted connection read: %v", err)
	}
	if _, err := guest.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(host, data[:]); err != nil || data[0] != 'y' {
		t.Fatalf("accepted connection write: %v", err)
	}
	if err := guest.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := guest.Read(data[:]); done <- err }()
	if err := guest.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("closed accepted connection: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing accepted connection left read blocked")
	}
}

func TestGuestVsockListenerCloseUnblocksAccept(t *testing.T) {
	ln, _ := testGuestVsockListener(t)
	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if conn != nil {
			_ = conn.Close()
		}
		done <- err
	}()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("closed listener: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing listener left accept blocked")
	}
}
