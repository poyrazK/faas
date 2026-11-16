package gateway

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestInternalReverseProxy_RedirectsForwardedPlainHTTP pins that an app
// request a trusted ingress received over plain http gets a 308 to https
// before it reaches the app, and that untrusted peers, missing headers, ACME
// challenges and the operator opt-out are served as before.
func TestInternalReverseProxy_RedirectsForwardedPlainHTTP(t *testing.T) {
	prefixes, err := ParseTrustedIngressCIDRs("127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, peer, proto, path string
		allow                   bool
		wantRedirect            bool
	}{
		{name: "trusted http", peer: "127.0.0.1:1", proto: "http", path: "/a?b=1", wantRedirect: true},
		{name: "trusted https", peer: "127.0.0.1:1", proto: "https", path: "/"},
		{name: "no forwarded scheme", peer: "127.0.0.1:1", path: "/"},
		{name: "untrusted peer", peer: "198.51.100.7:1", proto: "http", path: "/"},
		{name: "acme challenge", peer: "127.0.0.1:1", proto: "http", path: acmeChallengePrefix + "tok"},
		{name: "operator allows http", peer: "127.0.0.1:1", proto: "http", path: "/", allow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()
			p := NewInternalReverseProxy(&stubDialer{server: upstream}, &url.URL{Scheme: "http", Host: "internal"}, slog.Default(), false).
				WithTrustedIngressCIDRs(prefixes)
			p.AllowForwardedHTTP = tc.allow
			req := httptest.NewRequest(http.MethodPost, "http://shop.gregale.dev"+tc.path, nil)
			req.RemoteAddr = tc.peer
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			if tc.wantRedirect {
				if rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "https://shop.gregale.dev"+tc.path || reached {
					t.Fatalf("got %d Location=%q reached=%v, want 308 to https", rec.Code, rec.Header().Get("Location"), reached)
				}
				return
			}
			if rec.Code == http.StatusPermanentRedirect || !reached {
				t.Fatalf("got %d reached=%v, want the request served", rec.Code, reached)
			}
		})
	}
}
