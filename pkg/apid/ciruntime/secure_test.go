package ciruntime_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
)

func TestCIRuntimeHTTPSBoundary(t *testing.T) {
	cases := []struct {
		name, peer, host string
		tls, enabled     bool
		forwarded        []string
		allowed          bool
	}{
		{name: "direct_tls", peer: "192.0.2.1:123", tls: true, enabled: true, allowed: true},
		{name: "local_tls_terminator", peer: "127.0.0.1:123", enabled: true, forwarded: []string{"https"}, allowed: true},
		{name: "forged_forwarded_header", peer: "192.0.2.1:123", enabled: true, forwarded: []string{"https"}},
		{name: "plain_local_http", peer: "127.0.0.1:123", enabled: true},
		{name: "ambiguous_forwarded_header", peer: "127.0.0.1:123", enabled: true, forwarded: []string{"https", "http"}},
		{name: "wrong_origin_host", peer: "127.0.0.1:123", host: "other.example", tls: true, enabled: true},
		{name: "default_off", peer: "127.0.0.1:123", tls: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://runtime.example/v1/internal/ci/runtime/any/source", nil)
			r.RemoteAddr = tc.peer
			r.TLS = nil
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if tc.host != "" {
				r.Host = tc.host
			}
			for _, value := range tc.forwarded {
				r.Header.Add("X-Forwarded-Proto", value)
			}
			w := httptest.NewRecorder()
			ciruntime.SecureTransport("https://runtime.example", tc.enabled, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })(w, r)
			if (w.Code == http.StatusNoContent) != tc.allowed {
				t.Fatalf("HTTPS authority mismatch: %d", w.Code)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("transport response was cacheable")
			}
		})
	}
}
