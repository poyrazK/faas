// spec: §12.6
package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestParkedHeadDoesNotReusePrivateResponseHeaders(t *testing.T) {
	for _, tc := range []struct {
		name     string
		request  http.Header
		response http.Header
	}{
		{name: "authorization", request: http.Header{"Authorization": {"Bearer caller"}}},
		{name: "empty authorization", request: http.Header{"Authorization": {""}}},
		{name: "cookie", request: http.Header{"Cookie": {"session=caller"}}},
		{name: "unparsed cookie", request: http.Header{"Cookie": {`session="user\A"`}}},
		{name: "private", response: http.Header{"Cache-Control": {"public", "private"}}},
		{name: "no-store", response: http.Header{"Cache-Control": {"max-age=60", "no-store"}}},
		{name: "new session", response: http.Header{"Set-Cookie": {"session=caller"}}},
	} {
		for _, primePublic := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/public=%v", tc.name, primePublic), func(t *testing.T) {
				h, b, _ := newTestHandler(t)
				b.setLegacyHot()
				h.proxyByNode = func(Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/public" {
							w.Header().Set("ETag", `"public"`)
						} else {
							for key, values := range tc.response {
								w.Header()[key] = values
							}
							w.Header().Set("X-Account-Name", "private-caller")
							w.Header().Set("ETag", `"private"`)
						}
						_, _ = w.Write([]byte("origin response"))
					})
				}
				if primePublic {
					h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://"+b.host+"/public", nil))
				}
				request := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/private", nil)
				for key, values := range tc.request {
					request.Header[key] = values
				}
				live := httptest.NewRecorder()
				h.ServeHTTP(live, request)
				if live.Code != http.StatusOK || live.Header().Get("X-Account-Name") != "private-caller" {
					t.Fatalf("live response changed: %d %v", live.Code, live.Header())
				}
				b.mu.Lock()
				b.targets, b.running = nil, false
				b.mu.Unlock()
				parked := httptest.NewRecorder()
				h.ServeHTTP(parked, httptest.NewRequest(http.MethodHead, "http://"+b.host+"/", nil))
				wantStatus, wantETag := http.StatusNoContent, ""
				if primePublic {
					wantStatus, wantETag = http.StatusOK, `"public"`
				}
				if parked.Code != wantStatus || parked.Header().Get("ETag") != wantETag || parked.Header().Get("X-Account-Name") != "" {
					t.Errorf("anonymous HEAD reused private metadata: %d %v; want %d ETag %q", parked.Code, parked.Header(), wantStatus, wantETag)
				}
				if calls := atomic.LoadInt32(b.Admits()); calls != 0 {
					t.Errorf("parked HEAD woke the application: admits=%d", calls)
				}
			})
		}
	}
}

func TestHeadCacheOriginPrivacySurvivesResponseHeaderRules(t *testing.T) {
	for _, header := range []http.Header{
		{"Cache-Control": {"private"}},
		{"Set-Cookie": {"session=caller"}},
	} {
		h, b, _ := newTestHandler(t)
		live := httptest.NewRecorder()
		rec := &statusRecorder{ResponseWriter: live, request: httptest.NewRequest(http.MethodGet, "http://"+b.host+"/private", nil)}
		for key, values := range header {
			rec.Header()[key] = values
		}
		rec.Header().Set("ETag", `"private"`)
		rec.installHeaderOps([]EdgeRuleHeaderOp{
			{Action: "remove", Name: "Set-Cookie"},
			{Action: "set", Name: "Cache-Control", Value: "public"},
		})
		rec.WriteHeader(http.StatusOK)
		if live.Header().Get("Cache-Control") != "public" || live.Header().Get("Set-Cookie") != "" {
			t.Fatal("response header rules were not applied")
		}
		h.cacheHeadResponse(b.app.ID, rec)
		if cached, ok := h.headHeaders.get(b.app.ID); ok {
			t.Errorf("header rules made private origin metadata reusable: %v", cached)
		}
	}
}
