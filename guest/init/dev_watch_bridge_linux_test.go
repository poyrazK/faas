//go:build linux

package main

import (
	"bufio"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
)

// The bridge carries a request and its response both ways and ends the
// connection when the server does.
func TestStartDevWatchBridgeProxies(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backend.Close() }()
	go func() {
		c, err := backend.Accept()
		if err != nil {
			return
		}
		line, _ := bufio.NewReader(c).ReadString('\n')
		_, _ = io.WriteString(c, "echo "+line)
		_ = c.Close()
	}()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// 127.0.0.2 stands in for the guest address: another loopback IP.
	ln, err := startDevWatchBridge(net.IPv4(127, 0, 0, 2), 0, backend.Addr().String(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := io.WriteString(c, "hello\n"); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(c)
	if err != nil || strings.TrimSpace(string(got)) != "echo hello" {
		t.Fatalf("bridged reply = %q, %v", got, err)
	}
}
