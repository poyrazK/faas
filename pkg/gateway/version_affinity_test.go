package gateway

// adr: 084 — keyed affinity refines the existing traffic-split routing contract.
// adr: 122 — the managed cookie must preserve response-cache isolation.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type cookieAffinityBackend struct {
	*fakeBackend
	pickedKey string
}

type versionHeaderRuleMatcher struct{ noOpEdgeRuleMatcher }

func (*versionHeaderRuleMatcher) MatchHeaders(context.Context, string, string, string) *EdgeRuleHeadersResolved {
	return &EdgeRuleHeadersResolved{
		AccountID:      "acct-1",
		RequestHeaders: []EdgeRuleHeaderOp{{Name: api.VersionKeyHeader, Action: "set", Value: "rule-key"}},
	}
}

func (b *cookieAffinityBackend) AffinityDeployment(string, string) (string, bool) {
	return "dep-candidate", true
}

func (b *cookieAffinityBackend) PickForVersionKey(appID, key, _ string) PickResult {
	b.pickedKey = key
	return b.fakeBackend.Pick(appID)
}

func TestVersionAffinityKeyFromRequest(t *testing.T) {
	tests := []struct {
		name        string
		values      []string
		wantKey     string
		wantOutcome string
	}{
		{name: "missing", wantOutcome: versionAffinityKeyMissing},
		{name: "valid and trimmed", values: []string{"  customer-42  "}, wantKey: "customer-42", wantOutcome: versionAffinityKeyValid},
		{name: "empty", values: []string{"  "}, wantOutcome: versionAffinityKeyInvalid},
		{name: "oversized", values: []string{strings.Repeat("x", VersionAffinityKeyMaxBytes+1)}, wantOutcome: versionAffinityKeyInvalid},
		{name: "duplicate", values: []string{"a", "b"}, wantOutcome: versionAffinityKeyInvalid},
		{name: "nul", values: []string{"a\x00b"}, wantOutcome: versionAffinityKeyInvalid},
		{name: "control", values: []string{"a\x1fb"}, wantOutcome: versionAffinityKeyInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
			if tc.values != nil {
				req.Header[api.VersionKeyHeader] = tc.values
			}
			key, outcome := versionAffinityKeyFromRequest(req)
			if key != tc.wantKey || outcome != tc.wantOutcome {
				t.Fatalf("versionAffinityKeyFromRequest = %q/%q, want %q/%q", key, outcome, tc.wantKey, tc.wantOutcome)
			}
		})
	}
}

func TestVersionAffinityKeyFromPublicRequestCookie(t *testing.T) {
	tests := []struct {
		name        string
		cookie      string
		header      []string
		wantOutcome string
		wantKey     string
	}{
		{name: "missing", wantOutcome: versionAffinityKeyMissing},
		{name: "valid cookie", cookie: "visitor_id=user-42", wantOutcome: versionAffinityKeyValid},
		{name: "empty cookie", cookie: "visitor_id=", wantOutcome: versionAffinityKeyInvalid},
		{name: "duplicate cookie", cookie: "visitor_id=one; visitor_id=two", wantOutcome: versionAffinityKeyInvalid},
		{name: "oversized cookie", cookie: "visitor_id=" + strings.Repeat("x", VersionAffinityKeyMaxBytes+1), wantOutcome: versionAffinityKeyInvalid},
		{name: "explicit header wins", cookie: "visitor_id=user-42", header: []string{"manual-key"}, wantOutcome: versionAffinityKeyValid, wantKey: "manual-key"},
		{name: "invalid header does not fall back", cookie: "visitor_id=user-42", header: []string{"", "manual-key"}, wantOutcome: versionAffinityKeyInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
			}
			if tc.header != nil {
				req.Header[api.VersionKeyHeader] = tc.header
			}
			key, outcome := versionAffinityKeyFromPublicRequest(req, "visitor_id")
			if outcome != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q", outcome, tc.wantOutcome)
			}
			if tc.wantKey != "" && key != tc.wantKey {
				t.Fatalf("key = %q, want %q", key, tc.wantKey)
			}
			if tc.wantKey == "" && outcome == versionAffinityKeyValid {
				if key == "user-42" || len(key) != 64 || req.Header.Get(api.VersionKeyHeader) != key {
					t.Fatalf("cookie key was not hashed and forwarded: %q", key)
				}
			}
			if outcome == versionAffinityKeyInvalid && tc.header == nil && req.Header.Get(api.VersionKeyHeader) != "" {
				t.Fatal("invalid cookie must not create a version header")
			}
		})
	}

	first := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
	first.Header.Set("Cookie", "visitor_id=user-42")
	second := httptest.NewRequest(http.MethodGet, "http://app.example/other", nil)
	second.Header.Set("Cookie", "visitor_id=user-42")
	firstKey, _ := versionAffinityKeyFromPublicRequest(first, "visitor_id")
	secondKey, _ := versionAffinityKeyFromPublicRequest(second, "visitor_id")
	if firstKey != secondKey {
		t.Fatalf("same cookie routed to different keys: %q != %q", firstKey, secondKey)
	}
}

