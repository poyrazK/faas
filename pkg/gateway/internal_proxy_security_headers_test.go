package gateway

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/httpsec"
)

// ADR-830: the public hop forces the static headers on Gregale-owned hosts,
// keeps the app's own values on customer hosts, and fills defaults only for
// headers the app did not send.
func TestInternalReverseProxy_CustomerSecurityHeaders(t *testing.T) {
	httpsec.SetHSTSEnabled(true)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The app sets two of the five headers and leaves the rest.
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; preload")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	proxy := NewInternalReverseProxy(&stubDialer{server: upstream}, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false)
	handler := httpsec.ForSurface(func(r *http.Request) httpsec.Surface {
		return httpsec.ClassifyHost(r.Host, "gregale.dev")
	}, true, proxy)

	serve := func(host string) http.Header {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil))
		return rec.Header()
	}

	custom := serve("example.com")
	for name, want := range map[string]string{
		"X-Frame-Options":           "SAMEORIGIN",
		"Strict-Transport-Security": "max-age=63072000; preload",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Permissions-Policy":        "",
	} {
		if got := custom.Values(name); (want == "" && len(got) != 0) || (want != "" && (len(got) != 1 || got[0] != want)) {
			t.Errorf("custom domain %s = %q, want %q", name, got, want)
		}
	}

	platform := serve("api.gregale.dev")
	for name, want := range map[string]string{
		"X-Frame-Options":           "DENY",
		"Strict-Transport-Security": httpsec.ValueHSTSMaxAge,
		"Permissions-Policy":        httpsec.ValuePermissionsPolicy,
	} {
		if got := platform.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("platform host %s = %q, want forced %q", name, got, want)
		}
	}
}
