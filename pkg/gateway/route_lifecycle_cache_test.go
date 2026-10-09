// adr: 818
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
)

type cacheLifecycleMatcher struct {
	deployment, path, method string
	date                     time.Time
	err                      error
}

func (m *cacheLifecycleMatcher) MatchDeclaredRoute(context.Context, App, string, string) (bool, error) {
	return true, nil
}
func (m *cacheLifecycleMatcher) ResolveDeploymentRouteLifecycle(_ context.Context, _ App, id, path, method string) (routelifecycle.Metadata, error) {
	m.deployment, m.path, m.method = id, path, method
	return routelifecycle.Metadata{DeprecatedAt: m.date, SunsetAt: m.date.Add(time.Hour), Successor: "https://example.com/new"}, m.err
}
func TestCachedLifecycleFreshAndStale(t *testing.T) {
	for _, mode := range []string{"fresh", "stale_revalidate", "stale_waking", "stale_error"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
			h, _, _ := newTestHandler(t)
			h.WithResponseCache(cache)
			rule := EdgeRuleCacheResolved{ID: "rule", PathGlob: "/internal", MaxAgeSeconds: 60, StaleWhileRevalidateSeconds: 60, StaleIfErrorSeconds: 300}
			seedCacheRule(t, h, "jane-api.apps.dom", rule)
			// Disable background refresh for this replay test; existing refresh tests
			// cover origin refresh independently.
			h.backend = nil
			matcher := &cacheLifecycleMatcher{date: time.Unix(200, 0)}
			h.WithDeclaredRouteMatcher(matcher)
			app := App{ID: "app-1", AccountID: "account", Plan: api.PlanPro}
			key := CacheKey{AppID: app.ID, RuleID: rule.ID, Method: "GET", NormalizedPath: "/internal", VaryHash: hashStable("host\x00jane-api.apps.dom")}
			fresh := now.Add(time.Minute)
			revalidate := now.Add(2 * time.Minute)
			if mode != "fresh" {
				fresh = now.Add(-time.Second)
			}
			if mode == "stale_error" || mode == "stale_waking" {
				revalidate = fresh
			}
			if !cache.PutWithDeployment(key, 200, http.Header{"Deprecation": {"@999"}, "Link": {"<https://wrong.example>; rel=\"successor-version\"", "<https://example.com/help>; rel=\"help\""}}, []byte("baseline body"), fresh, revalidate, now.Add(5*time.Minute), rule.toStateEdgeRuleCacheAction(), nil, "baseline") {
				t.Fatal("cache store")
			}
			serve := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest("GET", "http://jane-api.apps.dom/internal", nil)
				req = withLifecycleRequestRoute(req, "/public", "GET")
				req = req.WithContext(withCacheRuleContext(req.Context(), &rule, app.ID, "GET", "/internal", "", hashStable("host\x00jane-api.apps.dom")))
				w := httptest.NewRecorder()
				w.Header().Set("Deprecation", "@failed-origin")
				rec := newTestStatusRecorder(w)
				served := false
				switch mode {
				case "fresh", "stale_revalidate":
					served, _ = h.applyEdgeRuleCache(w, req, app, rec)
				case "stale_waking":
					served, _ = h.serveStaleWhileWaking(w, req, app, rec)
				case "stale_error":
					served, _ = h.tryServeStaleOnWakeError(w, req, app, rec)
				}
				if !served || w.Body.String() != "baseline body" {
					t.Fatalf("served=%v body=%q", served, w.Body.String())
				}
				return w
			}
			w := serve()
			if w.Header().Get("Deprecation") != "@200" || matcher.deployment != "baseline" || matcher.path != "/public" || matcher.method != "GET" || len(w.Header().Values("Link")) != 2 {
				t.Fatalf("headers=%v matcher=%+v", w.Header(), matcher)
			}
			// Changing capture metadata changes guidance without replacing the cached body.
			matcher.date = time.Unix(300, 0)
			w = serve()
			if w.Header().Get("Deprecation") != "@300" {
				t.Fatal("capture update did not affect hit")
			}
			matcher.err = errors.New("capture unavailable")
			w = serve()
			if w.Header().Get("Deprecation") != "" || w.Header().Get("Sunset") != "" || len(w.Header().Values("Link")) != 1 {
				t.Fatalf("failed capture leaked %v", w.Header())
			}
		})
	}
}
func TestCachedLifecycleLegacyAndIsolation(t *testing.T) {
	matcher := &cacheLifecycleMatcher{date: time.Unix(200, 0)}
	h := &Handler{declaredRoutes: matcher}
	app := App{ID: "app"}
	for _, entry := range []*cacheEntry{
		{key: CacheKey{AppID: "app", DeploymentID: "candidate"}}, // old format: never guess from key
		{key: CacheKey{AppID: "other"}, servedDeploymentID: "baseline"},
		{key: CacheKey{AppID: "app", DeploymentID: "candidate"}, servedDeploymentID: "baseline"},
	} {
		w := httptest.NewRecorder()
		w.Header().Set("Deprecation", "@failed-origin")
		h.applyCachedRouteLifecycle(w, httptest.NewRequest("GET", "/public", nil), app, entry)
		if w.Header().Get("Deprecation") != "" || matcher.deployment != "" {
			t.Fatal("legacy or mismatched identity emitted guidance")
		}
	}
	w := httptest.NewRecorder()
	h.applyCachedRouteLifecycle(w, httptest.NewRequest("GET", "/public", nil), App{ID: "app", PinnedDeploymentID: "candidate"}, &cacheEntry{key: CacheKey{AppID: "app"}, servedDeploymentID: "baseline"})
	if matcher.deployment != "" {
		t.Fatal("pinned identity mismatch")
	}
}
func TestCachedLifecycleWriterAndSharedHydration(t *testing.T) {
	now := time.Now()
	shared := &memorySharedResponseCache{entries: map[string]*cacheEntry{}}
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	cache.WithSharedStore(shared)
	rule := EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 60}
	rec := newTestStatusRecorder(httptest.NewRecorder())
	writer := newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	writer.servedDeploymentID = "canary"
	writer.WriteHeader(200)
	writer.Write([]byte("canary body"))
	key := CacheKey{AppID: "app", RuleID: "rule", Method: "GET", NormalizedPath: "/public"}
	if !writer.finishCacheCapture(cache, key, now) {
		t.Fatal("capture not stored")
	}
	another := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	another.WithSharedStore(shared)
	_, entry := another.Get(key)
	if entry == nil || entry.servedDeploymentID != "canary" || string(entry.body) != "canary body" {
		t.Fatal("L2 lost deployment identity")
	}
	// A stale fallback inside a capture writer must not refresh this body or its identity.
	rec = newTestStatusRecorder(httptest.NewRecorder())
	writer = newCacheWriter(rec, rec, &rule, ResponseCachePerEntryMaxBytes)
	(&Handler{}).applyCachedRouteLifecycle(writer, httptest.NewRequest("GET", "/public", nil), App{ID: "app"}, entry)
	writer.WriteHeader(200)
	writer.Write([]byte("cached body"))
	writer.servedDeploymentID = "failed-origin"
	if writer.finishCacheCapture(cache, key, now) {
		t.Fatal("stale replay recaptured as failed origin")
	}
}
func TestCachedLifecycleRedisCompatibility(t *testing.T) {
	record := redisResponseCacheRecord{Version: 1, Key: CacheKey{AppID: "app"}, ServedDeploymentID: "baseline", Body: []byte("body")}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var decoded redisResponseCacheRecord
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.cacheEntry().servedDeploymentID != "baseline" {
		t.Fatal("Redis round-trip lost identity")
	}
	var legacy redisResponseCacheRecord
	if err := json.Unmarshal([]byte(`{"version":1,"key":{"AppID":"app","DeploymentID":"candidate"},"body":"Ym9keQ=="}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.cacheEntry().servedDeploymentID != "" || string(legacy.cacheEntry().body) != "body" {
		t.Fatal("legacy Redis compatibility")
	}
}

func TestCachedLifecycleForegroundOriginCapture(t *testing.T) {
	h, backend, upstream := newTestHandler(t)
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "instance", DeploymentID: "baseline"})
	cache := NewResponseCache()
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: "rule", PathGlob: "/public", MaxAgeSeconds: 60}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	matcher := &cacheLifecycleMatcher{date: time.Unix(200, 0)}
	h.WithDeclaredRouteMatcher(matcher)
	serve := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://jane-api.apps.dom/public", nil))
		if w.Code != 200 {
			t.Fatalf("response %d: %s", w.Code, w.Body.String())
		}
		return w
	}
	first := serve()
	if first.Header().Get("Deprecation") != "@200" {
		t.Fatalf("origin metadata %v", first.Header())
	}
	request := httptest.NewRequest("GET", "http://jane-api.apps.dom/public", nil)
	key := CacheKey{AppID: "app-1", RuleID: "rule", Method: "GET", NormalizedPath: "/public", VaryHash: computeVaryHash(request, nil)}
	_, entry := cache.Get(key)
	if entry == nil || entry.servedDeploymentID != "baseline" {
		t.Fatalf("actual origin capture %+v", entry)
	}
	matcher.date = time.Unix(300, 0)
	second := serve()
	if second.Header().Get("Deprecation") != "@300" || second.Body.String() != first.Body.String() {
		t.Fatalf("hit headers=%v body=%s", second.Header(), second.Body.String())
	}
}

type lifecycleRefreshBackend struct {
	*fakeBackend
	target     Target
	exactPicks int
}

func (b *lifecycleRefreshBackend) PickForDeployment(appID, id string) PickResult {
	b.exactPicks++
	return PickResult{OK: true, Target: b.target}
}
func TestCachedLifecycleRefreshDeploymentIsolation(t *testing.T) {
	h, base, upstream := newTestHandler(t)
	base.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "baseline-instance", DeploymentID: "baseline"})
	backend := &lifecycleRefreshBackend{fakeBackend: base, target: Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "canary-instance", DeploymentID: "canary"}}
	h.backend = backend
	cache := NewResponseCache()
	h.WithResponseCache(cache)
	rule := &EdgeRuleCacheResolved{ID: "rule", MaxAgeSeconds: 60}
	key := CacheKey{AppID: "app-1", DeploymentID: "canary", RuleID: "rule", Method: "GET", NormalizedPath: "/public"}
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/public", nil)
	app := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	h.refreshCacheFromWarmTarget(context.Background(), req, app, rule, key)
	_, entry := cache.Get(key)
	if backend.exactPicks != 1 || entry == nil || entry.servedDeploymentID != "canary" {
		t.Fatalf("refresh entry=%+v picks=%d", entry, backend.exactPicks)
	}
	backend.target.DeploymentID = "baseline"
	key.NormalizedPath = "/different"
	h.refreshCacheFromWarmTarget(context.Background(), req, app, rule, key)
	if _, entry := cache.Get(key); entry != nil {
		t.Fatal("sibling response stored under canary key")
	}
}

func TestCachedLifecycleRejectsSiblingWrite(t *testing.T) {
	cache := NewResponseCache()
	now := time.Now()
	key := CacheKey{AppID: "app", DeploymentID: "canary"}
	if cache.PutWithDeployment(key, 200, nil, []byte("baseline"), now.Add(time.Minute), now.Add(time.Minute), now.Add(time.Minute), nil, nil, "baseline") {
		t.Fatal("stored sibling body under selected deployment")
	}
	if cache.Len() != 0 {
		t.Fatal("rejected write populated cache")
	}
}
