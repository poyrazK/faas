// spec: §4.1

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestCORSResponsesVaryOnOrigin pins H9-130: the gateway stamps
// Access-Control-Allow-Origin only for an allowed origin and omits it when a
// request has no Origin, but never said so with Vary: Origin. A
// Cache-Control: public response stored by a browser or shared cache on a
// direct load was then replayed to a cross-origin fetch without the header,
// and the browser blocked it.
func TestCORSResponsesVaryOnOrigin(t *testing.T) {
	enabled := true
	rule := &EdgeRuleCORSResolved{
		ID: "rule-cors", AccountID: "acct-1", AppID: "app-1",
		AllowOrigins: []string{"https://app.example.com"}, AllowMethods: []string{"GET", "POST"},
	}
	ruleApp := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}
	defaultApp := App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro,
		CORSDefaultEnabled: &enabled, CORSDefaultOrigins: []string{"https://app.example.com"}}
	cases := []struct {
		name      string
		matcher   stubEdgeRuleMatcher
		app       App
		origin    string
		appVary   []string
		wantVary  []string
		wantAllow string
	}{
		{name: "rule, allowed origin", matcher: stubEdgeRuleMatcher{cors: rule}, app: ruleApp,
			origin: "https://app.example.com", wantVary: []string{"Origin"}, wantAllow: "https://app.example.com"},
		{name: "rule, other origin", matcher: stubEdgeRuleMatcher{cors: rule}, app: ruleApp,
			origin: "https://evil.example.com", wantVary: []string{"Origin"}},
		{name: "rule, no origin", matcher: stubEdgeRuleMatcher{cors: rule}, app: ruleApp,
			wantVary: []string{"Origin"}},
		{name: "rule, app already varies on origin", matcher: stubEdgeRuleMatcher{cors: rule}, app: ruleApp,
			origin: "https://app.example.com", appVary: []string{"Accept-Encoding, origin"},
			wantVary: []string{"Accept-Encoding, origin"}, wantAllow: "https://app.example.com"},
		{name: "rule, app varies on something else", matcher: stubEdgeRuleMatcher{cors: rule}, app: ruleApp,
			appVary: []string{"Accept-Language"}, wantVary: []string{"Accept-Language", "Origin"}},
		{name: "app default, no origin", app: defaultApp, wantVary: []string{"Origin"}},
		{name: "app default, allowed origin", app: defaultApp,
			origin: "https://app.example.com", wantVary: []string{"Origin"}, wantAllow: "https://app.example.com"},
		{name: "no CORS configured", app: ruleApp, origin: "https://app.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{edgeRules: tc.matcher, edgeRuleAudit: &countingAudit{}, metrics: NewMetrics()}
			rec := httptest.NewRecorder()
			srec := &statusRecorder{ResponseWriter: rec}
			r := httptest.NewRequest(http.MethodGet, "http://h.example.com/cacheable", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if h.applyEdgeRuleCORS(rec, r, tc.app, srec) {
				t.Fatal("CORS short-circuited a GET")
			}
			for _, v := range tc.appVary {
				srec.Header().Add("Vary", v)
			}
			srec.Header().Set("Cache-Control", "public, max-age=30")
			srec.WriteHeader(http.StatusOK)

			if got := rec.Header().Values("Vary"); !equalStrings(got, tc.wantVary) {
				t.Errorf("Vary = %q, want %q", got, tc.wantVary)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tc.wantAllow {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantAllow)
			}
		})
	}
}

// TestCORSVaryKeepsPlatformCacheStoring guards the other direction: the
// response cache refuses responses whose Vary names a header outside its key,
// so the platform's own Vary: Origin must not make CORS routes uncacheable.
func TestCORSVaryKeepsPlatformCacheStoring(t *testing.T) {
	h := &Handler{
		edgeRules: stubEdgeRuleMatcher{cors: &EdgeRuleCORSResolved{
			ID: "rule-cors", AccountID: "acct-1", AppID: "app-1",
			AllowOrigins: []string{"https://app.example.com"},
		}},
		edgeRuleAudit: &countingAudit{},
		metrics:       NewMetrics(),
	}
	rec := httptest.NewRecorder()
	srec := &statusRecorder{ResponseWriter: rec}
	r := httptest.NewRequest(http.MethodGet, "http://h.example.com/cacheable", nil)
	r.Header.Set("Origin", "https://app.example.com")
	h.applyEdgeRuleCORS(rec, r, App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, srec)

	cw := newCacheWriter(srec, srec, &EdgeRuleCacheResolved{ID: "rule-cache", MaxAgeSeconds: 60}, ResponseCachePerEntryMaxBytes)
	cw.Header().Set("Cache-Control", "public, max-age=30")
	cw.WriteHeader(http.StatusOK)
	_, _ = cw.Write([]byte(`{"ok":true}`))

	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("live response Vary = %q, want Origin", got)
	}
	if !cw.shouldStore() {
		t.Fatalf("CORS route response not stored; captured headers %v", cw.header)
	}
	if got := cw.header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("stored entry carries one origin's grant %q; CORS must be re-applied per hit", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
