package gateway

// adr: 20261104142830 — a synthetic probe is never answered from the edge
// cache: the cookie it carries survives the gateway's managed-cookie
// stripping and makes the request cache-ineligible, so a stale copy served
// because the app failed cannot pass a check.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSyntheticProbeBypassesTheEdgeCache(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://shop.apps.example.com/", nil)
	r.Header.Set("User-Agent", "Gregale-Synthetics/1 (+https://gregale.dev/docs/synthetic-checks)")
	r.AddCookie(&http.Cookie{Name: api.SyntheticProbeCookieName, Value: "1"})
	stripManagedVersionAffinityCookie(r)
	stripManagedReleaseContextCookie(r)
	if !hasCacheCredentials(r) {
		t.Fatal("a synthetic probe is cache-eligible; the edge could answer it from a stale copy")
	}
}
