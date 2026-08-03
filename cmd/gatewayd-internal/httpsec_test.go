// httpsec_test.go — confirm the gatewayd-side httpsec wiring
// (issue #249 / spec §11) distinguishes apid paths from customer-
// app paths. The full cmd/gatewayd/main.go boot is heavy (TLS
// bundle, schedd dialer, CertMagic); this test pins the load-
// bearing discriminator — isApidPath — by exercising the same
// middleware chain main.go builds:
//
//	publicHandler = httpsec.Static(httpsec.Nonce(
//	    func(r *http.Request) bool { return isApidPath(r.URL.Path) },
//	    inner,
//	))
//
// Inner is a stub that echoes the path so the test can assert which
// branch the gate took by inspecting the Content-Security-Policy
// header.

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/httpsec"
)

// httpsecFixture is the publicHandler chain main.go mounts. Built
// once per test so HSTS flip (TestHttpsec_HSTSDisabled) can mutate
// the package-level var before construction.
func httpsecFixture(t *testing.T) http.Handler {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok:"+r.URL.Path)
	})
	// Mirror cmd/gatewayd/main.go's adapter: isApidPath takes a
	// string, httpsec.Nonce wants *http.Request.
	gate := func(r *http.Request) bool { return isApidPath(r.URL.Path) }
	return httpsec.Static(httpsec.Nonce(gate, inner))
}

// TestHttpsec_ApidPathGetsCSP confirms CSP fires on every apid
// root. The list is the same one the proxy.go path table uses —
// keep them in sync (the httpsec_test.go compile errors if
// `isApidPath` is renamed).
func TestHttpsec_ApidPathGetsCSP(t *testing.T) {
	paths := []string{
		"/v1/apps",
		"/dashboard/",
		"/login",
		"/signup",
		"/login/forgot",
		"/auth/verify",
		"/auth/reset",
		"/logout",
		"/status",
		"/healthz",
		"/cli-auth",
		"/oauth/callback",
	}
	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			httpsecFixture(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if got := rec.Header().Get("Content-Security-Policy"); got == "" {
				t.Errorf("CSP missing on apid path %s", p)
			}
		})
	}
}

// TestHttpsec_CustomerPathSkipsCSP confirms CSP is NOT emitted on
// a customer-app URL. The risk callout in plan/issue #249 — gating
// matters: a nonce-locked policy on a customer response would break
// every customer HTML page.
func TestHttpsec_CustomerPathSkipsCSP(t *testing.T) {
	cases := []string{
		"/",
		"/hello-world",
		"/hello-world/api/users",
		"/wp-login.php",
		"/.env",
		"/api/foo",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			httpsecFixture(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if got := rec.Header().Get("Content-Security-Policy"); got != "" {
				t.Errorf("CSP leaked on customer path %s: %q", p, got)
			}
		})
	}
}

// TestHttpsec_StaticHeadersOnCustomerPath confirms the static
// headers (X-Frame-Options, etc.) still land on customer-app
// responses even when CSP is gated off. The static set is
// universally safe; CSP is the only gated header.
func TestHttpsec_StaticHeadersOnCustomerPath(t *testing.T) {
	rec := httptest.NewRecorder()
	httpsecFixture(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/hello-world", nil))

	headers := map[string]string{
		httpsec.HeaderXFrameOptions:       httpsec.ValueXFrameOptions,
		httpsec.HeaderXContentTypeOptions: httpsec.ValueXContentTypeOptions,
		httpsec.HeaderReferrerPolicy:      httpsec.ValueReferrerPolicy,
		httpsec.HeaderPermissionsPolicy:   httpsec.ValuePermissionsPolicy,
	}
	for k, v := range headers {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if rec.Header().Get(httpsec.HeaderStrictTransportSecurity) != httpsec.ValueHSTSMaxAge {
		t.Errorf("HSTS missing on customer path")
	}
}

// TestHttpsec_HSTSDisabledByEnv confirms the env knob stops HSTS
// emission on both apid and customer paths. The other four static
// headers are untouched.
func TestHttpsec_HSTSDisabledByEnv(t *testing.T) {
	prev := httpsec.HSTSEnabled
	defer httpsec.SetHSTSEnabled(prev)
	httpsec.SetHSTSEnabled(false)

	for _, p := range []string{"/v1/apps", "/hello-world"} {
		t.Run(p, func(t *testing.T) {
			rec := httptest.NewRecorder()
			httpsecFixture(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
			if got := rec.Header().Get(httpsec.HeaderStrictTransportSecurity); got != "" {
				t.Errorf("HSTS leaked when disabled on %s: %q", p, got)
			}
			// Sanity: the other static headers still ride along.
			if !strings.Contains(rec.Header().Get(httpsec.HeaderXFrameOptions), "DENY") {
				t.Errorf("X-Frame-Options missing alongside HSTS disable")
			}
		})
	}
}

// TestHttpsec_HSTSEnabledEnvHelper pins the FAAS_HSTS_ENABLED
// parsing contract shared by cmd/gatewayd/main.go and
// cmd/apid/main.go via pkg/httpsec.HSTSEnabledFromEnv. Both
// daemons parse the same set of truthy / falsy tokens; this
// test lives in gatewayd because the regex-heavy gatewayd
// surface is more likely to drift if a future maintainer adds
// a new token locally.
func TestHttpsec_HSTSEnabledEnvHelper(t *testing.T) {
	getenv := func(v string) func(string) string {
		return func(string) string { return v }
	}
	cases := []struct {
		raw  string
		want bool
	}{
		{"", true}, // default
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"on", true},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"no", false},
		{"off", false},
		{"maybe", true}, // default-to-true on unknown
	}
	for _, c := range cases {
		t.Run(c.raw, func(t *testing.T) {
			if got := httpsec.HSTSEnabledFromEnv(getenv(c.raw)); got != c.want {
				t.Errorf("HSTSEnabledFromEnv(%q) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}
