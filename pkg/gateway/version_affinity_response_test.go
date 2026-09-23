package gateway

// adr: 084 — managed rollout affinity keeps its reserved cookie edge-owned.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedVersionCookieStreamingResponseFiltersGuestOverride(t *testing.T) {
	const edgeCookie = "__Host-gregale_version=00112233445566778899aabbccddeeff; Path=/; Secure"
	const guestOverride = "__Host-gregale_version=ffeeddccbbaa99887766554433221100; Path=/; Secure"
	r := withManagedVersionCookieProtection(httptest.NewRequest(http.MethodGet, "http://app.test/", nil))
	dst := make(http.Header)
	dst.Add("Set-Cookie", edgeCookie)
	forwardedResponseHeader(r.Context(), dst, "sEt-cOoKiE", guestOverride)
	forwardedResponseHeader(r.Context(), dst, "Set-Cookie", "session=guest; Path=/")
	forwardedResponseHeader(r.Context(), dst, "Set-Cookie", "__Host-gregale_version_extra=allowed; Path=/; Secure")
	forwardedResponseHeaderWithUpgrade(r.Context(), dst, "Set-Cookie", guestOverride, true)
	if got := dst.Values("Set-Cookie"); len(got) != 3 || got[0] != edgeCookie || got[1] != "session=guest; Path=/" || got[2] != "__Host-gregale_version_extra=allowed; Path=/; Secure" {
		t.Fatalf("forwarded cookies = %q", got)
	}

	unmanaged := httptest.NewRequest(http.MethodGet, "http://app.test/", nil)
	dst = make(http.Header)
	forwardedResponseHeader(unmanaged.Context(), dst, "Set-Cookie", guestOverride)
	if got := dst.Values("Set-Cookie"); len(got) != 1 || got[0] != guestOverride {
		t.Fatalf("unmanaged app's cookie was changed: %q", got)
	}
}

func TestManagedVersionCookieLegacyProxyFiltersGuestOverride(t *testing.T) {
	const guestOverride = "__Host-gregale_version=ffeeddccbbaa99887766554433221100; Path=/; Secure"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", guestOverride)
		w.Header().Add("Set-Cookie", "session=guest; Path=/")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	for _, tc := range []struct {
		name    string
		protect bool
		want    int
	}{
		{name: "managed", protect: true, want: 2},
		{name: "unmanaged", protect: false, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, upstream.URL+"/", nil)
			if tc.protect {
				r = withManagedVersionCookieProtection(r)
			}
			w := httptest.NewRecorder()
			w.Header().Add("Set-Cookie", "__Host-gregale_version=edge-owned; Path=/; Secure")
			defaultProxy(strings.TrimPrefix(upstream.URL, "http://"), 0).ServeHTTP(w, r)
			got := w.Header().Values("Set-Cookie")
			if w.Code != http.StatusNoContent || len(got) != tc.want || got[0] != "__Host-gregale_version=edge-owned; Path=/; Secure" || got[len(got)-1] != "session=guest; Path=/" {
				t.Fatalf("status=%d cookies=%q", w.Code, got)
			}
			if tc.protect && (len(got) != 2 || got[1] == guestOverride) {
				t.Fatalf("guest override survived: %q", got)
			}
		})
	}
}

func TestManagedVersionCookieHandlerProtectsMintedCookie(t *testing.T) {
	const guestOverride = "ffeeddccbbaa99887766554433221100"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Set-Cookie", api.ManagedVersionAffinityCookieName+"="+guestOverride+"; Path=/; Secure")
		w.Header().Add("Set-Cookie", "session=guest; Path=/")
		_, _ = w.Write([]byte("origin"))
	}))
	t.Cleanup(upstream.Close)
	h, backend, _ := newTestHandler(t)
	backend.app.VersionAffinityManagedCookie = true
	backend.upstream = upstream.Listener.Addr().String()
	backend.AddTarget(Target{NodeID: backend.upstream, InstanceID: "stable-1", DeploymentID: "dep-stable"})
	r := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	var managed, session int
	for _, cookie := range w.Result().Cookies() {
		switch cookie.Name {
		case api.ManagedVersionAffinityCookieName:
			managed++
			if cookie.Value == guestOverride || len(cookie.Value) != 32 {
				t.Fatalf("guest replaced edge cookie: %+v", cookie)
			}
		case "session":
			session++
		}
	}
	if managed != 1 || session != 1 {
		t.Fatalf("response cookies = %+v", w.Result().Cookies())
	}

	returning := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	returning.AddCookie(&http.Cookie{Name: api.ManagedVersionAffinityCookieName, Value: "00112233445566778899aabbccddeeff"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, returning)
	if w.Code != http.StatusOK || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Name != "session" {
		t.Fatalf("guest replaced returning visitor's cookie: status=%d cookies=%+v", w.Code, w.Result().Cookies())
	}
}
