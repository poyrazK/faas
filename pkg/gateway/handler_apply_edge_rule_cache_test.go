// adr: 211
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestApplyEdgeRuleCache_MissWhenNoRule verifies that a request
// for a host with no cache rule falls through (returns false).
// The applier must NEVER serve a cached body when the host has no
// kind=cache rule — that's the deny-by-default posture.
func TestApplyEdgeRuleCache_MissWhenNoRule(t *testing.T) {
	h, _, _ := newTestHandler(t)
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
	rec := newTestStatusRecorder(httptest.NewRecorder())
	got, _ := h.applyEdgeRuleCache(rec, req, App{ID: "app-1", Plan: api.PlanPro}, rec)
	if got {
		t.Fatalf("applyEdgeRuleCache returned true with no rule installed")
	}
}

// TestApplyEdgeRuleCache_BypassOnAuthorization verifies that a
// request carrying an Authorization header bypasses the cache
// even when a matching rule is present. This is ADR-122 D3 — the
// load-bearing safety property.
func TestApplyEdgeRuleCache_BypassOnAuthorization(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(NewResponseCache())
	rule := EdgeRuleCacheResolved{
		ID:                  "rule-cache-1",
		PathGlob:            "/catalog",
		MaxAgeSeconds:       60,
		StaleIfErrorSeconds: 300,
	}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
	req.Header.Set("Authorization", "Bearer some-token")
	rec := newTestStatusRecorder(httptest.NewRecorder())
	got, _ := h.applyEdgeRuleCache(rec, req, App{ID: "app-1", Plan: api.PlanPro}, rec)
	if got {
		t.Fatalf("authed request should bypass cache")
	}
	if h.responseCache.Len() != 0 {
		t.Fatalf("bypass path must not create cache entries; got %d", h.responseCache.Len())
	}
}

// TestApplyEdgeRuleCache_MethodGateOnlyGet verifies that POST
// (and other non-GET/HEAD methods) are NEVER served from cache.
// A cache that served a POST response to a subsequent POST would
// be a CSRF vector.
func TestApplyEdgeRuleCache_MethodGateOnlyGet(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(NewResponseCache())
	rule := EdgeRuleCacheResolved{
		ID:                  "rule-cache-1",
		PathGlob:            "/catalog",
		MaxAgeSeconds:       60,
		StaleIfErrorSeconds: 300,
	}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(method, "http://jane-api.apps.dom/catalog", nil)
		rec := newTestStatusRecorder(httptest.NewRecorder())
		got, _ := h.applyEdgeRuleCache(rec, req, App{ID: "app-1", Plan: api.PlanPro}, rec)
		if got {
			t.Errorf("%s should be method-gated out", method)
		}
	}
}

// TestApplyEdgeRuleCache_HitReplaysBody verifies the happy path:
// a fresh entry is replayed verbatim to the response writer,
// bypassing the upstream entirely.
func TestApplyEdgeRuleCache_HitReplaysBody(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, clock)
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{
		ID:                  "rule-cache-1",
		PathGlob:            "/catalog",
		MaxAgeSeconds:       60,
		StaleIfErrorSeconds: 300,
	}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	app := App{ID: "app-1", Plan: api.PlanPro}
	key := CacheKey{
		AppID:          app.ID,
		RuleID:         rule.ID,
		Method:         "GET",
		NormalizedPath: "/catalog",
		VaryHash:       hashStable(""),
	}
	cache.Put(key, 200,
		http.Header{"Content-Type": []string{"application/json"}},
		[]byte(`{"items":["a","b"]}`),
		now.Add(60*time.Second),
		now.Add(360*time.Second),
		rule.toStateEdgeRuleCacheAction(),
	)
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
	w := httptest.NewRecorder()
	rec := newTestStatusRecorder(w)
	got, _ := h.applyEdgeRuleCache(w, req, app, rec)
	if !got {
		t.Fatalf("applyEdgeRuleCache returned false on a fresh hit")
	}
	if !strings.Contains(w.Body.String(), `"items"`) {
		t.Errorf("body not replayed: %q", w.Body.String())
	}
	if w.Code != 200 {
		t.Errorf("status not replayed: got %d", w.Code)
	}
	if rec.Bytes != int64(len(`{"items":["a","b"]}`)) {
		t.Errorf("rec.Bytes = %d, want %d", rec.Bytes, len(`{"items":["a","b"]}`))
	}
}

// TestApplyEdgeRuleCache_StaleNotServedOnMiss verifies that an
// entry past its fresh window but within the stale window is NOT
// served on a regular hit path. Stale-on-error is a wake-failure
// path (commit 13) — it must not appear as a normal hit.
func TestApplyEdgeRuleCache_StaleNotServedOnMiss(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, clock)
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{
		ID:                  "rule-cache-1",
		PathGlob:            "/catalog",
		MaxAgeSeconds:       60,
		StaleIfErrorSeconds: 300,
	}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	app := App{ID: "app-1", Plan: api.PlanPro}
	key := CacheKey{
		AppID:          app.ID,
		RuleID:         rule.ID,
		Method:         "GET",
		NormalizedPath: "/catalog",
		VaryHash:       hashStable(""),
	}
	cache.Put(key, 200,
		http.Header{"Content-Type": []string{"application/json"}},
		[]byte(`{}`),
		now.Add(-1*time.Second), // already expired
		now.Add(300*time.Second),
		rule.toStateEdgeRuleCacheAction(),
	)
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
	w := httptest.NewRecorder()
	rec := newTestStatusRecorder(w)
	got, _ := h.applyEdgeRuleCache(w, req, app, rec)
	if got {
		t.Fatalf("stale entry must not be served on the normal hit path")
	}
}