func TestVersionAffinityCookieSelectsRevisionAndForwardsDerivedKey(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.VersionAffinityCookie = "visitor_id"
	backend.AddTarget(Target{NodeID: backend.upstream, InstanceID: "candidate-1", DeploymentID: "dep-candidate"})
	affinity := &cookieAffinityBackend{fakeBackend: backend}
	h.backend = affinity
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/account", nil)
	req.Header.Set("Cookie", "visitor_id=user-42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("response status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(affinity.pickedKey) != 64 || affinity.pickedKey == "user-42" {
		t.Fatalf("picker key = %q, want hashed cookie", affinity.pickedKey)
	}
	if got := req.Header.Get(api.VersionKeyHeader); got != affinity.pickedKey {
		t.Fatalf("forwarded version key = %q, picker used %q", got, affinity.pickedKey)
	}
}

func TestVersionAffinityHeaderRulePrecedesCookieSelection(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.VersionAffinityCookie = "visitor_id"
	backend.AddTarget(Target{NodeID: backend.upstream, InstanceID: "candidate-1", DeploymentID: "dep-candidate"})
	affinity := &cookieAffinityBackend{fakeBackend: backend}
	h.backend = affinity
	h.edgeRules = &versionHeaderRuleMatcher{}
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/account", nil)
	req.Header.Set("Cookie", "visitor_id=user-42")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("response status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if affinity.pickedKey != "rule-key" || req.Header.Get(api.VersionKeyHeader) != "rule-key" {
		t.Fatalf("picker/forwarded key = %q/%q, want rule-key", affinity.pickedKey, req.Header.Get(api.VersionKeyHeader))
	}
}

func TestManagedVersionAffinityCookieFirstRequestAndReplay(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.VersionAffinityManagedCookie = true
	backend.AddTarget(Target{NodeID: backend.upstream, InstanceID: "candidate-1", DeploymentID: "dep-candidate"})
	affinity := &cookieAffinityBackend{fakeBackend: backend}
	h.backend = affinity

	first := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/page", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, first)
	if w.Code != http.StatusOK {
		t.Fatalf("first status = %d: %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != api.ManagedVersionAffinityCookieName {
		t.Fatalf("issued cookies = %+v", cookies)
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 7*24*60*60 {
		t.Fatalf("insecure managed cookie = %+v", cookie)
	}
	if len(cookie.Value) != 32 || affinity.pickedKey == "" || affinity.pickedKey != first.Header.Get(api.VersionKeyHeader) {
		t.Fatalf("first request key = %q, forwarded = %q, cookie = %q", affinity.pickedKey, first.Header.Get(api.VersionKeyHeader), cookie.Value)
	}
	firstKey := affinity.pickedKey
	second := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/asset.js", nil)
	second.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, second)
	if w.Code != http.StatusOK || affinity.pickedKey != firstKey || second.Header.Get(api.VersionKeyHeader) != firstKey {
		t.Fatalf("replay status/key = %d/%q, want %q", w.Code, affinity.pickedKey, firstKey)
	}
	if got := w.Result().Cookies(); len(got) != 0 {
		t.Fatalf("unexpected reissued cookie = %+v", got)
	}
	if hasSessionCookie(second) || second.Header.Get("Cookie") != "" {
		t.Fatalf("platform cookie reached cache/guest: %q", second.Header.Get("Cookie"))
	}
}

func TestManagedVersionAffinityCookieInvalidAndCustomerCookie(t *testing.T) {
	for _, header := range []string{
		api.ManagedVersionAffinityCookieName + "=bad",
		api.ManagedVersionAffinityCookieName + "=00112233445566778899aabbccddeeff; " + api.ManagedVersionAffinityCookieName + "=ffeeddccbbaa99887766554433221100",
	} {
		r := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
		r.Header.Set("Cookie", header)
		key, outcome, token := versionAffinityKeyFromManagedRequest(r)
		if key != "" || outcome != versionAffinityKeyInvalid || token != "" {
			t.Fatalf("invalid cookie %q yielded %q/%q/%q", header, key, outcome, token)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
	r.Header.Set(api.VersionKeyHeader, "manual-key")
	r.Header.Set("Cookie", api.ManagedVersionAffinityCookieName+"=00112233445566778899aabbccddeeff; session=private")
	key, outcome, token := versionAffinityKeyFromManagedRequest(r)
	if key != "manual-key" || outcome != versionAffinityKeyValid || token != "" {
		t.Fatalf("explicit key = %q/%q/%q", key, outcome, token)
	}
	stripManagedVersionAffinityCookie(r)
	if r.Header.Get("Cookie") != "session=private" || !hasSessionCookie(r) {
		t.Fatalf("customer session cookie lost: %q", r.Header.Get("Cookie"))
	}
	r = httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
	r.Header[api.VersionKeyHeader] = []string{"", "duplicate"}
	key, outcome, token = versionAffinityKeyFromManagedRequest(r)
	if key != "" || outcome != versionAffinityKeyInvalid || token != "" {
		t.Fatalf("invalid explicit header fell back to mint: %q/%q/%q", key, outcome, token)
	}
}

func TestManagedVersionAffinityCookieCacheIsolation(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.VersionAffinityManagedCookie = true
	affinity := &cookieAffinityBackend{fakeBackend: backend}
	h.backend = affinity
	now := time.Now()
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, func() time.Time { return now })
	h.WithResponseCache(cache)
	rule := EdgeRuleCacheResolved{ID: "rule-cache", PathGlob: "/catalog", MaxAgeSeconds: 60}
	seedCacheRule(t, h, backend.host, rule)
	cache.Put(CacheKey{AppID: backend.app.ID, DeploymentID: "dep-candidate", RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hashStable("")},
		200, http.Header{"Content-Type": []string{"text/plain"}}, []byte("public cached body"), now.Add(time.Minute), now.Add(time.Minute), rule.toStateEdgeRuleCacheAction())

	first := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/catalog", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, first)
	if w.Code != 200 || w.Body.String() != "public cached body" || len(w.Result().Cookies()) != 1 {
		t.Fatalf("first cache hit = %d/%q, cookies = %+v", w.Code, w.Body.String(), w.Result().Cookies())
	}
	issued := w.Result().Cookies()[0]
	second := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/catalog", nil)
	second.AddCookie(issued)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, second)
	if w.Code != 200 || w.Body.String() != "public cached body" || len(w.Result().Cookies()) != 0 {
		t.Fatalf("replay cache hit = %d/%q, cookies = %+v", w.Code, w.Body.String(), w.Result().Cookies())
	}

	private := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/catalog", nil)
	private.AddCookie(issued)
	private.AddCookie(&http.Cookie{Name: "session", Value: "private"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, private)
	if w.Code != 200 || w.Body.String() == "public cached body" {
		t.Fatalf("customer cookie reached shared cache: %d/%q", w.Code, w.Body.String())
	}
}

func TestVersionAffinityMetricUsesOnlyBoundedOutcomes(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveVersionAffinityKey(versionAffinitySurfacePublic, versionAffinityKeyValid)
	metrics.ObserveVersionAffinityKey(versionAffinitySurfacePublic, versionAffinityKeyInvalid)
	metrics.ObserveVersionAffinityKey("customer-controlled-value", versionAffinityKeyValid)
	if got := readCounterLabels(t, metrics, "gateway_version_affinity_key_total", map[string]string{"surface": versionAffinitySurfacePublic, "outcome": versionAffinityKeyValid}); got != 1 {
		t.Fatalf("valid version-key outcomes = %v, want 1", got)
	}
	if got := readCounterLabels(t, metrics, "gateway_version_affinity_key_total", map[string]string{"surface": versionAffinitySurfacePublic, "outcome": versionAffinityKeyInvalid}); got != 1 {
		t.Fatalf("invalid version-key outcomes = %v, want 1", got)
	}
}
