// adr: 686 — exercise scenario-scoped TCP chaos on private service streams.
package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestServiceTCPChaosUpdatesExistingPooledConnections(t *testing.T) {
	h := newTCPTestHarness(t)
	var mu sync.Mutex
	lease := chaos.Lease{}
	unavailable := false
	h.proxy.chaos.resolve = func(context.Context, string, string) (chaos.Lease, error) {
		mu.Lock()
		defer mu.Unlock()
		if unavailable {
			return chaos.Lease{}, errors.New("run removed")
		}
		return lease, nil
	}
	client, server := net.Pipe()
	defer client.Close()
	target := ServiceTCPTarget{AppID: "cache", ScenarioTestRunID: "run", ScenarioWorkload: "cache"}
	conn, cleanup, err := h.proxy.chaosConn(context.Background(), server, "api", target, 6379)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(h.proxy.chaos.groups) != 1 {
		t.Fatal("session was not registered")
	}
	mu.Lock()
	lease = chaos.Lease{ExpiresAt: time.Now().Add(3 * time.Second), Rules: []chaos.Rule{{To: "cache", Kind: chaos.KindTCPTimeout, Port: 6379, Percent: 100}}}
	mu.Unlock()
	// Wait for the shared controller to refresh; forwarding uses the same socket.
	time.Sleep(2 * serviceTCPChaosRefresh)
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("ok")); done <- err }()
	_ = client.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if _, err := client.Read(make([]byte, 2)); err == nil {
		t.Fatal("pooled connection bypassed timeout")
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	mu.Lock()
	lease = chaos.Lease{}
	mu.Unlock()
	if _, err := io.ReadFull(client, make([]byte, 2)); err != nil {
		t.Fatalf("cleared plan did not resume socket: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	unavailable = true
	mu.Unlock()
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("session survived namespace removal")
	}
	cleanup()
	h.proxy.chaos.mu.Lock()
	defer h.proxy.chaos.mu.Unlock()
	if len(h.proxy.chaos.groups) != 0 {
		t.Fatal("policy watcher leaked")
	}
}

func TestServiceTCPProductionSessionsDoNotReadChaos(t *testing.T) {
	h := newTCPTestHarness(t)
	h.proxy.chaos.resolve = func(context.Context, string, string) (chaos.Lease, error) {
		t.Fatal("production read chaos")
		return chaos.Lease{}, nil
	}
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn, cleanup, err := h.proxy.chaosConn(context.Background(), server, "api", ServiceTCPTarget{AppID: "cache"}, 6379)
	if err != nil || conn != server {
		t.Fatalf("production session changed: %v", err)
	}
	cleanup()
}

func TestServiceTCPChaosWatcherOutlivesFirstSocketAndSharesPorts(t *testing.T) {
	h := newTCPTestHarness(t)
	var mu sync.Mutex
	lease := chaos.Lease{}
	h.proxy.chaos.resolve = func(context.Context, string, string) (chaos.Lease, error) {
		mu.Lock()
		defer mu.Unlock()
		return lease, nil
	}
	target := ServiceTCPTarget{AppID: "cache", ScenarioTestRunID: "run", ScenarioWorkload: "cache"}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstClient, firstServer := net.Pipe()
	defer firstClient.Close()
	_, closeFirst, err := h.proxy.chaosConn(firstCtx, firstServer, "api", target, 6379)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFirst()
	client, server := net.Pipe()
	defer client.Close()
	conn, cleanup, err := h.proxy.chaosConn(context.Background(), server, "api", target, 6380)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	h.proxy.chaos.mu.Lock()
	groups := len(h.proxy.chaos.groups)
	h.proxy.chaos.mu.Unlock()
	if groups != 1 {
		t.Fatalf("watchers = %d, want one across both ports", groups)
	}
	cancelFirst()
	closeFirst()
	mu.Lock()
	lease = chaos.Lease{ExpiresAt: time.Now().Add(3 * time.Second), Rules: []chaos.Rule{{To: "cache", Kind: chaos.KindTCPTimeout, Port: 6380, Percent: 100}}}
	mu.Unlock()
	time.Sleep(2 * serviceTCPChaosRefresh)
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("x")); done <- err }()
	_ = client.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	if _, err := client.Read(make([]byte, 1)); err == nil {
		t.Fatal("watcher stopped when its first socket closed")
	}
	mu.Lock()
	lease = chaos.Lease{}
	mu.Unlock()
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(client, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
