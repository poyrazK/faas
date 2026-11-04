// adr: 20261031105055
// edge_placement_test.go pins placement routing for a compute edge: a host
// goes to the node that last served it, and anything doubtful goes local.
package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestEdgePlacementRemembersEachHost(t *testing.T) {
	now := time.Unix(1000, 0)
	e := newEdgePlacement()
	e.now = func() time.Time { return now }
	req := func(host string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = host
		return r
	}

	if _, ok := e.Prefer(req("shop.gregale.dev")); ok {
		t.Fatal("an unknown host has a node")
	}
	e.Learn(req("Shop.gregale.dev:443"), "node-2")
	if pref, ok := e.Prefer(req("shop.gregale.dev.")); !ok || pref.NodeID != "node-2" || pref.Host != "shop.gregale.dev" {
		t.Fatalf("Prefer = %+v %v, want node-2 for the canonical host", pref, ok)
	}
	now = now.Add(edgePlacementTTL)
	if _, ok := e.Prefer(req("shop.gregale.dev")); ok {
		t.Fatal("an entry outlived its TTL")
	}
	e.Learn(req("shop.gregale.dev"), "node-2")
	e.Forget("shop.gregale.dev")
	if _, ok := e.Prefer(req("shop.gregale.dev")); ok {
		t.Fatal("Forget kept the entry")
	}

	e.max = 2
	e.Learn(req("a.gregale.dev"), "node-1")
	e.Learn(req("b.gregale.dev"), "node-1")
	e.Learn(req("c.gregale.dev"), "node-1")
	if len(e.hosts) != 2 {
		t.Fatalf("map holds %d hosts, want the bound of 2", len(e.hosts))
	}
	if _, ok := e.Prefer(req("c.gregale.dev")); !ok {
		t.Fatal("the newest host was evicted")
	}
}

// placementFixture is a compute edge on fsn-3 with a pool that holds fsn-2,
// whose gatewayd-internal is a real listener.
type placementFixture struct {
	dialer     *localFirstDialer
	placement  *edgePlacement
	metrics    *edgeMetrics
	localDials int
	remote     string
}

func newPlacementFixture(t *testing.T) *placementFixture {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	f := &placementFixture{placement: newEdgePlacement(), metrics: newEdgeMetrics(), remote: ln.Addr().String()}
	now := time.Now()
	pool := &computeGatewayPool{
		log:       quietEdgeLog(),
		refreshed: now,
		loadedAt:  now,
		endpoints: []computeGatewayEndpoint{
			{id: "id-fsn-2", name: "fsn-2", address: f.remote},
			{id: "id-fsn-3", name: "fsn-3", address: "127.0.0.1:1"},
		},
	}
	local := dialerFunc(func(context.Context, string) (net.Conn, error) {
		f.localDials++
		c, _ := net.Pipe()
		return c, nil
	})
	f.dialer = newLocalFirstDialer(local, pool, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, f.metrics, quietEdgeLog()).
		withDrainWatch("fsn-3", nil).
		withPlacement(f.placement, pool)
	f.dialer.healthy.Store(true)
	return f
}

func (f *placementFixture) result(name string) float64 {
	return testutil.ToFloat64(f.metrics.placement.WithLabelValues(name))
}

func TestLocalFirstDialerRoutesAHostToItsNode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		pref       *gateway.PreferredNode
		excluded   []string
		wantDialed string
		wantResult string
	}{
		{name: "no node known", wantDialed: "fsn-3", wantResult: placementMiss},
		{name: "another node", pref: &gateway.PreferredNode{NodeID: "id-fsn-2", Host: "shop.gregale.dev"}, wantDialed: "fsn-2", wantResult: placementRemote},
		{name: "this node", pref: &gateway.PreferredNode{NodeID: "id-fsn-3", Host: "shop.gregale.dev"}, wantDialed: "fsn-3", wantResult: placementSelf},
		{name: "node not in the inventory", pref: &gateway.PreferredNode{NodeID: "id-gone", Host: "shop.gregale.dev"}, wantDialed: "fsn-3", wantResult: placementUnusable},
		{name: "node excluded by a retry", pref: &gateway.PreferredNode{NodeID: "id-fsn-2", Host: "shop.gregale.dev"}, excluded: []string{"fsn-2"}, wantDialed: "fsn-3", wantResult: placementUnusable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPlacementFixture(t)
			rec := gateway.NewDialRecord(tc.excluded...)
			ctx := gateway.WithDialRecord(context.Background(), rec)
			if tc.pref != nil {
				ctx = gateway.WithPreferredNode(ctx, *tc.pref)
			}
			conn, err := f.dialer.DialContext(ctx, "")
			if err != nil {
				t.Fatalf("DialContext: %v", err)
			}
			_ = conn.Close()
			if rec.Dialed() != tc.wantDialed {
				t.Fatalf("dialed %q, want %q", rec.Dialed(), tc.wantDialed)
			}
			if got := f.result(tc.wantResult); got != 1 {
				t.Fatalf("%s = %v, want 1", tc.wantResult, got)
			}
		})
	}
}

