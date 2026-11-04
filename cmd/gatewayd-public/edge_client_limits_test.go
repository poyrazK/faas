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
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/clientlimit"
)

func TestEdgeClientLimitsFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, value, proxies string
		mode                 publicEdgeMode
		wantOn               bool
		wantProxies          int
		wantErr              string
	}{
		{name: "unset", mode: edgeModeCompute},
		{name: "off", value: "0", mode: edgeModeCompute},
		{name: "on", value: "1", mode: edgeModeCompute, wantOn: true},
		{name: "on a control-plane edge", value: "1", mode: edgeModeControlPlane, wantErr: "for a compute edge"},
		{name: "not a switch value", value: "yes", mode: edgeModeCompute, wantErr: edgeClientLimitsEnv},
		{name: "on with Cloudflare ranges", value: "1", proxies: "173.245.48.0/20, 2400:cb00::/32", mode: edgeModeCompute, wantOn: true, wantProxies: 2},
		{name: "a proxy entry is no prefix", value: "1", proxies: "173.245.48.1/20", mode: edgeModeCompute, wantErr: edgeProxyCIDRsEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := edgeClientLimitsFromEnv(tc.mode, func(k string) string {
				switch k {
				case edgeClientLimitsEnv:
					return tc.value
				case edgeProxyCIDRsEnv:
					return tc.proxies
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
			if l.on != tc.wantOn || len(l.proxies) != tc.wantProxies {
				t.Fatalf("on = %v with %d proxies, want %v with %d", l.on, len(l.proxies), tc.wantOn, tc.wantProxies)
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

func TestEdgeClientLimitsCapForwardsAndExemptGRPC(t *testing.T) {
	refused := newEdgeMetrics().refused
	release := make(chan struct{})
	var entered sync.WaitGroup
	blocking := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered.Done()
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	if got := (edgeClientLimits{}).forwards(blocking); fmt.Sprintf("%p", got) != fmt.Sprintf("%p", blocking) {
		t.Fatal("the forward cap wrapped the forwarder with the limits off")
	}
	if (edgeClientLimits{on: true, refused: refused}).forwards(nil) != nil {
		t.Fatal("a missing forwarder became a handler")
	}

	capped := edgeClientLimits{on: true, refused: refused}.forwards(blocking)
	entered.Add(api.EdgeControlPlaneForwardsInFlightMax)
	var done sync.WaitGroup
	for range api.EdgeControlPlaneForwardsInFlightMax {
		done.Add(1)
		go func() {
			defer done.Done()
			capped.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "https://shop.gregale.dev/upload", nil))
		}()
	}
	entered.Wait()
	rec := httptest.NewRecorder()
	capped.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "https://shop.gregale.dev/upload", nil))
	close(release)
	done.Wait()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a forward past the cap = %d, want 503", rec.Code)
	}
	if got := testutil.ToFloat64(refused.WithLabelValues(clientlimit.ReasonForwardInFlight)); got != 1 {
		t.Fatalf("forward_in_flight refusals = %v, want 1", got)
	}

	for contentType, want := range map[string]bool{
		"application/grpc":       true,
		"application/grpc+proto": true,
		"application/json":       false,
		"":                       false,
	} {
		req := httptest.NewRequest(http.MethodPost, "https://shop.gregale.dev/", nil)
		req.Header.Set("Content-Type", contentType)
		if got := isGRPC(req); got != want {
			t.Errorf("isGRPC(%q) = %v, want %v", contentType, got, want)
		}
	}
}
