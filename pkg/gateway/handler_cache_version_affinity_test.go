package gateway

// adr: 122 — response-cache keys must keep rollout cohorts isolated.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type cacheVersionAffinityBackend struct {
	*fakeBackend
	deploymentID string
}

type cacheColdFallbackBackend struct {
	*cacheVersionAffinityBackend
	stable Target
}

func (b *cacheColdFallbackBackend) PickForVersionKey(string, string, string) PickResult {
	return PickResult{Target: b.stable, OK: true, Picked: "dep-candidate", ColdBucket: "dep-candidate"}
}

func (b *cacheColdFallbackBackend) Admit(context.Context, string, string, string, string, int) (string, WakeMethod, bool, error) {
	return "", WakeMethodUnspecified, false, errors.New("candidate wake unavailable")
}

func (b *cacheVersionAffinityBackend) AffinityDeployment(string, string) (string, bool) {
	return b.deploymentID, b.deploymentID != ""
}

func TestResponseCachePartitionsVersionAffinityByDeployment(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	h, backend, _ := newTestHandler(t)
	h.backend = &cacheVersionAffinityBackend{fakeBackend: backend, deploymentID: "dep-candidate"}
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: "rule-cache-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	app := App{ID: "app-1", Plan: api.PlanPro}
	base := CacheKey{AppID: app.ID, RuleID: rule.ID, Method: http.MethodGet, NormalizedPath: "/catalog", VaryHash: hashStable("")}
	unkeyed := base
	candidate := base
	candidate.DeploymentID = "dep-candidate"
	cache.Put(unkeyed, http.StatusOK, nil, []byte("stable-or-cursor"), now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction())
	cache.Put(candidate, http.StatusOK, nil, []byte("candidate"), now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction())

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/catalog", nil)
	req.Header.Set(api.VersionKeyHeader, "customer-42")
	w := httptest.NewRecorder()
	rec := newTestStatusRecorder(w)
	served, _ := h.applyEdgeRuleCache(w, req, app, rec)
	if !served || w.Body.String() != "candidate" {
		t.Fatalf("keyed cache response = served %v body %q, want candidate partition", served, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/catalog", nil)
	w = httptest.NewRecorder()
	rec = newTestStatusRecorder(w)
	served, _ = h.applyEdgeRuleCache(w, req, app, rec)
	if !served || w.Body.String() != "stable-or-cursor" {
		t.Fatalf("unkeyed cache response = served %v body %q, want legacy partition", served, w.Body.String())
	}
}

func TestResponseCacheDoesNotStoreWarmFallbackInKeyedPartition(t *testing.T) {
	cache := NewResponseCache()
	h, backend, upstream := newTestHandler(t)
	stable := Target{NodeID: upstream.Listener.Addr().String(), InstanceID: "stable-1", DeploymentID: "dep-stable"}
	backend.AddTarget(stable)
	h.backend = &cacheColdFallbackBackend{
		cacheVersionAffinityBackend: &cacheVersionAffinityBackend{fakeBackend: backend, deploymentID: "dep-candidate"},
		stable:                      stable,
	}
	h.WithResponseCache(cache)
	seedCacheRule(t, h, "jane-api.apps.dom", EdgeRuleCacheResolved{ID: "rule-cache-1", PathGlob: "/catalog", MaxAgeSeconds: 60})

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/catalog", nil)
	req.Header.Set(api.VersionKeyHeader, "customer-42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "hello from app" {
		t.Fatalf("fallback response = %d %q, want stable 200", w.Code, w.Body.String())
	}
	if got := cache.Len(); got != 0 {
		t.Fatalf("cache entries = %d, want no candidate entry for stable fallback", got)
	}
}
