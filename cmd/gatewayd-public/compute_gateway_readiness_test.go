package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// computeNodeServer is a compute node's gatewayd-internal data listener.
// paused models the restart window behind its durable socket: connections
// queue in the backlog and nothing answers until resume.
type computeNodeServer struct {
	listener net.Listener
	resumed  chan struct{}
}

func startComputeNode(t *testing.T, paused bool) *computeNodeServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	node := &computeNodeServer{listener: listener, resumed: make(chan struct{})}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() {
		<-node.resumed
		_ = server.Serve(listener)
	}()
	if !paused {
		close(node.resumed)
	}
	t.Cleanup(func() { _ = server.Close(); _ = listener.Close() })
	return node
}

func (n *computeNodeServer) target() *string {
	target := "tcp://" + n.listener.Addr().String()
	return &target
}

func readinessPool(t *testing.T, wait time.Duration, nodes ...*computeNodeServer) *computeGatewayPool {
	t.Helper()
	var inventory []state.ComputeNode
	for i, node := range nodes {
		inventory = append(inventory, state.ComputeNode{Name: string(rune('a' + i)), Active: true, GatewayTargetURL: node.target()})
	}
	pool := newComputeGatewayPool(fakeComputeGatewayStore{nodes: inventory}, slog.New(slog.NewTextHandler(io.Discard, nil))).(*computeGatewayPool)
	pool.readiness = newComputeGatewayReadiness(wait)
	if _, err := pool.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	return pool
}

func dialTargets(t *testing.T, pool *computeGatewayPool, n int) map[string]int {
	t.Helper()
	got := map[string]int{}
	for range n {
		conn, err := pool.DialContext(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		got[conn.RemoteAddr().String()]++
		_ = conn.Close()
	}
	return got
}

// A node that stops answering for longer than the cap stops receiving new
// requests; they fail over to a node that answers (ADR-2026101801).
func TestComputeGatewayPoolFailsOverAfterRestartWait(t *testing.T) {
	restarting := startComputeNode(t, true)
	healthy := startComputeNode(t, false)
	pool := readinessPool(t, 200*time.Millisecond, restarting, healthy)

	started := time.Now()
	pool.readiness.probeAll(context.Background(), pool.endpoints, pool.log)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("probe took %v, want about the 200ms cap", elapsed)
	}
	got := dialTargets(t, pool, 10)
	if got[restarting.listener.Addr().String()] != 0 || got[healthy.listener.Addr().String()] != 10 {
		t.Fatalf("dials = %v, want all 10 on the answering node", got)
	}
}

// A node that answers again within the cap keeps its share: requests waited
// for it instead of failing over.
func TestComputeGatewayPoolWaitsForNodeBackWithinRestartWait(t *testing.T) {
	restarting := startComputeNode(t, true)
	healthy := startComputeNode(t, false)
	pool := readinessPool(t, 2*time.Second, restarting, healthy)

	time.AfterFunc(100*time.Millisecond, func() { close(restarting.resumed) })
	pool.readiness.probeAll(context.Background(), pool.endpoints, pool.log)
	got := dialTargets(t, pool, 10)
	if got[restarting.listener.Addr().String()] != 5 || got[healthy.listener.Addr().String()] != 5 {
		t.Fatalf("dials = %v, want the restarted node to keep its round-robin share", got)
	}
}

// With no node answering, requests still go somewhere and wait rather than
// failing at the edge.
func TestComputeGatewayPoolWaitsWhenNoNodeAnswers(t *testing.T) {
	a := startComputeNode(t, true)
	b := startComputeNode(t, true)
	pool := readinessPool(t, 100*time.Millisecond, a, b)
	pool.readiness.probeAll(context.Background(), pool.endpoints, pool.log)
	if got := dialTargets(t, pool, 4); len(got) == 0 {
		t.Fatal("no dial succeeded")
	}
}

func TestComputeGatewayRestartWaitConfig(t *testing.T) {
	tests := map[string]time.Duration{
		"":     3 * time.Second,
		"5s":   5 * time.Second,
		"-1s":  3 * time.Second,
		"junk": 3 * time.Second,
	}
	for value, want := range tests {
		if got := computeGatewayRestartWait(func(string) string { return value }); got != want {
			t.Errorf("%q: wait = %v, want %v", value, got, want)
		}
	}
}