func TestApplyEdgeRuleCache_StaleWhileRevalidate(t *testing.T) {
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(cache)
	// The serve behavior is independent of the refresh transport. A nil backend
	// makes the asynchronous refresh a deliberate no-op in this focused test.
	h.backend = nil
	rule := EdgeRuleCacheResolved{
		ID:                          "rule-cache-swr",
		PathGlob:                    "/products/42",
		MaxAgeSeconds:               30,
		StaleWhileRevalidateSeconds: 60,
		StaleIfErrorSeconds:         300,
	}
	seedCacheRule(t, h, "shop.apps.dom", rule)
	app := App{ID: "app-shop", Plan: api.PlanPro}
	key := CacheKey{
		AppID:          app.ID,
		RuleID:         rule.ID,
		Method:         "GET",
		NormalizedPath: "/products/42",
		VaryHash:       hashStable(""),
	}
	cache.PutWithWindows(
		key,
		http.StatusOK,
		http.Header{"Content-Type": []string{"application/json"}},
		[]byte(`{"id":42}`),
		now.Add(-time.Second),
		now.Add(59*time.Second),
		now.Add(299*time.Second),
		rule.toStateEdgeRuleCacheAction(),
	)

	req := httptest.NewRequest("GET", "http://shop.apps.dom/products/42", nil)
	w := httptest.NewRecorder()
	rec := newTestStatusRecorder(w)
	served, _ := h.applyEdgeRuleCache(w, req, app, rec)
	if !served {
		t.Fatal("stale-while-revalidate entry was not served")
	}
	if got := w.Header().Get("x-faas-cache"); got != "stale-while-revalidate" {
		t.Fatalf("x-faas-cache = %q, want stale-while-revalidate", got)
	}
	if got := w.Header().Get("Warning"); !strings.Contains(got, "110") {
		t.Fatalf("Warning = %q, want stale response warning", got)
	}
	if got := w.Body.String(); got != `{"id":42}` {
		t.Fatalf("body = %q, want cached product", got)
	}
}

// TestApplyEdgeRuleCache_NilCacheReturnsFalse ensures the
// applier degrades gracefully when no cache has been wired —
// it MUST NOT panic, MUST return false, and the request must
// fall through to the wake gate.
func TestApplyEdgeRuleCache_NilCacheReturnsFalse(t *testing.T) {
	h, _, _ := newTestHandler(t)
	// Don't call WithResponseCache.
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
	rec := newTestStatusRecorder(httptest.NewRecorder())
	got, _ := h.applyEdgeRuleCache(rec, req, App{ID: "app-1", Plan: api.PlanPro}, rec)
	if got {
		t.Fatalf("nil cache must produce false (fall through to wake)")
	}
}

// TestHasSessionCookie covers the auth-bypass predicate for the
// cookie half of the rule. A request with any cookie is treated
// as authed; only cookieless + headerless requests are eligible.
func TestHasSessionCookie(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if hasSessionCookie(r) {
		t.Fatalf("cookieless request reported as authed")
	}
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.AddCookie(&http.Cookie{Name: "session", Value: "abc"})
	if !hasSessionCookie(r2) {
		t.Fatalf("cookied request reported as anon")
	}
}

// TestIsHopByHopHeader covers the header drop-list symmetry
// between Put and Get paths.
func TestIsHopByHopHeader(t *testing.T) {
	cases := map[string]bool{
		"Connection":        true,
		"connection":        true,
		"KEEP-ALIVE":        true,
		"Transfer-Encoding": true,
		"Upgrade":           true,
		"Content-Type":      false,
		"ETag":              false,
		"X-Custom-Header":   false,
		"Set-Cookie":        false, // important: we DO want this on the replay path
	}
	for k, want := range cases {
		if got := isHopByHopHeader(k); got != want {
			t.Errorf("isHopByHopHeader(%q) = %v, want %v", k, got, want)
		}
	}
}

// newTestStatusRecorder returns a statusRecorder backed by the
// given recorder. Keeps tests decoupled from the package-private
// constructor if one ever grows on Handler.
func newTestStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: 200}
}

// seedCacheRule arms the handler's matcher with a single cache
// rule for `host`. The h.edgeRules field is typed as the
// EdgeRuleMatcher interface; tests use a thin wrapper that
// embeds noOpEdgeRuleMatcher for the 13 unrelated kinds and
// adds a real MatchCache against an in-memory rule slice.
func seedCacheRule(t *testing.T, h *Handler, host string, rule EdgeRuleCacheResolved) {
	t.Helper()
	m := &cacheOnlyMatcher{rules: map[string][]EdgeRuleCacheResolved{host: {rule}}}
	h.edgeRules = m
}

// cacheOnlyMatcher is a test-only EdgeRuleMatcher that returns
// nil for every kind except kind=cache. Embedding noOpEdgeRuleMatcher
// supplies the 13 default no-op Match* methods; the cache
// MatchCache is the only one that does real work. Mirrors the
// production pattern where a future kind plugs into the matcher
// by embedding noOpEdgeRuleMatcher and overriding the one method
// that needs to do work.
type cacheOnlyMatcher struct {
	noOpEdgeRuleMatcher
	rules map[string][]EdgeRuleCacheResolved
}

func (c *cacheOnlyMatcher) MatchCache(_ context.Context, host, requestPath, method string) *EdgeRuleCacheResolved {
	rs, ok := c.rules[host]
	if !ok {
		return nil
	}
	return PickFirstCacheMatch(rs, requestPath, method)
}
func (c *cacheOnlyMatcher) Reset() {
	c.rules = map[string][]EdgeRuleCacheResolved{}
}
