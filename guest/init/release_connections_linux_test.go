//go:build linux

package main

// adr: 20261109130000 — on a real kernel, an off-guest connection is reset and
// a loopback one is left alone.

import (
	"errors"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// errSkip carries a skip reason out of the namespaced goroutine.
type errSkip string

func (e errSkip) Error() string { return string(e) }

// TestReleaseGuestConnectionsResetsOffGuestOnly runs in a private network
// namespace: releaseGuestConnections acts on every socket of its namespace,
// and on a shared host that would include the test runner's own connections.
func TestReleaseGuestConnectionsResetsOffGuestOnly(t *testing.T) {
	if unix.Geteuid() != 0 {
		t.Skip("needs root for a private network namespace and SOCK_DESTROY")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("needs the ip tool")
	}
	done := make(chan error, 1)
	go func() {
		// The thread stays locked and exits with the goroutine, so the
		// namespace never leaks to another goroutine.
		runtime.LockOSThread()
		done <- releaseInPrivateNetns()
	}()
	err := <-done
	var skip errSkip
	if errors.As(err, &skip) {
		t.Skip(string(skip))
	}
	if err != nil {
		t.Fatal(err)
	}
}

func releaseInPrivateNetns() error {
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		return errSkip(fmt.Sprintf("unshare netns: %v", err))
	}
	// 10.99.0.1 on lo is not a loopback address, so a connection to it is
	// off-guest for the release; 127.0.0.1 stays loopback.
	for _, args := range [][]string{{"link", "set", "lo", "up"}, {"addr", "add", "10.99.0.1/32", "dev", "lo"}} {
		if out, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("ip %v: %v: %s", args, err, out)
		}
	}
	open := func(host string) (net.Conn, net.Conn, func(), error) {
		ln, err := net.Listen("tcp4", host+":0")
		if err != nil {
			return nil, nil, nil, err
		}
		client, err := net.Dial("tcp4", ln.Addr().String())
		if err != nil {
			_ = ln.Close()
			return nil, nil, nil, err
		}
		server, err := ln.Accept()
		if err != nil {
			_ = ln.Close()
			_ = client.Close()
			return nil, nil, nil, err
		}
		return client, server, func() { _ = client.Close(); _ = server.Close(); _ = ln.Close() }, nil
	}
	offClient, _, closeOff, err := open("10.99.0.1")
	if err != nil {
		return err
	}
	defer closeOff()
	loClient, _, closeLo, err := open("127.0.0.1")
	if err != nil {
		return err
	}
	defer closeLo()

	n, err := releaseGuestConnections(time.Now().Add(2 * time.Second))
	if errors.Is(err, unix.EOPNOTSUPP) {
		return errSkip("kernel without CONFIG_INET_DIAG_DESTROY")
	}
	if err != nil {
		return fmt.Errorf("releaseGuestConnections: %w", err)
	}
	// Both ends of the 10.99.0.1 pair have a non-loopback peer.
	if n != 2 {
		return fmt.Errorf("reset %d sockets, want the 2 off-guest ones", n)
	}
	_ = offClient.SetReadDeadline(time.Now().Add(time.Second))
	var timeout net.Error
	if _, err := offClient.Read(make([]byte, 1)); err == nil || (errors.As(err, &timeout) && timeout.Timeout()) {
		return fmt.Errorf("off-guest connection still open after release: %v", err)
	}
	_ = loClient.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := loClient.Write([]byte("x")); err != nil {
		return fmt.Errorf("loopback connection was reset: %w", err)
	}
	return nil
}
