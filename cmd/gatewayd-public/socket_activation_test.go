package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// Issue #607 / ADR-068: development keeps the ordinary listener path while
// production fails closed on a partial socket-activation contract.
func TestPublicListenerFallsBackWithoutActivation(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	t.Setenv("LISTEN_FDS", "")
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	listener, err := publicListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("publicListener: %v", err)
	}
	defer listener.Close()
	if !strings.HasPrefix(listener.Addr().String(), "127.0.0.1:") {
		t.Fatalf("listener = %s, want loopback", listener.Addr())
	}
}

// Issue #607 / ADR-068: the systemd-owned descriptor keeps accepting TCP
// handshakes while no gateway process is running. The replacement consumes a
// duplicate and serves the connection that arrived in that restart window.
func TestActivatedListenerQueuesAcrossRestart(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpBase := base.(*net.TCPListener)
	master, err := tcpBase.File()
	_ = base.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	addr := tcpBase.Addr().String()

	first, err := activatedListener(master, addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close() // old gateway has completed its bounded drain

	client, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("connection during restart was refused: %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintln(client, "queued"); err != nil {
		t.Fatal(err)
	}

	replacement, err := activatedListener(master, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	served := make(chan error, 1)
	go func() {
		conn, acceptErr := replacement.Accept()
		if acceptErr != nil {
			served <- acceptErr
			return
		}
		defer conn.Close()
		line, readErr := bufio.NewReader(conn).ReadString('\n')
		if readErr == nil && line != "queued\n" {
			readErr = fmt.Errorf("request = %q", line)
		}
		if readErr == nil {
			_, readErr = fmt.Fprintln(conn, "ready")
		}
		served <- readErr
	}()
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("queued connection response = %q, %v", line, err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

// Issue #607 / ADR-068: a broken unit must fail before falling back to a
// competing bind that would reintroduce the rollout connection gap.
func TestPublicListenerRejectsIncompleteActivation(t *testing.T) {
	t.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
	_ = os.Unsetenv("LISTEN_FDS")
	_, err := publicListener("127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error = %v, want incomplete activation", err)
	}
}

// Issue #607 / ADR-068: accepting multiple inherited listeners makes the
// public bind ambiguous and can route Caddy around the durable socket.
func TestPublicListenerRejectsMultipleDescriptors(t *testing.T) {
	t.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
	t.Setenv("LISTEN_FDS", "2")
	_, err := publicListener("127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "exactly 1") {
		t.Fatalf("error = %v, want descriptor-count rejection", err)
	}
}
