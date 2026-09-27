package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestJWKSHTTPClientRefusesNodeLocalTargets pins the §11 egress guard on
// edge-rule JWKS fetches. jwks_url is customer-supplied and only
// string-prefix checked by the API validator, so a hostname resolving to a
// node-local or metadata address, or a redirect to one, reached it through
// the plain client this used before.
func TestJWKSHTTPClientRefusesNodeLocalTargets(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "")
	hit := false
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	defer internal.Close()

	resp, err := newJWKSHTTPClient().Get(internal.URL + "/.well-known/jwks.json")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("JWKS fetch reached loopback %s (status %d); want an egress refusal", internal.URL, resp.StatusCode)
	}
	if hit || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("hit=%v err=%v; want the egress guard's refusal before any request", hit, err)
	}
}