// A node that refuses the dial is forgotten for the host, and the request
// goes to this node's own gateway.
func TestLocalFirstDialerForgetsANodeThatRefuses(t *testing.T) {
	f := newPlacementFixture(t)
	f.dialer.placementPool.endpoints[0].address = "127.0.0.1:1"
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "shop.gregale.dev"
	f.placement.Learn(r, "id-fsn-2")
	pref, _ := f.placement.Prefer(r)

	rec := gateway.NewDialRecord()
	conn, err := f.dialer.DialContext(gateway.WithPreferredNode(gateway.WithDialRecord(context.Background(), rec), pref), "")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()
	if rec.Dialed() != "fsn-3" || f.localDials != 1 {
		t.Fatalf("dialed %q with %d local dials, want this node", rec.Dialed(), f.localDials)
	}
	if got := f.result(placementFailed); got != 1 {
		t.Fatalf("failed = %v, want 1", got)
	}
	if _, ok := f.placement.Prefer(r); ok {
		t.Fatal("the refusing node is still remembered")
	}
}

// With keep-alive per node the picker names the remembered node, and counts
// the route only when the transport uses it.
func TestLocalFirstDialerPicksTheHostsNode(t *testing.T) {
	f := newPlacementFixture(t)
	ctx := gateway.WithPreferredNode(context.Background(), gateway.PreferredNode{NodeID: "id-fsn-2", Host: "shop.gregale.dev"})
	up, err := f.dialer.PickUpstream(ctx, gateway.NewDialRecord(), nil)
	if err != nil {
		t.Fatalf("PickUpstream: %v", err)
	}
	if up.Node != "fsn-2" || up.Key != computeGatewayUpstreamKey(f.remote) {
		t.Fatalf("picked %q (%s), want fsn-2", up.Node, up.Key)
	}
	conn, err := up.Dial(ctx)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()
	if up.Used != nil {
		up.Used()
	}
	if got := f.result(placementRemote); got != 1 {
		t.Fatalf("remote = %v, want 1", got)
	}

	// Once that node's address failed this request, the picker goes local.
	up, err = f.dialer.PickUpstream(ctx, gateway.NewDialRecord(), []string{computeGatewayUpstreamKey(f.remote)})
	if err != nil || up.Key != localUpstreamKey {
		t.Fatalf("after a failed dial: %q %v, want the local gateway", up.Key, err)
	}
}

// Without the switch a dialer has no placement and routes as before.
func TestLocalFirstDialerWithoutPlacementIgnoresPreferences(t *testing.T) {
	f := newPlacementFixture(t)
	f.dialer.placement, f.dialer.placementPool = nil, nil
	rec := gateway.NewDialRecord()
	ctx := gateway.WithPreferredNode(gateway.WithDialRecord(context.Background(), rec), gateway.PreferredNode{NodeID: "id-fsn-2", Host: "shop.gregale.dev"})
	conn, err := f.dialer.DialContext(ctx, "")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	_ = conn.Close()
	if rec.Dialed() != "fsn-3" {
		t.Fatalf("dialed %q, want this node", rec.Dialed())
	}
	for _, result := range placementResults {
		if got := f.result(result); got != 0 {
			t.Fatalf("%s = %v with placement off", result, got)
		}
	}
}
