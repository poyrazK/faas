package gateway

import (
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
