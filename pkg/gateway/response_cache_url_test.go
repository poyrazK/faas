// spec: §4.1
package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestResponseCache_RequestURLIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, first, second string
	}{
		{"repeated parameter order", "/catalog?id=1&id=2", "/catalog?id=2&id=1"},
		{"encoded parameter aliases", "/catalog?id=1&%69d=2", "/catalog?%69d=2&id=1"},
		{"distinct parameter order", "/catalog?a=1&b=2", "/catalog?b=2&a=1"},
		{"encoded slash", "/catalog/a%2Fb", "/catalog/a/b"},
		{"encoded unreserved character", "/catalog/%61", "/catalog/a"},
		{"empty query delimiter", "/catalog?", "/catalog"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				// Applications can distinguish the request target, including
				// query ordering and escaping, even when URL.Path is identical.
				_, _ = w.Write([]byte(r.RequestURI))
			}))
			t.Cleanup(origin.Close)
			h, b, _ := newTestHandler(t)
			b.upstream = origin.Listener.Addr().String()
			b.setLegacyHot()
			cache := NewResponseCache()
			h.WithResponseCache(cache)
			first := httptest.NewRequest(http.MethodGet, "http://"+b.host+tc.first, nil)
			rule := EdgeRuleCacheResolved{ID: "url-rule", PathGlob: first.URL.Path, MaxAgeSeconds: 60}
			seedCacheRule(t, h, b.host, rule)
			for _, path := range []string{tc.first, tc.second, tc.first, tc.second} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://"+b.host+path, nil))
				if w.Code != http.StatusOK || w.Body.String() != path {
					t.Fatalf("request %q received %d %q", path, w.Code, w.Body.String())
				}
			}
			if calls.Load() != 2 || cache.Len() != 2 {
				t.Fatalf("variants did not cache separately: origin calls=%d entries=%d", calls.Load(), cache.Len())
			}
			// Purges retain decoded path matching and remove every URL variant.
			if err := cache.InvalidateByAppPath(b.app.ID, first.URL.Path); err != nil {
				t.Fatal(err)
			}
			if cache.Len() != 0 {
				t.Fatal("path purge left a URL variant behind")
			}
		})
	}
}

func TestContentObserver_RequestURLIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, first, second string
	}{
		{"host", "http://a.example/catalog", "http://b.example/catalog"},
		{"query order", "http://a.example/catalog?id=1&id=2", "http://a.example/catalog?id=2&id=1"},
		{"escaped path", "http://a.example/catalog/a%2Fb", "http://a.example/catalog/a/b"},
		{"empty query delimiter", "http://a.example/catalog?", "http://a.example/catalog"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observer := newContentObserver(10)
			now := time.Now()
			for i, target := range []string{tc.first, tc.second, tc.first, tc.second} {
				r := httptest.NewRequest(http.MethodGet, target, nil)
				tee := newContentTee(httptest.NewRecorder(), observer, "app-1", r)
				if _, err := tee.Write([]byte("common response")); err != nil {
					t.Fatal(err)
				}
				want := api.ContentOutcomeFirst
				if i >= 2 {
					want = "same_60"
				}
				if got := tee.finish(now.Add(time.Duration(i) * time.Second)); got != want {
					t.Fatalf("request %q classified as %q, want %q", target, got, want)
				}
			}
		})
	}
}

func TestResponseCache_MethodCaseSensitive(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	h.WithResponseCache(NewResponseCache())
	seedCacheRule(t, h, b.host, EdgeRuleCacheResolved{ID: "method-rule", PathGlob: "/catalog", MaxAgeSeconds: 60})
	calls := 0
	h.proxyByNode = func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			_, _ = w.Write([]byte(r.Method))
		})
	}
	for _, method := range []string{http.MethodGet, "get", "get", http.MethodGet} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "http://"+b.host+"/catalog", nil))
		if w.Code != http.StatusOK || w.Body.String() != method {
			t.Fatalf("method %q received %d %q", method, w.Code, w.Body.String())
		}
	}
	if calls != 3 {
		t.Fatalf("origin calls = %d, want lowercase methods to bypass caching", calls)
	}
}

func TestResponseCache_URLVariantsStaleRefreshAndSharedPurge(t *testing.T) {
	server := miniredis.RunT(t)
	shared, err := NewRedisResponseCache(t.Context(), "redis://"+server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shared.Close() })
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fresh " + r.RequestURI))
	}))
	t.Cleanup(origin.Close)
	h, b, _ := newTestHandler(t)
	b.upstream = origin.Listener.Addr().String()
	b.setLegacyHot()
	cache := NewResponseCache().WithSharedStore(shared)
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: "url-rule", PathGlob: "/catalog/a/b", MaxAgeSeconds: 60, StaleIfErrorSeconds: 300}
	paths := []string{"/catalog/a%2Fb?id=2&id=1", "/catalog/a/b?id=1&id=2", "/catalog/a%2Fb?", "/catalog/a/b"}
	now := time.Now()
	for _, path := range paths {
		r := httptest.NewRequest(http.MethodGet, "http://"+b.host+path, nil)
		entry := &cacheEntry{
			key: responseCacheKeyForRequest(r, b.app.ID, "", &rule), statusCode: http.StatusOK,
			body: []byte("stale " + path), freshUntil: now.Add(-time.Second),
			errorUntil: now.Add(time.Minute), staleUntil: now.Add(time.Minute), ruleAction: rule.toStateEdgeRuleCacheAction(),
		}
		retryRedisResponseCacheIO(t, func() error { return shared.Put(entry) })
	}
	for _, path := range paths {
		r := httptest.NewRequest(http.MethodGet, "http://"+b.host+path, nil)
		key := responseCacheKeyForRequest(r, b.app.ID, "", &rule)
		r = r.WithContext(withCacheKeyContext(r.Context(), &rule, key))
		for _, serve := range []func(http.ResponseWriter, *http.Request, App, *statusRecorder) (bool, string){
			h.tryServeStaleOnWakeError, h.serveStaleWhileWaking,
		} {
			w := httptest.NewRecorder()
			rec := newTestStatusRecorder(w)
			if served, _ := serve(rec, r, b.app, rec); !served || w.Body.String() != "stale "+path {
				t.Fatalf("stale request %q received %q (served=%v)", path, w.Body.String(), served)
			}
		}
		h.refreshCacheFromWarmTarget(r.Context(), r, b.app, &rule, key)
		var refreshed *cacheEntry
		retryRedisResponseCacheIO(t, func() error {
			refreshed, err = shared.Get(key)
			return err
		})
		if refreshed == nil || string(refreshed.body) != "fresh "+path {
			t.Fatalf("refresh did not update shared variant %q", path)
		}
	}
	if cache.Len() != len(paths) {
		t.Fatalf("local entries = %d, want %d variants", cache.Len(), len(paths))
	}
	retryRedisResponseCacheIO(t, func() error { return cache.InvalidateByAppPath(b.app.ID, "/catalog/a/b") })
	if cache.Len() != 0 {
		t.Fatal("path purge left local variants behind")
	}
	for _, path := range paths {
		r := httptest.NewRequest(http.MethodGet, "http://"+b.host+path, nil)
		key := responseCacheKeyForRequest(r, b.app.ID, "", &rule)
		var entry *cacheEntry
		retryRedisResponseCacheIO(t, func() error {
			entry, err = shared.Get(key)
			return err
		})
		if entry != nil {
			t.Fatalf("path purge left shared variant %q behind", path)
		}
	}
}
