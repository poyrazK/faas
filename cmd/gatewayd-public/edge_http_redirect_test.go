package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Prod hunt r74: http://<app>.gregale.app hung until the client gave up.
// The redirect listener answers 308 to the same host and path over HTTPS.
func TestHTTPSRedirect(t *testing.T) {
	h := httpsRedirect("gregale.dev")
	for _, tc := range []struct {
		method, target, host string
		code                 int
		location             string
	}{
		{http.MethodGet, "/a/b?x=1", "shop.gregale.app", http.StatusPermanentRedirect, "https://shop.gregale.app/a/b?x=1"},
		{http.MethodPost, "/hook", "Shop.Gregale.App.:80", http.StatusPermanentRedirect, "https://shop.gregale.app/hook"},
		{http.MethodGet, "/", "www.customer.example", http.StatusPermanentRedirect, "https://www.customer.example/"},
		{http.MethodGet, "/", "51.222.39.227", http.StatusNotFound, ""},
		{http.MethodGet, "/", "[2001:db8::1]", http.StatusNotFound, ""},
		{http.MethodGet, "/", "[2001:db8::1]:80", http.StatusNotFound, ""},
		{http.MethodGet, "/", "api.gregale.dev", http.StatusNotFound, ""},
		{http.MethodGet, "/", "", http.StatusBadRequest, ""},
	} {
		req := httptest.NewRequest(tc.method, tc.target, nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code || rec.Header().Get("Location") != tc.location {
			t.Errorf("%s %s Host %q = %d %q, want %d %q", tc.method, tc.target, tc.host, rec.Code, rec.Header().Get("Location"), tc.code, tc.location)
		}
	}
}

func TestEdgeHTTPRedirectAddrNeedsTheDirectTLSListener(t *testing.T) {
	env := func(string) string { return ":80" }
	if _, err := edgeHTTPRedirectAddr(edgeTLSConfig{}, env); err == nil || !strings.Contains(err.Error(), edgeHTTPRedirectAddrEnv) {
		t.Fatalf("no TLS listener: err = %v", err)
	}
	if _, err := edgeHTTPRedirectAddr(edgeTLSConfig{addr: ":443", front: edgeFrontCloudflare}, env); err == nil {
		t.Fatal("behind Cloudflare: want an error")
	}
	addr, err := edgeHTTPRedirectAddr(edgeTLSConfig{addr: ":443", front: edgeFrontNone}, env)
	if err != nil || addr != ":80" {
		t.Fatalf("direct: %q, %v", addr, err)
	}
	if addr, err := edgeHTTPRedirectAddr(edgeTLSConfig{}, func(string) string { return "" }); err != nil || addr != "" {
		t.Fatalf("unset: %q, %v", addr, err)
	}
}
