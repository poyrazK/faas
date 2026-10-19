package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 1343
func TestComputeGatewayPoolNodeStatus(t *testing.T) {
	target := "tcp://127.0.0.1:8080"
	pool := newComputeGatewayPool(fakeComputeGatewayStore{nodes: []state.ComputeNode{
		{Name: "cn-1", Active: true, GatewayTargetURL: &target},
		{Name: "cn-2", Active: false, GatewayTargetURL: &target},
		{Name: "cn-3", Active: true, GatewayTargetURL: &target},
	}}, quietEdgeLog()).(*computeGatewayPool)
	if active, nodes, known := pool.nodeStatus(context.Background(), "cn-1"); !active || nodes != 2 || !known {
		t.Fatalf("cn-1: active=%v nodes=%d known=%v, want active among 2 known nodes", active, nodes, known)
	}
	if active, nodes, known := pool.nodeStatus(context.Background(), "cn-2"); active || nodes != 2 || !known {
		t.Fatalf("drained cn-2: active=%v nodes=%d known=%v, want inactive among 2 known nodes", active, nodes, known)
	}

	down := newComputeGatewayPool(fakeComputeGatewayStore{err: errors.New("registry down")}, quietEdgeLog()).(*computeGatewayPool)
	if _, _, known := down.nodeStatus(context.Background(), "cn-1"); known {
		t.Fatal("an unreadable registry was reported as known")
	}
}

type fakeNodeInventory struct {
	active      bool
	activeNodes int
	known       bool
}

func (f fakeNodeInventory) nodeStatus(context.Context, string) (bool, int, bool) {
	return f.active, f.activeNodes, f.known
}

// ADR-1343 §6: a drained node's edge sends every request to the other nodes
// without counting it as a fallback; an unreadable registry, or a drain with
// nowhere else to go, keeps the local path.
//
// adr: 1343
func TestLocalFirstDialerWithdrawsWhileTheNodeIsDrained(t *testing.T) {
	target := &url.URL{Scheme: "http", Host: "gatewayd-internal"}
	var poolDials int
	pool := dialerFunc(func(context.Context, string) (net.Conn, error) {
		poolDials++
		c, _ := net.Pipe()
		return c, nil
	})
	for _, tc := range []struct {
		name        string
		nodeName    string
		inventory   fakeNodeInventory
		wantDrained bool
	}{
		{name: "active", nodeName: "cn-1", inventory: fakeNodeInventory{active: true, activeNodes: 2, known: true}},
		{name: "drained", nodeName: "cn-1", inventory: fakeNodeInventory{activeNodes: 1, known: true}, wantDrained: true},
		{name: "drained with no other node", nodeName: "cn-1", inventory: fakeNodeInventory{known: true}},
		{name: "registry unreadable", nodeName: "cn-1", inventory: fakeNodeInventory{}},
		{name: "no node name", inventory: fakeNodeInventory{activeNodes: 1, known: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newEdgeMetrics()
			d := newLocalFirstDialer(healthServer(t, http.StatusOK), pool, target, m, quietEdgeLog()).
				withDrainWatch(tc.nodeName, tc.inventory)
			d.checkLocal(context.Background())
			if got := d.drained.Load(); got != tc.wantDrained {
				t.Fatalf("drained = %v, want %v", got, tc.wantDrained)
			}
			if got := testutil.ToFloat64(m.nodeDrained) == 1; got != tc.wantDrained {
				t.Fatalf("node_drained gauge = %v, want %v", testutil.ToFloat64(m.nodeDrained), tc.wantDrained)
			}
			if got := testutil.ToFloat64(m.localReady) == 1; got == tc.wantDrained {
				t.Fatalf("local_gateway_ready = %v with drained=%v", testutil.ToFloat64(m.localReady), tc.wantDrained)
			}
			poolDials = 0
			c, err := d.DialContext(context.Background(), target.String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			_ = c.Close()
			wantPool := 0
			if tc.wantDrained {
				wantPool = 1
			}
			if poolDials != wantPool {
				t.Fatalf("pool dials = %d, want %d", poolDials, wantPool)
			}
			if got := testutil.ToFloat64(m.fallbacks); got != 0 {
				t.Fatalf("fallbacks = %v, want 0: a drain is a planned route", got)
			}
		})
	}
}
