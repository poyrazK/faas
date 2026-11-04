package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A client can add Connection: Upgrade to any request, and servers that do
// not speak the named protocol run it as usual. A body sent that way is still
// validated; only a body-less handshake skips the schema (ADR-20261103090000).
func TestValidateRuleChecksUpgradeRequestsWithABody(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		wantValidated      bool
	}{
		{name: "POST with a body and Upgrade headers", method: http.MethodPost, body: `{"name": 123}`, wantValidated: true},
		{name: "WebSocket handshake", method: http.MethodGet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := &EdgeRuleValidateResolved{ID: "rule-v", AccountID: "acct-1", AppID: "app-1", ValidateMode: api.ValidateModeBlock}
			val := &stubValidatorApply{}
			h := &Handler{edgeRules: stubEdgeRuleMatcher{validate: rule}, validator: val, metrics: NewMetrics()}
			var body *strings.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			var r *http.Request
			if body != nil {
				r = httptest.NewRequest(tc.method, "http://h.example.com/api/x", body)
				r.Header.Set("Content-Type", "application/json")
			} else {
				r = httptest.NewRequest(tc.method, "http://h.example.com/api/x", nil)
			}
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			rec := httptest.NewRecorder()
			blocked := h.applyEdgeRuleValidate(rec, r, App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}, &statusRecorder{ResponseWriter: rec})
			if got := val.calls == 1; got != tc.wantValidated {
				t.Fatalf("validated = %v, want %v", got, tc.wantValidated)
			}
			if blocked != tc.wantValidated {
				t.Fatalf("blocked = %v, want %v (the stub validator rejects every body)", blocked, tc.wantValidated)
			}
		})
	}
}

// Keys in X-API-Key or Proxy-Authorization are not part of the cache key, so
// such requests bypass the shared cache like Authorization does.
func TestCacheCredentialHeaders(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"Authorization", true},
		{"Proxy-Authorization", true},
		{"X-API-Key", true},
		{"Cookie", true},
		{"Accept-Language", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://h.example.com/catalog", nil)
		r.Header.Set(tc.header, "v")
		if got := hasCacheCredentials(r); got != tc.want {
			t.Errorf("%s: hasCacheCredentials = %v, want %v", tc.header, got, tc.want)
		}
	}
}
