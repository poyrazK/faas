package internal

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeReseedBarrier accepts one registration and returns the connection.
func fakeReseedBarrier(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	// Unix socket paths are capped near 104 bytes; t.TempDir() can exceed it.
	dir, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "r.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	conns := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conns <- conn
		}
	}()
	return socket, conns
}

func roundTrip(t *testing.T, conn net.Conn, reader *bufio.Reader, msg string) string {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(line)
}

func TestRegisterRestoreRecycleRecyclesBeforeConfirming(t *testing.T) {
	socket, conns := fakeReseedBarrier(t)
	t.Setenv("GREGALE_RESEED_SOCKET", socket)
	t.Setenv("GREGALE_RESTORE_RESEED", "")
	recycled := make(chan struct{}, 2)
	var fail atomic.Bool
	if !RegisterRestoreRecycle("go124", func() error {
		recycled <- struct{}{}
		if fail.Load() {
			return errors.New("boom")
		}
		return nil
	}) {
		t.Fatal("registration failed")
	}
	conn := <-conns
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	hello, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(hello, "hello go124 ") {
		t.Fatalf("hello = %q, err = %v", hello, err)
	}
	if reply := roundTrip(t, conn, reader, "reseed 00112233445566778899aabbccddeeff\n"); reply != "ok" {
		t.Fatalf("reply = %q, want ok", reply)
	}
	select {
	case <-recycled:
	default:
		t.Fatal("confirmed without recycling")
	}
	fail.Store(true)
	if reply := roundTrip(t, conn, reader, "reseed ffeeddccbbaa99887766554433221100\n"); reply != "err recycle" {
		t.Fatalf("reply = %q, want a failure that fails the barrier closed", reply)
	}
}

func TestRegisterRestoreRecycleSkipsWithoutBarrier(t *testing.T) {
	t.Setenv("GREGALE_RESEED_SOCKET", "")
	if RegisterRestoreRecycle("go124", func() error { return nil }) {
		t.Fatal("registered without a barrier socket")
	}
	socket, _ := fakeReseedBarrier(t)
	t.Setenv("GREGALE_RESEED_SOCKET", socket)
	t.Setenv("GREGALE_RESTORE_RESEED", "off")
	if RegisterRestoreRecycle("go124", func() error { return nil }) {
		t.Fatal("registered although the workload opted out")
	}
}
