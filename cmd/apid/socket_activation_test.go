// adr: 126
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

func TestAPIDListenerFallsBackWithoutActivation(t *testing.T) {
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	listener, err := apidListener("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("apidListener: %v", err)
	}
	defer listener.Close()
	if !strings.HasPrefix(listener.Addr().String(), "127.0.0.1:") {
		t.Fatalf("listener = %s, want loopback", listener.Addr())
	}
}

func TestActivatedAPIDListenerQueuesAcrossRestart(t *testing.T) {
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

	first, err := activatedAPIDListener(master, addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	client, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("connection during restart was refused: %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintln(client, "queued"); err != nil {
		t.Fatal(err)
	}

	replacement, err := activatedAPIDListener(master, addr)
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

func TestActivatedAPIDListenerAcceptsNarrowerBindThanWildcardConfig(t *testing.T) {
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

	port := tcpBase.Addr().(*net.TCPAddr).Port
	listener, err := activatedAPIDListener(master, fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatalf("narrower activated bind rejected: %v", err)
	}
	_ = listener.Close()

	if _, err := activatedAPIDListener(master, fmt.Sprintf("0.0.0.0:%d", port+1)); err == nil {
		t.Fatal("activated listener accepted a different configured port")
	}
}

func TestAPIDListenerRejectsIncompleteActivation(t *testing.T) {
	t.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
	_ = os.Unsetenv("LISTEN_FDS")
	_, err := apidListener("tcp", "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error = %v, want incomplete activation", err)
	}
}

func TestAPIDListenerRejectsMultipleDescriptors(t *testing.T) {
	t.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
	t.Setenv("LISTEN_FDS", "2")
	_, err := apidListener("tcp", "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "exactly 1") {
		t.Fatalf("error = %v, want descriptor-count rejection", err)
	}
}
