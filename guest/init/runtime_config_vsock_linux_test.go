//go:build linux

// adr:438
package main

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRuntimeSecretVsockStreamDeadlinesAndClose(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	client := &runtimeConfigVsockConn{File: os.NewFile(uintptr(fds[0]), "vsock-test-client")}
	server := &runtimeConfigVsockConn{File: os.NewFile(uintptr(fds[1]), "vsock-test-server")}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	if err := client.SetDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read deadline: %v", err)
	}
	if _, err := client.Write([]byte{1}); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write deadline: %v", err)
	}
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := server.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := writeRuntimeConfigFrame(client, []byte(`{"accepted":true}`)); err != nil {
		t.Fatal(err)
	}
	body, err := readRuntimeConfigFrame(server)
	if err != nil || string(body) != `{"accepted":true}` {
		t.Fatalf("frame = %q, %v", body, err)
	}
	if err := client.SetDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(client, make([]byte, 1)); done <- err }()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("close did not interrupt read: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("close left a blocked reader")
	}
}

func TestRuntimeSecretVsockConnectDeadline(t *testing.T) {
	if err := waitRuntimeConfigVsockConnect(-1, time.Now().Add(-time.Second)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expired connect deadline = %v", err)
	}
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fds[0]); _ = unix.Close(fds[1]) })
	for {
		_, err := unix.Write(fds[0], make([]byte, 4096))
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	if err := waitRuntimeConfigVsockConnect(fds[0], started.Add(20*time.Millisecond)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("blocked connect deadline = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("connect wait exceeded its budget")
	}
}
