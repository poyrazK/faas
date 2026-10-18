// spec: §4.1
package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResponseCache_CredentialPresence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		values []string
	}{
		{"empty authorization", "Authorization", []string{""}},
		{"empty first authorization", "Authorization", []string{"", "Bearer user-A"}},
		{"valid cookie", "Cookie", []string{"session=alice"}},
		{"escaped cookie value", "Cookie", []string{"session=\"user\\A\""}},
		{"cookie parser count limit", "Cookie", []string{strings.Repeat("x=y; ", 3000) + "session=user-A"}},
	} {
		for _, primePublic := range []bool{false, true} {
			name := tc.name + "/credentialed first"
			if primePublic {
				name = tc.name + "/public first"
			}
			t.Run(name, func(t *testing.T) {
				var calls atomic.Int32
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					body := "public response"
					// An origin can inspect every Authorization field or use
					// a cookie parser that accepts values Go's parser skips.
					if len(r.Header.Values(tc.header)) > 0 {
						body = "credentialed response"
					}
					_, _ = w.Write([]byte(body))
				}))
				t.Cleanup(origin.Close)
				h, b, _ := newTestHandler(t)
				b.upstream = origin.Listener.Addr().String()
				b.setLegacyHot()
				cache := NewResponseCache()
				h.WithResponseCache(cache)
				seedCacheRule(t, h, b.host, EdgeRuleCacheResolved{ID: "auth-rule", PathGlob: "/catalog", MaxAgeSeconds: 60})
				gateway := httptest.NewServer(h)
				t.Cleanup(gateway.Close)
				for _, authenticated := range []bool{!primePublic, primePublic, !primePublic, primePublic} {
					r, err := http.NewRequest(http.MethodGet, gateway.URL+"/catalog", nil)
					if err != nil {
						t.Fatal(err)
					}
					r.Host = b.host
					want := "public response"
					if authenticated {
						r.Header[tc.header] = tc.values
						want = "credentialed response"
					}
					response, err := gateway.Client().Do(r)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != http.StatusOK || string(body) != want {
						t.Errorf("authenticated=%v: received %d %q, want %q", authenticated, response.StatusCode, body, want)
					}
				}
				if calls.Load() != 3 || cache.Len() != 1 {
					t.Errorf("origin calls=%d entries=%d, want 3 calls and only the public response cached", calls.Load(), cache.Len())
				}
			})
		}
	}
}

func TestResponseCache_CredentialsBypassStaleAndObservation(t *testing.T) {
	for i, header := range []http.Header{
		{"Authorization": {""}},
		{"Authorization": {"", "Bearer user-A"}},
		{"Authorization": {"Bearer user-A", ""}},
		{"Cookie": {"session=\"user\\A\""}},
		{"Cookie": {strings.Repeat("x=y; ", 3000) + "session=user-A"}},
	} {
		h, b, _ := newTestHandler(t)
		cache := NewResponseCache()
		h.WithResponseCache(cache)
		rule := EdgeRuleCacheResolved{ID: "auth-rule", MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}
		r := httptest.NewRequest(http.MethodGet, "http://"+b.host+"/catalog", nil)
		r.Header = header
		key := responseCacheKeyForRequest(r, b.app.ID, "", &rule)
		r = r.WithContext(withCacheKeyContext(r.Context(), &rule, key))
		if !cache.Put(key, http.StatusOK, nil, []byte("stale public response"), time.Now().Add(-time.Second), time.Now().Add(time.Minute), rule.toStateEdgeRuleCacheAction()) {
			t.Fatal("could not seed stale response")
		}
		for _, serve := range []func(http.ResponseWriter, *http.Request, App, *statusRecorder) (bool, string){
			h.tryServeStaleOnWakeError, h.serveStaleWhileWaking,
		} {
			w := httptest.NewRecorder()
			rec := newTestStatusRecorder(w)
			if served, _ := serve(rec, r, b.app, rec); served || w.Body.Len() != 0 {
				t.Errorf("credential case %d received stale response", i)
			}
		}
		if contentObservable(r) {
			t.Errorf("credential case %d was eligible for public cacheability analysis", i)
		}
	}
}
