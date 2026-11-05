// adr: 069
package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A custom domain may contain "--": every internationalized domain does
// ("xn--bcher-kva.ch") and so may an ordinary one ("my--shop.example").
// The "--" selector belongs to platform hostnames; a custom domain the
// router knows must route to its app, not be split into an app "xn" and a
// companion "bcher-kva.ch".
func TestCustomDomainWithDoubleHyphenRoutes(t *testing.T) {
	for _, host := range []string{"xn--bcher-kva.ch", "my--shop.example.com"} {
		t.Run(host, func(t *testing.T) {
			b := &fakeBackend{app: App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, host: host}
			b.AddTarget(Target{NodeID: "node-1", InstanceID: "instance-1"})
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil))).WithSelectorSuffixes(".gregale.dev")
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
			if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
				t.Fatalf("status = %d body = %q, want the app's 200", rec.Code, rec.Body.String())
			}
		})
	}
}

// Under the platform suffix the selector still applies: a companion host
// resolves to the app, and an internationalized custom domain does not.
func TestSplitHostSelectorUnderPlatformSuffixes(t *testing.T) {
	suffixes := []string{".gregale.dev"}
	for _, tc := range []struct{ host, appHost, selector string }{
		{"shop--db-proxy.gregale.dev", "shop.gregale.dev", "db-proxy"},
		{"shop--port-admin.gregale.dev", "shop.gregale.dev", "port-admin"},
		{"shop.gregale.dev", "shop.gregale.dev", ""},
		{"xn--bcher-kva.ch", "xn--bcher-kva.ch", ""},
		{"api.xn--mnchen-3ya.de", "api.xn--mnchen-3ya.de", ""},
		{"my--shop.example.com", "my--shop.example.com", ""},
	} {
		appHost, selector := SplitHostSelectorUnder(tc.host, "", suffixes)
		if appHost != tc.appHost || selector != tc.selector {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.host, appHost, selector, tc.appHost, tc.selector)
		}
	}
	if appHost, selector := SplitHostSelectorUnder("a--b.example.com", "", nil); appHost != "a" || selector != "b.example.com" {
		t.Errorf("no suffixes configured: got (%q, %q); the legacy split must be unchanged", appHost, selector)
	}
}
