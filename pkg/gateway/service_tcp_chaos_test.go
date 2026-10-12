// adr: 981 — exercise scenario-scoped TCP chaos on private service streams.
package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
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

func TestServiceTCPAttemptsIncludeFailedConnectionsAndExcludeDeniedRoutes(t *testing.T) {
	for _, kind := range []string{chaos.KindTCPConnectRefused, chaos.KindTCPTimeout} {
		t.Run(kind, func(t *testing.T) {
			h := newTCPTestHarness(t)
			target := h.targets[netip.MustParseAddr("198.19.0.7")]
			target.ScenarioTestRunID, target.ScenarioWorkload = "run", "database"
			h.targets[netip.MustParseAddr("198.19.0.7")] = target
			rule := chaos.Rule{From: "api", To: "database", Kind: kind, Percent: 100, Port: 5432}
			h.proxy.chaos.resolve = func(context.Context, string, string) (chaos.Lease, error) {
				return chaos.Lease{Generation: "generation", ExpiresAt: time.Now().Add(time.Minute), Rules: []chaos.Rule{rule}}, nil
			}
			attempts := 0
			h.proxy.chaos.observeAttempt = func(run, caller, generation string, observed chaos.Rule) {
				if run != "run" || caller != "api" || generation != "generation" || observed.Port != 5432 {
					t.Fatalf("wrong attempt scope: %s %s %s %+v", run, caller, generation, observed)
				}
				attempts++
			}
			h.provider.set("db", ServiceEndpoint{InstanceID: "dead", NodeID: "n1", Port: 8080})
			h.dead["dead"] = true
			outcome, reason := h.serve(t, "caller", "198.19.0.7:5432")
			wantOutcome, wantReason := "error", ""
			if kind == chaos.KindTCPTimeout {
				wantOutcome, wantReason = "", "no_replica"
			}
			if reason != wantReason || outcome != wantOutcome || attempts != 1 {
				t.Fatalf("failed connection missing: %s %s attempts=%d", outcome, reason, attempts)
			}
			h.authzErr = ErrServiceProxyDenied
			h.serve(t, "caller", "198.19.0.7:5432")
			if attempts != 1 {
				t.Fatal("counted denied route")
			}
		})
	}
}

func TestServiceTCPAttemptsCountOnceWithoutPayloadOrPoolRefresh(t *testing.T) {
	h := newTCPTestHarness(t)
	lease := chaos.Lease{Generation: "one", ExpiresAt: time.Now().Add(time.Minute), Rules: []chaos.Rule{{From: "api", To: "database", Kind: chaos.KindTCPTimeout, Percent: 100, Port: 5432}}}
	h.proxy.chaos.resolve = func(context.Context, string, string) (chaos.Lease, error) { return lease, nil }
	attempts := 0
	h.proxy.chaos.observeAttempt = func(string, string, string, chaos.Rule) { attempts++ }
	client, server := net.Pipe()
	defer client.Close()
	conn, cleanup, err := h.proxy.chaosConn(context.Background(), server, "api", ServiceTCPTarget{AppID: "db", ScenarioTestRunID: "run"}, 5432)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if attempts != 1 {
		t.Fatal("idle connection not counted")
	}
	for i := 0; i < 3; i++ {
		if err := conn.(*chaos.TCPConn).Update(lease); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != 1 {
		t.Fatal("pool refresh counted as reconnect")
	}
	// A different port must not count toward this rule.
	otherClient, otherServer := net.Pipe()
	defer otherClient.Close()
	_, otherCleanup, err := h.proxy.chaosConn(context.Background(), otherServer, "api", ServiceTCPTarget{AppID: "db", ScenarioTestRunID: "run"}, 5433)
	if err != nil {
		t.Fatal(err)
	}
	defer otherCleanup()
	if attempts != 1 {
		t.Fatal("wrong port counted")
	}
}
