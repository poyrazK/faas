// adr: 211
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// hostVaryHash is the VaryHash a request for host produces with no vary_on.
func hostVaryHash(host string) [32]byte {
	return computeVaryHash(&http.Request{Host: host}, nil)
}

// One app serves many hostnames (custom domains, platform-tenant surfaces)
// and a "*" or wildcard cache rule covers them all under one rule ID. The
// key held only app, rule, path and query, so a page cached for one
// tenant's hostname was served on another's.
func TestResponseCacheKeyVariesByHost(t *testing.T) {
	a := httptest.NewRequest(http.MethodGet, "http://tenant-a.example/profile", nil)
	b := httptest.NewRequest(http.MethodGet, "http://Tenant-B.example:443/profile", nil)
	if computeVaryHash(a, nil) == computeVaryHash(b, nil) {
		t.Fatal("cache variants for different hosts collide")
	}
	if computeVaryHash(a, []string{"Accept-Language"}) == computeVaryHash(b, []string{"Accept-Language"}) {
		t.Fatal("cache variants for different hosts collide with vary_on set")
	}
	if computeVaryHash(b, nil) != hostVaryHash("tenant-b.example") {
		t.Fatal("host variant is not normalized (case, port)")
	}
}
