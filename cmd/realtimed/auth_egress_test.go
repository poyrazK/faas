package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestJWKSHTTPClientRefusesNodeLocalTargets: auth_jwks_url is
// customer-supplied, so realtimed must fetch it through the §11 egress
// guard, as it already does for callbacks.
func TestJWKSHTTPClientRefusesNodeLocalTargets(t *testing.T) {
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "")
	hit := false
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	defer internal.Close()

	resp, err := newJWKSHTTPClient().Get(internal.URL + "/jwks.json")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("JWKS fetch reached loopback %s (status %d); want an egress refusal", internal.URL, resp.StatusCode)
	}
	if hit || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("hit=%v err=%v; want the egress guard's refusal before any request", hit, err)
	}
}
