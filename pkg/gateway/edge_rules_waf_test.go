package gateway

// adr: 831 — kind=waf is observe-only: the gate never writes a response and
// never reads the body itself; it records the prefix the proxy reads.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type wafRuleMatcher struct {
	noOpEdgeRuleMatcher
	rule *EdgeRuleWAFResolved
}

func (m wafRuleMatcher) MatchWAF(context.Context, string, string, string) *EdgeRuleWAFResolved {
	return m.rule
}

type recordingWAFInspector struct{ samples []WAFSample }

func (r *recordingWAFInspector) Submit(s WAFSample) { r.samples = append(r.samples, s) }

func TestPickFirstWAFMatch(t *testing.T) {
	rules := []EdgeRuleWAFResolved{
		{ID: "api-post", Priority: 10, PathGlob: "/api/*", Methods: map[string]bool{"POST": true}},
		{ID: "all", Priority: 20, PathGlob: "/*"},
	}
	for _, tc := range []struct {
		path, method, want string
	}{
		{"/api/items", http.MethodPost, "api-post"},
		{"/api/items", http.MethodGet, "all"},
		{"/login", http.MethodPost, "all"},
	} {
		got := PickFirstWAFMatch(rules, tc.path, tc.method)
		if got == nil || got.ID != tc.want {
			t.Errorf("%s %s matched %+v, want %s", tc.method, tc.path, got, tc.want)
		}
	}
	if got := PickFirstWAFMatch(rules[:1], "/other", http.MethodPost); got != nil {
		t.Errorf("unmatched path returned %+v", got)
	}
}

func TestBeginEdgeRuleWAF(t *testing.T) {
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 2, AnomalyThreshold: 7, ExcludeRuleIDs: []int{920350}}
	app := App{ID: "app-1", AccountID: "acct-1"}
	long := strings.Repeat("x", api.EdgeWAFInspectBodyBytes+100)
	for _, tc := range []struct {
		name          string
		rule          *EdgeRuleWAFResolved
		app           App
		body          string
		upgrade       bool
		wantSample    bool
		wantBody      string
		wantTruncated bool
	}{
		{name: "records body read downstream", rule: rule, app: app, body: `{"q":"1' OR 1=1--"}`, wantSample: true, wantBody: `{"q":"1' OR 1=1--"}`},
		{name: "truncates at sample size", rule: rule, app: app, body: long, wantSample: true, wantBody: long[:api.EdgeWAFInspectBodyBytes], wantTruncated: true},
		{name: "no rule", rule: nil, app: app, body: "x"},
		{name: "foreign account rule", rule: rule, app: App{ID: "app-2", AccountID: "acct-2"}, body: "x"},
		{name: "upgrade skips body", rule: rule, app: app, body: "x", upgrade: true, wantSample: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspector := &recordingWAFInspector{}
			h := &Handler{edgeRules: wafRuleMatcher{rule: tc.rule}, wafInspector: inspector}
			r := httptest.NewRequest(http.MethodPost, "http://app.example.test/api/items?debug=1", strings.NewReader(tc.body))
			r.Header.Set("User-Agent", "sqlmap/1.7")
			if tc.upgrade {
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
			}
			submit := h.beginEdgeRuleWAF(r, tc.app)
			if !tc.wantSample {
				if submit != nil {
					t.Fatal("gate armed a sample, want none")
				}
				return
			}
			if submit == nil {
				t.Fatal("gate did not arm a sample")
			}
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Fatalf("downstream body read: %v", err)
			}
			submit()
			if len(inspector.samples) != 1 {
				t.Fatalf("got %d samples, want 1", len(inspector.samples))
			}
			s := inspector.samples[0]
			if s.AppID != "app-1" || s.RuleID != "rule-1" || s.ParanoiaLevel != 2 || s.AnomalyThreshold != 7 || len(s.ExcludeRuleIDs) != 1 {
				t.Errorf("sample identity/scoring = %+v", s)
			}
			if s.URI != "/api/items?debug=1" || s.Method != http.MethodPost || s.Header.Get("User-Agent") != "sqlmap/1.7" {
				t.Errorf("sample request = %s %s UA=%q", s.Method, s.URI, s.Header.Get("User-Agent"))
			}
			if string(s.Body) != tc.wantBody || s.BodyTruncated != tc.wantTruncated {
				t.Errorf("sample body len=%d truncated=%v, want len=%d truncated=%v", len(s.Body), s.BodyTruncated, len(tc.wantBody), tc.wantTruncated)
			}
		})
	}
}

func TestBeginEdgeRuleWAFDisabledWithoutInspector(t *testing.T) {
	h := &Handler{edgeRules: wafRuleMatcher{rule: &EdgeRuleWAFResolved{ID: "r", AccountID: "a"}}}
	r := httptest.NewRequest(http.MethodGet, "http://app.example.test/", nil)
	if submit := h.beginEdgeRuleWAF(r, App{ID: "app", AccountID: "a"}); submit != nil {
		t.Fatal("gate armed without an inspector")
	}
}
