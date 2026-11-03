package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The edge never forwards gatewayd-internal's private paths: realtimed's
// management API answered any app host's /v1/internal/realtime/internal/...
func TestRefuseComputePrivatePaths(t *testing.T) {
	for _, tc := range []struct {
		target  string
		refused bool
	}{
		{"https://victim.gregale.dev/v1/internal/realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/internal/realtime/internal/endpoints/ep_1/channels/c:publish", true},
		{"https://shop.example.com/v1/internal/realtime", true},
		{"https://api.gregale.dev/v1/internal/realtime/internal/stats", true},
		{"https://victim.gregale.dev//v1/internal/realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/internal/./realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/apps/../internal/realtime/internal/connections", true},
		{"https://victim.gregale.dev/v1/internal/realtime%2Finternal%2Fconnections", true},
		{"https://victim.gregale.dev/v1/internal/metrics", true},
		{"https://victim.gregale.dev/v1/internal/realtimes", false},
		{"https://victim.gregale.dev/v1/internal/services/payments/charge", false},
		{"https://victim.gregale.dev/v1/orders", false},
		{"https://victim.gregale.dev/", false},
	} {
		forwarded := false
		h := refuseComputePrivatePaths(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwarded = true
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if tc.refused && (forwarded || rec.Code != http.StatusNotFound) {
			t.Errorf("%s: forwarded=%v status=%d, want 404 without forwarding", tc.target, forwarded, rec.Code)
		}
		if !tc.refused && (!forwarded || rec.Code != http.StatusOK) {
			t.Errorf("%s: forwarded=%v status=%d, want it forwarded", tc.target, forwarded, rec.Code)
		}
	}
}

// gatewayd-internal's synth routes wake and invoke any app by ID. Only the
// local scheduler's direct loopback request reaches them; anything a proxy
// relayed, any TLS request and any app Host is refused (ADR-20261103090000).
func TestRefuseSchedulerPathsFromPublicTraffic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  string
		remote  string
		headers map[string]string
		tls     bool
		refused bool
	}{
		{name: "local scheduler", target: "http://127.0.0.1:8080/v1/invocations:dispatch", remote: "127.0.0.1:40000"},
		{name: "local scheduler batch", target: "http://127.0.0.1:8080/v1/invocations:dispatch_batch", remote: "[::1]:40000"},
		{name: "localhost host", target: "http://localhost:8080/v1/synthesize", remote: "127.0.0.1:40000"},
		{name: "app host", target: "http://victim.gregale.dev/v1/invocations:dispatch", remote: "127.0.0.1:40000", refused: true},
		{name: "via Caddy", target: "http://127.0.0.1:8080/v1/synthesize", remote: "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9", "X-Forwarded-Proto": "https"}, refused: true},
		{name: "via Caddy, forwarded host only", target: "http://127.0.0.1/v1/synthesize", remote: "127.0.0.1:40000",
			headers: map[string]string{"X-Forwarded-Host": "victim.gregale.dev"}, refused: true},
		{name: "remote peer", target: "http://10.0.0.5:8080/v1/invocations:dispatch", remote: "10.0.0.9:40000", refused: true},
		{name: "compute edge TLS", target: "https://127.0.0.1/v1/invocations:dispatch", remote: "127.0.0.1:40000", tls: true, refused: true},
		{name: "unclean path", target: "http://victim.gregale.dev//v1/./synthesize", remote: "127.0.0.1:40000", refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forwarded := false
			h := refuseComputePrivatePaths(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				forwarded = true
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodPost, tc.target, nil)
			req.RemoteAddr = tc.remote
			if !tc.tls {
				req.TLS = nil
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if tc.refused != !forwarded || tc.refused && rec.Code != http.StatusNotFound {
				t.Fatalf("forwarded=%v status=%d, want refused=%v", forwarded, rec.Code, tc.refused)
			}
		})
	}
}

// Both the control-plane proxy and the compute edge sit under rootHandler,
// so the guard must wrap rootHandler itself.
func TestRootHandlerRefusesComputePrivatePaths(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	guard := strings.Index(body, "rootHandler = refuseComputePrivatePaths(rootHandler)")
	mount := strings.Index(body, `traceMux.Handle("/", rootHandler)`)
	edge := strings.Index(body, "rootHandler, err = computeEdgeRoot(")
	if guard < 0 || mount < 0 || edge < 0 || !(edge < guard && guard < mount) {
		t.Fatal("main.go must wrap rootHandler with refuseComputePrivatePaths after the compute edge is chosen and before it is mounted")
	}
}
