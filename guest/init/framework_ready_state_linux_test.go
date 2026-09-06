//go:build linux

package main

import (
	"bufio"
	"net"
	"testing"

	"github.com/onebox-faas/faas/pkg/frameworkready"
)

func TestFrameworkReadyProxyRetainsReplayableSignal(t *testing.T) {
	state := &frameworkReadyState{}
	if state.status().Ready {
		t.Fatal("ready before runner signal")
	}
	for _, line := range []string{"node22 125\n", "node22 999\n"} {
		a, b := net.Pipe()
		done := make(chan struct{})
		go func() { handleFrameworkReadyConn(a, state); close(done) }()
		if _, err := b.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
		reply, err := bufio.NewReader(b).ReadString('\n')
		_ = b.Close()
		<-done
		if err != nil || reply != "ok\n" {
			t.Fatalf("reply=%q err=%v", reply, err)
		}
	}
	for range 3 {
		if got := state.status(); got != (frameworkready.Status{Ready: true, WarmupMs: 125}) {
			t.Fatalf("replay changed: %+v", got)
		}
	}
}
func TestFrameworkReadyProxyRejectsBadSignal(t *testing.T) {
	state := &frameworkReadyState{}
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() { handleFrameworkReadyConn(a, state); close(done) }()
	_, _ = b.Write([]byte("node22 -1\n"))
	_, _ = bufio.NewReader(b).ReadString('\n')
	_ = b.Close()
	<-done
	if state.status().Ready {
		t.Fatal("malformed signal made runtime ready")
	}
}
