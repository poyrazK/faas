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

// beginWAF runs the gate the way ServeHTTP does and returns the sample
// submitter, failing the test if the gate answered the request itself.
func beginWAF(t *testing.T, h *Handler, r *http.Request, app App) func() {
	t.Helper()
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK, request: r}
	handled, submit := h.applyEdgeRuleWAF(rec, r, app, rec)
	if handled {
		t.Fatal("gate answered the request")
	}
	return submit
}

func TestBeginEdgeRuleWAF(t *testing.T) {
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 2, AnomalyThreshold: 7, ExcludeRuleIDs: []int{920350}}
	app := App{ID: "app-1", AccountID: "acct-1"}
	long := strings.Repeat("x", api.EdgeWAFDefaultInspectBodyBytes+100)
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
		{name: "truncates at sample size", rule: rule, app: app, body: long, wantSample: true, wantBody: long[:api.EdgeWAFDefaultInspectBodyBytes], wantTruncated: true},
		{name: "rule body cap", rule: &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 2, AnomalyThreshold: 7, ExcludeRuleIDs: []int{920350}, InspectBodyBytes: 16},
			app: app, body: long, wantSample: true, wantBody: long[:16], wantTruncated: true},
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
			submit := beginWAF(t, h, r, tc.app)
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
	if submit := beginWAF(t, h, r, App{ID: "app", AccountID: "a"}); submit != nil {
		t.Fatal("gate armed without an inspector")
	}
}

type fakeWAFInline struct {
	result WAFInlineResult
	calls  int
}

func (f *fakeWAFInline) CheckInline(s WAFSample) WAFInlineResult {
	f.calls++
	return f.result
}

func TestApplyEdgeRuleWAFInline(t *testing.T) {
	detected := WAFInlineResult{Outcome: WAFInlineDetected, RuleIDs: []int{913100}, Categories: []string{"scanner"}, Seconds: 0.001}
	for _, tc := range []struct {
		name        string
		mode        string
		verdict     WAFInlineResult
		wantChecks  int
		wantHandled bool
		wantStatus  int
		wantWarning bool
		wantSample  bool
	}{
		{name: "block detected", mode: "block", verdict: detected, wantChecks: 1, wantHandled: true, wantStatus: http.StatusForbidden},
		{name: "warn detected", mode: "warn", verdict: detected, wantChecks: 1, wantStatus: http.StatusOK, wantWarning: true},
		{name: "block clean", mode: "block", verdict: WAFInlineResult{Outcome: WAFInlineClean}, wantChecks: 1, wantStatus: http.StatusOK, wantSample: true},
		{name: "block skipped fails open", mode: "block", verdict: WAFInlineResult{Outcome: WAFInlineSkipped}, wantChecks: 1, wantStatus: http.StatusOK, wantSample: true},
		{name: "block error fails open", mode: "block", verdict: WAFInlineResult{Outcome: WAFInlineError}, wantChecks: 1, wantStatus: http.StatusOK, wantSample: true},
		{name: "observe never checks in-path", mode: "observe", verdict: detected, wantChecks: 0, wantStatus: http.StatusOK, wantSample: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inline := &fakeWAFInline{result: tc.verdict}
			rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: tc.mode}
			h := &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInspector: &recordingWAFInspector{}, wafInline: inline}
			r := httptest.NewRequest(http.MethodGet, "http://app.example.test/admin/", nil)
			w := httptest.NewRecorder()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
			handled, submit := h.applyEdgeRuleWAF(rec, r, App{ID: "app-1", AccountID: "acct-1"}, rec)
			if !handled {
				rec.WriteHeader(http.StatusOK)
			}
			if inline.calls != tc.wantChecks {
				t.Errorf("in-path checks = %d, want %d", inline.calls, tc.wantChecks)
			}
			if handled != tc.wantHandled || w.Code != tc.wantStatus {
				t.Errorf("handled=%v status=%d, want handled=%v status=%d", handled, w.Code, tc.wantHandled, tc.wantStatus)
			}
			if got := w.Header().Get("X-WAF-Warning"); (got == "rule-1") != tc.wantWarning {
				t.Errorf("X-WAF-Warning = %q, want present=%v", got, tc.wantWarning)
			}
			if (submit != nil) != tc.wantSample {
				t.Errorf("sample armed = %v, want %v", submit != nil, tc.wantSample)
			}
			if tc.wantHandled && !strings.Contains(w.Body.String(), "rule-1") {
				t.Errorf("403 body does not name the edge rule: %s", w.Body.String())
			}
		})
	}
}
