package gateway

// adr: 091 — amendment 2026-11-02: Cloudflare caches responses in front of
// the gateway by URL, so a response to a request an access control decided
// must not be stored there, or the next client gets it past the control.

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAccessControlledResponsesStayOutOfSharedCaches(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The app asks for CDN caching; an access rule must win.
		w.Header().Set(cloudflareCDNCacheControlHeader, "public, max-age=3600")
		_, _ = w.Write([]byte("report"))
	}))
	t.Cleanup(upstream.Close)
	_, allowNet, _ := net.ParseCIDR("198.51.100.0/24")
	for name, tc := range map[string]struct {
		rule        *EdgeRuleIPResolved
		wantPrivate bool
	}{
		"an ip rule covers the request": {
			rule:        &EdgeRuleIPResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Allow: []*net.IPNet{allowNet}},
			wantPrivate: true,
		},
		"no access rule": {},
	} {
		t.Run(name, func(t *testing.T) {
			b := &fakeBackend{
				app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
				host:     "jane-api.apps.dom",
				upstream: upstream.Listener.Addr().String(),
			}
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
			h.WithEdgeRules(stubEdgeRuleMatcher{ip: tc.rule}, nil, nil)
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/reports/q3.pdf", nil)
			req.Header.Set("X-Forwarded-For", "198.51.100.7")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || rec.Body.String() != "report" {
				t.Fatalf("status = %d body %q, want the app's 200", rec.Code, rec.Body)
			}
			cf, cdn := rec.Header().Get(cloudflareCDNCacheControlHeader), rec.Header().Get(cdnCacheControlHeader)
			if tc.wantPrivate && (cf != "private" || cdn != "private") {
				t.Fatalf("CDN cache headers = %q / %q, want private", cf, cdn)
			}
			if !tc.wantPrivate && cf != "public, max-age=3600" {
				t.Fatalf("the app's own CDN cache header was changed to %q without an access rule", cf)
			}
		})
	}
}

func TestUnknownAppIsNotCached(t *testing.T) {
	h, _, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://no-such-app.apps.dom/logo.png", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unknown app: %d Cache-Control=%q, want 404 no-store", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

// prod 2026-11-19: an app response with no cache policy on a static-looking
// path (/me.pdf) was stored by Cloudflare for four hours keyed by URL, so bob
// received alice's cookie-authenticated body. Without a policy the response
// is CDN-private; a declared policy is left alone.
func TestResponsesWithoutCachePolicyStayOutOfSharedCaches(t *testing.T) {
	for name, tc := range map[string]struct {
		set         func(http.Header)
		wantPrivate bool
	}{
		"no cache headers":        {set: func(http.Header) {}, wantPrivate: true},
		"app Cache-Control":       {set: func(h http.Header) { h.Set("Cache-Control", "public, max-age=60") }},
		"app CDN-Cache-Control":   {set: func(h http.Header) { h.Set(cdnCacheControlHeader, "max-age=60") }},
		"app Cloudflare targeted": {set: func(h http.Header) { h.Set(cloudflareCDNCacheControlHeader, "max-age=60") }},
	} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.set(w.Header())
				_, _ = w.Write([]byte("user=" + r.Header.Get("Cookie")))
			}))
			t.Cleanup(upstream.Close)
			b := &fakeBackend{
				app:      App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
				host:     "jane-api.apps.dom",
				upstream: upstream.Listener.Addr().String(),
			}
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/me.pdf", nil)
			req.Header.Set("Cookie", "session=alice")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			private := rec.Header().Get(cloudflareCDNCacheControlHeader) == "private" && rec.Header().Get(cdnCacheControlHeader) == "private"
			if private != tc.wantPrivate {
				t.Fatalf("CDN-private = %v, want %v (headers %v)", private, tc.wantPrivate, rec.Header())
			}
		})
	}
}
