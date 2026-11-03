// adr: 20261103163954
// edge_client_limits_test.go pins how a compute edge applies its
// per-client-address limits: the request rate counts against the client the
// gateway trusts, so behind Cloudflare that is CF-Connecting-IP and with no
// front it is the connection's peer, whatever the client claims; the
// connection and handshake limits wrap only a listener with no front.
package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/clientlimit"
)

func TestEdgeClientLimitsFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		mode        publicEdgeMode
		wantOn      bool
		wantErr     string
	}{
		{name: "unset", mode: edgeModeCompute},
		{name: "off", value: "0", mode: edgeModeCompute},
		{name: "on", value: "1", mode: edgeModeCompute, wantOn: true},
		{name: "on a control-plane edge", value: "1", mode: edgeModeControlPlane, wantErr: "for a compute edge"},
		{name: "not a switch value", value: "yes", mode: edgeModeCompute, wantErr: edgeClientLimitsEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := edgeClientLimitsFromEnv(tc.mode, func(k string) string {
				if k == edgeClientLimitsEnv {
					return tc.value
				}
				return ""
			})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if l.on != tc.wantOn {
				t.Fatalf("on = %v, want %v", l.on, tc.wantOn)
			}
		})
	}
}

func TestEdgeClientLimitsCountRequestsAgainstTheTrustedClient(t *testing.T) {
	refused := newEdgeMetrics().refused
	limits := edgeClientLimits{on: true, refused: refused}
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	// The production order: cloudflareClientIP outside the limits.
	h := cloudflareClientIP(limits.requests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), trusted), trusted)
	burst := api.EdgeClientRequestsBurstPerAddr
	send := func(peer, cfConnectingIP string) int {
		req := httptest.NewRequest(http.MethodGet, "https://shop.gregale.dev/", nil)
		req.RemoteAddr = peer + ":40000"
		req.Header.Set("CF-Connecting-IP", cfConnectingIP)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// servedUntilRefused sends until the first 429 and returns how many were
	// served first. The bucket refills in real time while the loop runs, so
	// a few more than the burst may pass, never fewer.
	servedUntilRefused := func(peer string, client func(i int) string) int {
		t.Helper()
		for i := range 2 * burst {
			switch code := send(peer, client(i)); code {
			case http.StatusNoContent:
			case http.StatusTooManyRequests:
				return i
			default:
				t.Fatalf("request %d = %d", i, code)
			}
		}
		t.Fatalf("%d requests from %s were all served", 2*burst, peer)
		return 0
	}

	// Behind the tunnel the peer is loopback, so each client is its
	// CF-Connecting-IP.
	if served := servedUntilRefused("127.0.0.1", func(int) string { return "203.0.113.9" }); served < burst {
		t.Fatalf("one client was refused after %d requests, want at least the burst %d", served, burst)
	}
	if code := send("127.0.0.1", "203.0.113.10"); code != http.StatusNoContent {
		t.Fatalf("another client behind the same connector = %d, want it served", code)
	}

	// With no front the peer is the client: a new CF-Connecting-IP on each
	// request buys nothing.
	forged := func(i int) string { return fmt.Sprintf("192.0.2.%d", i%250) }
	if served := servedUntilRefused("198.51.100.7", forged); served < burst {
		t.Fatalf("a direct client was refused after %d requests, want at least the burst %d", served, burst)
	}
	if got := testutil.ToFloat64(refused.WithLabelValues(clientlimit.ReasonRequestRate)); got != 2 {
		t.Fatalf("request_rate refusals = %v, want 2", got)
	}
}

func TestEdgeClientLimitsWrapOnlyAListenerWithNoFront(t *testing.T) {
	refused := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "refused"}, []string{"reason"})
	for _, tc := range []struct {
		name     string
		on       bool
		direct   bool
		wantGate bool
	}{
		{name: "off", direct: true},
		{name: "behind Cloudflare", on: true},
		{name: "no front", on: true, direct: true, wantGate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ln := edgeClientLimits{on: tc.on, refused: refused}.tlsListener(t.Context(), raw, &tls.Config{}, tc.direct, func(net.Addr, error) {})
			defer ln.Close()
			if _, isGate := ln.(*clientlimit.HandshakeGate); isGate != tc.wantGate {
				t.Fatalf("listener %T, want the handshake gate = %v", ln, tc.wantGate)
			}
		})
	}
}
