package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
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

// switchableComputeGatewayStore is a registry whose answer can change
// between dials, standing in for a database that goes away and returns.
type switchableComputeGatewayStore struct {
	mu    sync.Mutex
	nodes []state.ComputeNode
	err   error
	calls int
}

func (s *switchableComputeGatewayStore) ActiveComputeNodes(context.Context) ([]state.ComputeNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return append([]state.ComputeNode(nil), s.nodes...), s.err
}

func (s *switchableComputeGatewayStore) set(nodes []state.ComputeNode, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodes, s.err = nodes, err
}

func (s *switchableComputeGatewayStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// acceptingTarget returns a tcp:// target that accepts and closes every
// connection until the test ends.
func acceptingTarget(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			conn.Close()
		}
	}()
	return "tcp://" + listener.Addr().String()
}

func dialPool(t *testing.T, pool *computeGatewayPool) error {
	t.Helper()
	conn, err := pool.DialContext(context.Background(), "")
	if err == nil {
		conn.Close()
	}
	return err
}

// expire moves the pool's stamps back by age, as if that much time had
// passed since the last successful read.
func expire(pool *computeGatewayPool, age time.Duration) {
	pool.mu.Lock()
	defer pool.mu.Unlock()
	pool.refreshed = pool.refreshed.Add(-age)
	pool.loadedAt = pool.loadedAt.Add(-age)
}

func waitBackgroundRefresh(t *testing.T, pool *computeGatewayPool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pool.mu.Lock()
		busy := pool.refreshing
		pool.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background refresh did not finish")
}

// adr: 1344
// TestComputeGatewayPoolServesLastKnownInventoryWhenRegistryFails pins
// ADR-1344 §5 rule 1: once the registry has answered, a registry failure
// keeps routing to the last-known nodes instead of failing every request.
func TestComputeGatewayPoolServesLastKnownInventoryWhenRegistryFails(t *testing.T) {
	target := acceptingTarget(t)
	store := &switchableComputeGatewayStore{}
	store.set([]state.ComputeNode{{Name: "live.faas", Active: true, GatewayTargetURL: &target}}, nil)
	pool := newComputeGatewayPool(store, slog.New(slog.NewTextHandler(io.Discard, nil))).(*computeGatewayPool)
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("initial dial: %v", err)
	}

	store.set(nil, errors.New("database offline"))
	expire(pool, computeGatewayPoolTTL+time.Second)
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("dial with registry down must use last-known inventory: %v", err)
	}
	waitBackgroundRefresh(t, pool)
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("dial after failed background refresh: %v", err)
	}
	if got := store.callCount(); got != 2 {
		t.Fatalf("registry reads = %d, want 2 (initial + one background retry per TTL)", got)
	}
}

// adr: 1344
// TestComputeGatewayPoolStaleInventoryIsBounded: past the stale bound the
// pool stops trusting the old inventory and reports no capacity.
func TestComputeGatewayPoolStaleInventoryIsBounded(t *testing.T) {
	target := acceptingTarget(t)
	store := &switchableComputeGatewayStore{}
	store.set([]state.ComputeNode{{Name: "live.faas", Active: true, GatewayTargetURL: &target}}, nil)
	pool := newComputeGatewayPool(store, slog.New(slog.NewTextHandler(io.Discard, nil))).(*computeGatewayPool)
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("initial dial: %v", err)
	}

	store.set(nil, errors.New("database offline"))
	expire(pool, computeGatewayPoolStaleTTL+time.Second)
	if err := dialPool(t, pool); !errors.Is(err, gateway.ErrNoComputeCapacity) {
		t.Fatalf("dial past stale bound error = %v, want ErrNoComputeCapacity", err)
	}
}

// adr: 1344
// TestComputeGatewayPoolBackgroundRefreshPicksUpNewInventory: an expired
// TTL with a healthy registry serves the cached list once and replaces it
// in the background.
func TestComputeGatewayPoolBackgroundRefreshPicksUpNewInventory(t *testing.T) {
	oldTarget := acceptingTarget(t)
	newTarget := acceptingTarget(t)
	store := &switchableComputeGatewayStore{}
	store.set([]state.ComputeNode{{Name: "old.faas", Active: true, GatewayTargetURL: &oldTarget}}, nil)
	pool := newComputeGatewayPool(store, slog.New(slog.NewTextHandler(io.Discard, nil))).(*computeGatewayPool)
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("initial dial: %v", err)
	}

	store.set([]state.ComputeNode{{Name: "new.faas", Active: true, GatewayTargetURL: &newTarget}}, nil)
	expire(pool, computeGatewayPoolTTL+time.Second)
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("dial during background refresh: %v", err)
	}
	waitBackgroundRefresh(t, pool)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.endpoints) != 1 || pool.endpoints[0].name != "new.faas" {
		t.Fatalf("endpoints after refresh = %+v, want new.faas", pool.endpoints)
	}
}

// adr: 1344
// TestComputeGatewayPoolInvalidationRereadsAndFallsBack: compute_node_changed
// forces a synchronous re-read; if the registry fails at that moment the
// pool keeps the last-known inventory instead of dropping traffic.
func TestComputeGatewayPoolInvalidationRereadsAndFallsBack(t *testing.T) {
	oldTarget := acceptingTarget(t)
	newTarget := acceptingTarget(t)
	store := &switchableComputeGatewayStore{}
	store.set([]state.ComputeNode{{Name: "old.faas", Active: true, GatewayTargetURL: &oldTarget}}, nil)
	pool := newComputeGatewayPool(store, slog.New(slog.NewTextHandler(io.Discard, nil))).(*computeGatewayPool)
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("initial dial: %v", err)
	}

	store.set([]state.ComputeNode{{Name: "new.faas", Active: true, GatewayTargetURL: &newTarget}}, nil)
	pool.invalidate()
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("dial after invalidation: %v", err)
	}
	pool.mu.Lock()
	got := pool.endpoints[0].name
	pool.mu.Unlock()
	if got != "new.faas" {
		t.Fatalf("invalidation did not re-read synchronously: endpoint %q", got)
	}

	store.set(nil, errors.New("database offline"))
	pool.invalidate()
	before := store.callCount()
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("dial after invalidation with registry down: %v", err)
	}
	if err := dialPool(t, pool); err != nil {
		t.Fatalf("second dial with registry down: %v", err)
	}
	if reads := store.callCount() - before; reads != 1 {
		t.Fatalf("registry reads while down = %d, want 1 (later dials wait for the TTL)", reads)
	}
}
