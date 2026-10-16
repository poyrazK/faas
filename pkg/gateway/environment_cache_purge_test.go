// adr: 1353 — purge generations isolate environments and fence late cache writes.
package gateway

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEnvironmentPurgeNamespacesFenceSharedAndLateWrites(t *testing.T) {
	app, env, neighbor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	shared := &memorySharedResponseCache{entries: map[string]*cacheEntry{}}
	cache := NewResponseCache().WithSharedStore(shared)
	if _, ready := cache.EnvironmentPurgeToken(app, env, "/item"); ready {
		t.Fatal("startup trusted unreplayed cache")
	}
	cache.MarkEnvironmentPurgesReady()
	key := func(environment, path string) CacheKey {
		token, ready := cache.EnvironmentPurgeToken(app, environment, path)
		if !ready {
			t.Fatal("not ready")
		}
		return CacheKey{AppID: app, EnvironmentID: environment, PurgeToken: token, RuleID: "rule", Method: "GET", NormalizedPath: path}
	}
	put := func(k CacheKey) {
		t.Helper()
		if !cache.Put(k, 200, nil, []byte("old"), time.Now().Add(time.Minute), time.Now().Add(time.Minute), nil) {
			t.Fatal("put failed")
		}
	}
	old := key(env, "/item")
	otherPath := key(env, "/other")
	otherEnv := key(neighbor, "/item")
	put(old)
	put(otherPath)
	put(otherEnv)
	if err := cache.ApplyEnvironmentPurge(app, env, "/item", 1); err != nil {
		t.Fatal(err)
	}
	put(old) // Origin response started before purge, written after application.
	next := key(env, "/item")
	if old.String() == next.String() {
		t.Fatal("purge generation did not partition key")
	}
	if _, entry := cache.Get(next); entry != nil {
		t.Fatal("late response became reachable")
	}
	for _, k := range []CacheKey{otherPath, otherEnv} {
		if state, _ := cache.Get(k); state != "fresh" {
			t.Fatal("path purge evicted sibling")
		}
	}
	restarted := NewResponseCache().WithSharedStore(shared)
	if err := restarted.ApplyEnvironmentPurge(app, env, "/item", 1); err != nil {
		t.Fatal(err)
	}
	restarted.MarkEnvironmentPurgesReady()
	token, _ := restarted.EnvironmentPurgeToken(app, env, "/item")
	if token != next.PurgeToken {
		t.Fatal("restart did not restore durable namespace")
	}
	if _, entry := restarted.Get(next); entry != nil {
		t.Fatal("restart exposed old shared namespace")
	}
	if err := cache.ApplyEnvironmentPurge(app, env, "", 2); err != nil {
		t.Fatal(err)
	}
	if _, entry := cache.Get(key(env, "/other")); entry != nil {
		t.Fatal("whole environment retained old path")
	}
	if state, _ := cache.Get(otherEnv); state != "fresh" {
		t.Fatal("whole environment evicted another environment")
	}
}

func TestEnvironmentPurgeHandlerCapturesOneKeyAcrossOriginAndStalePaths(t *testing.T) {
	appID, environment := uuid.NewString(), uuid.NewString()
	host := BuildEnvironmentHost(wire.DeployWildcardSuffix, environment, appID)
	cache := NewResponseCache()
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: uuid.NewString(), PathGlob: "/item", MaxAgeSeconds: 60, StaleIfErrorSeconds: 60}
	seedCacheRule(t, h, host, rule)
	app := App{ID: appID, Plan: api.PlanPro}
	req := httptest.NewRequest("GET", "http://"+host+"/item?x=1", nil)
	w := httptest.NewRecorder()
	rec := newTestStatusRecorder(w)
	if served, matched := h.applyEdgeRuleCache(w, req, app, rec); served || matched != nil {
		t.Fatal("startup bypass left a cache writer rule")
	}
	cache.MarkEnvironmentPurgesReady()
	if served, matched := h.applyEdgeRuleCache(w, req, app, rec); served || matched == nil {
		t.Fatal("ready cache did not return miss rule")
	}
	old, ok := responseCacheKeyFromContext(req.Context())
	if !ok || old.EnvironmentID != environment || old.PurgeToken != "0/0" {
		t.Fatal("lookup did not capture environment key")
	}
	req = req.WithContext(withCacheRuleContext(req.Context(), &rule, appID, "GET", "/item", "x=1", computeVaryHash(req, rule.VaryOn)))
	if snapshot := cacheRuleFromContext(req.Context()); snapshot == nil || snapshot.cacheKey() != old {
		t.Fatal("stale/refresh context changed lookup key")
	}
	if err := cache.ApplyEnvironmentPurge(appID, environment, "/item", 1); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-purge origin request finishing after the generation changed.
	cache.Put(old, 200, nil, []byte("old"), time.Now().Add(-time.Second), time.Now().Add(time.Minute), rule.toStateEdgeRuleCacheAction())
	newer := httptest.NewRequest("GET", "http://"+host+"/item?x=1", nil)
	out := httptest.NewRecorder()
	outRec := newTestStatusRecorder(out)
	if served, _ := h.applyEdgeRuleCache(out, newer, app, outRec); served {
		t.Fatal("new request replayed pre-purge response")
	}
	next, ok := responseCacheKeyFromContext(newer.Context())
	if !ok || next.PurgeToken == old.PurgeToken {
		t.Fatal("new request retained old generation")
	}
	newer = newer.WithContext(withCacheRuleContext(newer.Context(), &rule, appID, "GET", "/item", "x=1", computeVaryHash(newer, rule.VaryOn)))
	if served, _ := h.tryServeStaleOnWakeError(out, newer, app, outRec); served {
		t.Fatal("stale fallback escaped purge generation")
	}
	if snapshot := cacheRuleFromContext(req.Context()); snapshot.cacheKey() != old {
		t.Fatal("origin completion rebased old request onto new namespace")
	}
	cache.Put(next, 200, nil, []byte("new"), time.Now().Add(time.Minute), time.Now().Add(time.Minute), rule.toStateEdgeRuleCacheAction())
	fresh := httptest.NewRequest("GET", "http://"+host+"/item?x=1", nil)
	freshOut := httptest.NewRecorder()
	if served, _ := h.applyEdgeRuleCache(freshOut, fresh, app, newTestStatusRecorder(freshOut)); !served || freshOut.Body.String() != "new" {
		t.Fatal("new-generation response was not cacheable")
	}
}
