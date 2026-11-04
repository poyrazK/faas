package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// countingComputeGatewayStore counts registry reads.
type countingComputeGatewayStore struct {
	fakeComputeGatewayStore
	reads atomic.Int32
}

func (s *countingComputeGatewayStore) ActiveComputeNodes(ctx context.Context) ([]state.ComputeNode, error) {
	s.reads.Add(1)
	return s.fakeComputeGatewayStore.ActiveComputeNodes(ctx)
}

func threeNodeStore() *countingComputeGatewayStore {
	return &countingComputeGatewayStore{fakeComputeGatewayStore: fakeComputeGatewayStore{nodes: []state.ComputeNode{
		{Name: "fsn-1", Active: true, GatewayTargetURL: stringPtr("tcp://127.0.0.1:1001")},
		{Name: "fsn-2", Active: true, GatewayTargetURL: stringPtr("tcp://127.0.0.1:1002")},
		{Name: "fsn-3", Active: true, GatewayTargetURL: stringPtr("tcp://127.0.0.1:1003")},
	}}}
}

// adr: 1343 — the compute pool names each request's node (round robin, ready
// first) before a connection is chosen, keyed by the node's address.
func TestComputeGatewayPoolPicksANodePerRequest(t *testing.T) {
	pool := newComputeGatewayPool(threeNodeStore(), slog.New(slog.NewTextHandler(io.Discard, nil))).(*computeGatewayPool)
	ctx := context.Background()

	seen := map[string]string{}
	for range 3 {
		upstream, err := pool.PickUpstream(ctx, nil, nil)
		if err != nil {
			t.Fatalf("PickUpstream: %v", err)
		}
		seen[upstream.Node] = upstream.Key
	}
	if len(seen) != 3 || seen["fsn-2"] != "tcp://127.0.0.1:1002" {
		t.Fatalf("three picks reached %v, want each node once, keyed by address", seen)
	}

	// adr: 20261022184531 — a retry skips the node it excluded, and an attempt
	// skips the node whose dial failed.
	upstream, err := pool.PickUpstream(ctx, gateway.NewDialRecord("fsn-1"), []string{"tcp://127.0.0.1:1002"})
	if err != nil || upstream.Node != "fsn-3" {
		t.Fatalf("pick = %q, %v; want fsn-3", upstream.Node, err)
	}
	if upstream.Used != nil {
		t.Fatal("the pool counts nothing itself")
	}
	_, err = pool.PickUpstream(ctx, gateway.NewDialRecord("fsn-1", "fsn-3"), []string{"tcp://127.0.0.1:1002"})
	if !errors.Is(err, gateway.ErrNoComputeCapacity) {
		t.Fatalf("every node excluded or failed: err = %v, want ErrNoComputeCapacity", err)
	}
}

// adr: 1343
func TestComputeGatewayPoolUpstreamDialsItsNode(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		if conn, err := listener.Accept(); err == nil {
			_ = conn.Close()
		}
	}()
	target := "tcp://" + listener.Addr().String()
	store := fakeComputeGatewayStore{nodes: []state.ComputeNode{{Name: "fsn-2", Active: true, GatewayTargetURL: &target}}}
	pool := newComputeGatewayPool(store, slog.New(slog.NewTextHandler(io.Discard, nil))).(*computeGatewayPool)

	upstream, err := pool.PickUpstream(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("PickUpstream: %v", err)
	}
	conn, err := upstream.Dial(context.Background())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()
}

// adr: 1343 — the compute edge picks its own node's gateway without reading
// the registry, and counts a fallback only for an unplanned route to the pool.
func TestLocalFirstDialerPicksUpstreams(t *testing.T) {
	local := dialerFunc(func(context.Context, string) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	})
	target := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	tests := []struct {
		name         string
		healthy      bool
		drained      bool
		excluded     []string
		failed       []string
		wantLocal    bool
		wantFallback bool
	}{
		{name: "healthy", healthy: true, wantLocal: true},
		{name: "local dial failed", healthy: true, failed: []string{localUpstreamKey}, wantFallback: true},
		{name: "unhealthy", wantFallback: true},
		{name: "drained", drained: true},
		{name: "retry excludes this node", healthy: true, excluded: []string{"fsn-3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := threeNodeStore()
			pool := newComputeGatewayPool(store, quietEdgeLog())
			m := newEdgeMetrics()
			d := newLocalFirstDialer(local, pool, target, m, quietEdgeLog()).withDrainWatch("fsn-3", nil)
			d.healthy.Store(tc.healthy)
			d.drained.Store(tc.drained)

			upstream, err := d.PickUpstream(context.Background(), gateway.NewDialRecord(tc.excluded...), tc.failed)
			if err != nil {
				t.Fatalf("PickUpstream: %v", err)
			}
			if gotLocal := upstream.Key == localUpstreamKey; gotLocal != tc.wantLocal {
				t.Fatalf("picked %q (%s), want local=%v", upstream.Key, upstream.Node, tc.wantLocal)
			}
			if tc.wantLocal {
				if upstream.Node != "fsn-3" || store.reads.Load() != 0 {
					t.Fatalf("local pick named %q after %d registry reads; want fsn-3 and none", upstream.Node, store.reads.Load())
				}
				conn, err := upstream.Dial(context.Background())
				if err != nil {
					t.Fatalf("local Dial: %v", err)
				}
				_ = conn.Close()
			}
			if upstream.Used != nil {
				upstream.Used()
			}
			if got := testutil.ToFloat64(m.fallbacks); (got == 1) != tc.wantFallback {
				t.Fatalf("fallbacks = %v, want fallback=%v", got, tc.wantFallback)
			}
		})
	}
}

// adr: 1343
func TestLocalFirstDialerPickNeedsANamingPool(t *testing.T) {
	pool := dialerFunc(func(context.Context, string) (net.Conn, error) { return nil, errors.New("unused") })
	d := newLocalFirstDialer(pool, pool, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, newEdgeMetrics(), quietEdgeLog())
	if _, err := d.PickUpstream(context.Background(), nil, nil); !errors.Is(err, gateway.ErrNoComputeCapacity) {
		t.Fatalf("err = %v, want ErrNoComputeCapacity from a pool that cannot name its nodes", err)
	}
}
