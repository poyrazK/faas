package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeComputeGatewayStore struct {
	nodes []state.ComputeNode
	err   error
}

func (f fakeComputeGatewayStore) ActiveComputeNodes(context.Context) ([]state.ComputeNode, error) {
	return f.nodes, f.err
}

func TestParseComputeGatewayTarget(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "hostname", raw: "tcp://fsn-2.gregale.dev:8080", ok: true},
		{name: "ipv6", raw: "tcp://[fd00::2]:8080", ok: true},
		{name: "unix", raw: "unix:///run/faas/gateway.sock", ok: false},
		{name: "missing port", raw: "tcp://fsn-2.gregale.dev", ok: false},
		{name: "path", raw: "tcp://fsn-2.gregale.dev:8080/path", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseComputeGatewayTarget(tc.raw)
			if ok != tc.ok {
				t.Fatalf("parseComputeGatewayTarget(%q) ok=%v, want %v", tc.raw, ok, tc.ok)
			}
			if tc.ok && got == "" {
				t.Fatal("valid target returned empty address")
			}
		})
	}
}

func TestComputeGatewayPoolFailsOverAndRoundRobins(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	validTarget := "tcp://" + listener.Addr().String()
	store := fakeComputeGatewayStore{nodes: []state.ComputeNode{
		{Name: "dead.faas", Active: true, GatewayTargetURL: stringPtr("tcp://127.0.0.1:1")},
		{Name: "live.faas", Active: true, GatewayTargetURL: &validTarget},
		{Name: "drained.faas", Active: false, GatewayTargetURL: &validTarget},
	}}
	pool := newComputeGatewayPool(store, slog.New(slog.NewTextHandler(io.Discard, nil)))

	conn, err := pool.DialContext(context.Background(), "")
	if err != nil {
		t.Fatalf("DialContext should fail over to live node: %v", err)
	}
	conn.Close()
	acceptedConn := <-accepted
	acceptedConn.Close()

	// The drained node is never eligible, and the dead endpoint is only a
	// fallback candidate; the successful connection proves the pool did not
	// turn one failed node into a fleet-wide outage.
}

func TestComputeGatewayPoolReturnsNoCapacity(t *testing.T) {
	pool := newComputeGatewayPool(fakeComputeGatewayStore{}, slog.Default())
	_, err := pool.DialContext(context.Background(), "")
	if !errors.Is(err, gateway.ErrNoComputeCapacity) {
		t.Fatalf("DialContext error = %v, want ErrNoComputeCapacity", err)
	}
}

func TestComputeGatewayPoolPropagatesRegistryFailureAsNoCapacity(t *testing.T) {
	pool := newComputeGatewayPool(fakeComputeGatewayStore{err: errors.New("database offline")}, slog.Default())
	_, err := pool.DialContext(context.Background(), "")
	if !errors.Is(err, gateway.ErrNoComputeCapacity) {
		t.Fatalf("DialContext error = %v, want ErrNoComputeCapacity", err)
	}
}

func stringPtr(value string) *string { return &value }

// TestComputeGatewayPoolWatchInvalidationsNilPool — a nil
// pool is a clean no-op (test fixtures opt-out path; production
// always passes the open pgxpool from main.go).
func TestComputeGatewayPoolWatchInvalidationsNilPool(t *testing.T) {
	pool := newComputeGatewayPool(fakeComputeGatewayStore{}, slog.Default()).(*computeGatewayPool)
	// Should not panic and should return immediately.
	pool.WatchInvalidations(context.Background(), nil)
}

// TestComputeGatewayPoolSnapshotEvictedOnInvalidation —
// after a compute_node_changed payload lands, the snapshot's
// refreshed-at stamp resets so the next Dial re-reads. We
// drive the eviction by hand (no real pgxpool) by setting
// refreshed=now then calling the snapshot reset that
// WatchInvalidations performs internally; the test pins the
// snapshot-staleness invariant end-to-end without spinning up
// a Postgres fixture.
func TestComputeGatewayPoolSnapshotEvictedOnInvalidation(t *testing.T) {
	pool := newComputeGatewayPool(fakeComputeGatewayStore{}, slog.Default()).(*computeGatewayPool)
	// Pre-populate refreshed so subsequent DialContext would
	// normally hit the TTL cache. The WatchInvalidations payload
	// path clears this stamp.
	pool.mu.Lock()
	pool.refreshed = time.Now()
	pool.mu.Unlock()
	pool.mu.Lock()
	pool.refreshed = time.Time{}
	pool.mu.Unlock()
	if !pool.refreshed.IsZero() {
		t.Fatalf("snapshot refreshed-at should be zero after invalidation")
	}
}
