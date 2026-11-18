package internal

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRegisterStatelessRunnerAnswersTheBarrier pins that a runner shim joins
// guest-init's reseed barrier with a well-formed hello and confirms every
// reseed, so guest-init no longer reports it as an uncovered Go process.
func TestRegisterStatelessRunnerAnswersTheBarrier(t *testing.T) {
	// A short path: t.TempDir() exceeds the unix socket path limit on macOS.
	dir, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	t.Setenv("GREGALE_RESEED_SOCKET", socket)
	t.Setenv("GREGALE_RESTORE_RESEED", "")
	if !RegisterStatelessRunner("node22-runner") {
		t.Fatal("runner did not register")
	}
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	hello, err := reader.ReadString('\n')
	if fields := strings.Fields(hello); err != nil || len(fields) != 3 || fields[0] != "hello" || fields[1] != "node22-runner" {
		t.Fatalf("hello = %q, %v; want three fields naming node22-runner", hello, err)
	}
	for range 2 {
		if _, err := conn.Write([]byte("reseed nonce\n")); err != nil {
			t.Fatal(err)
		}
		if reply, err := reader.ReadString('\n'); err != nil || strings.TrimSpace(reply) != "ok" {
			t.Fatalf("reply = %q, %v; want ok", reply, err)
		}
	}
}
