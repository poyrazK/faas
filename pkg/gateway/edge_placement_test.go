// adr: 20261031105055
package gateway

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// gatewayd-internal names the instance's node only for an edge that asked,
// and moves the request out of the headers so no later hop sees it.
func TestInstanceNodeAnswersOnlyAnEdgeThatAsked(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		want   string
	}{
		{name: "asked", header: "1", want: "node-2"},
		{name: "not asked"},
		{name: "unknown value", header: "yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set(EdgePlacementHeader, tc.header)
			}
			r = takeEdgePlacement(r)
			if r.Header.Get(EdgePlacementHeader) != "" {
				t.Fatal("the placement request stayed in the headers")
			}
			h := http.Header{}
			stampInstanceNode(r.Context(), h, "node-2")
			if got := h.Get(InstanceNodeHeader); got != tc.want {
				t.Fatalf("%s = %q, want %q", InstanceNodeHeader, got, tc.want)
			}
		})
	}
}

// A guest cannot name a node: its copy is dropped on both forward paths.
func TestGuestInstanceNodeIsDropped(t *testing.T) {
	dst := http.Header{}
	forwardedResponseHeader(t.Context(), dst, "x-faas-instance-node", "node-9")
	if got := dst.Get(InstanceNodeHeader); got != "" {
		t.Fatalf("guest node forwarded: %q", got)
	}
	resp := &http.Response{Header: http.Header{InstanceNodeHeader: {"node-9"}}}
	stripGuestEvidenceResponseHeaders(resp)
	if got := resp.Header.Get(InstanceNodeHeader); got != "" {
		t.Fatalf("legacy forward kept the guest node %q", got)
	}
}

type fakePlacement struct {
	mu      sync.Mutex
	prefer  PreferredNode
	learned map[string]string
}

func (p *fakePlacement) Prefer(*http.Request) (PreferredNode, bool) {
	return p.prefer, p.prefer.NodeID != ""
}

func (p *fakePlacement) Learn(r *http.Request, nodeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.learned[r.Host] = nodeID
}

// preferenceDialer records the preference the proxy handed the dialer.
type preferenceDialer struct {
	*stubDialer
	mu  sync.Mutex
	got PreferredNode
}

func (d *preferenceDialer) DialContext(ctx context.Context, target string) (net.Conn, error) {
	d.mu.Lock()
	d.got, _ = PreferredNodeFrom(ctx)
	d.mu.Unlock()
	return d.stubDialer.DialContext(ctx, target)
}

func TestInternalReverseProxyLearnsAndRoutesByPlacement(t *testing.T) {
	var sawPlacement string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPlacement = r.Header.Get(EdgePlacementHeader)
		w.Header().Set(InstanceNodeHeader, "node-2")
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	for _, tc := range []struct {
		name          string
		placement     *fakePlacement
		wantPlacement string
		wantLearned   string
		wantPref      string
	}{
		{name: "compute edge", placement: &fakePlacement{prefer: PreferredNode{NodeID: "node-2", Host: "shop.gregale.dev"}, learned: map[string]string{}},
			wantPlacement: "1", wantLearned: "node-2", wantPref: "node-2"},
		{name: "no placement"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialer := &preferenceDialer{stubDialer: &stubDialer{server: upstream}}
			p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
			if tc.placement != nil {
				p.WithEdgePlacement(tc.placement)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = "shop.gregale.dev"
			// A client cannot ask on its own, or forge an answer.
			req.Header.Set(EdgePlacementHeader, "1")
			rr := httptest.NewRecorder()

			p.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d", rr.Code)
			}
			if sawPlacement != tc.wantPlacement {
				t.Fatalf("upstream saw %s %q, want %q", EdgePlacementHeader, sawPlacement, tc.wantPlacement)
			}
			if got := rr.Header().Get(InstanceNodeHeader); got != "" {
				t.Fatalf("client received %s %q", InstanceNodeHeader, got)
			}
			if dialer.got.NodeID != tc.wantPref {
				t.Fatalf("dialer preference %q, want %q", dialer.got.NodeID, tc.wantPref)
			}
			if tc.placement != nil && tc.placement.learned["shop.gregale.dev"] != tc.wantLearned {
				t.Fatalf("learned %v, want %q", tc.placement.learned, tc.wantLearned)
			}
		})
	}
}
