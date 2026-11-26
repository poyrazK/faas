package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-831 amendment 20: headers a check would only partly score count as a
// detection for warn and block rules, so padding cannot hide a payload; the
// client is not boxed, and in-bound headers still get a real check.
func TestWAFHeadersPastTheInspectionBoundAreADetection(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeBlock}
	pad := strings.Repeat("a", api.EdgeWAFMaxInspectHeaderValueBytes)
	for name, tc := range map[string]struct {
		headers   map[string]string
		wantBlock bool
	}{
		"one value past the value bound": {headers: map[string]string{"User-Agent": pad + "x"}, wantBlock: true},
		"headers past the total bound":   {headers: map[string]string{"A1": pad, "A2": pad, "A3": pad, "A4": pad, "X-Payload": "x"}, wantBlock: true},
		"headers within both bounds":     {headers: map[string]string{"User-Agent": "curl/8", "Cookie": pad[:3000]}},
		// A browser joins every cookie into one header; each is bounded alone.
		"a long Cookie header of normal cookies": {headers: map[string]string{"Cookie": "a=" + pad[:3000] + "; b=" + pad[:3000]}},
		"one cookie past the value bound":        {headers: map[string]string{"Cookie": "a=1; big=" + pad}, wantBlock: true},
	} {
		t.Run(name, func(t *testing.T) {
			box := NewPenaltyBox()
			inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
			h := &Handler{
				edgeRules:  wafPenaltyMatcher{wafRuleMatcher: wafRuleMatcher{rule: rule}, penalty: api.EdgeRulePenalty{Seconds: 600, After: 1, WithinSeconds: 60}},
				wafInline:  inline,
				penaltyBox: box,
			}
			r := httptest.NewRequest(http.MethodGet, "http://shop.example.test/", nil)
			r.RemoteAddr = "198.51.100.23:51234"
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
			handled, _ := h.applyEdgeRuleWAF(rec, r, app, rec)
			if handled != tc.wantBlock || (tc.wantBlock && w.Code != http.StatusForbidden) {
				t.Fatalf("handled=%v status=%d, want block=%v", handled, w.Code, tc.wantBlock)
			}
			if tc.wantBlock && inline.calls != 0 {
				t.Fatalf("an over-bound request was still sent to the check (%d calls)", inline.calls)
			}
			if !tc.wantBlock && inline.calls != 1 {
				t.Fatalf("an in-bound request skipped the check (%d calls)", inline.calls)
			}
			if _, _, boxed := box.Boxed(app.ID, challengeClientIP(r)); boxed {
				t.Fatal("an over-bound request boxed the client")
			}
		})
	}
}

// ADR-831 amendment 21: a URI longer than a check scores is a detection for
// warn and block rules, without running the check, so long queries can
// neither hide a payload nor empty the app's check budget; the client is not
// boxed.
func TestWAFURIPastTheInspectionBoundIsADetection(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeBlock}
	base := "/search?q="
	for name, tc := range map[string]struct {
		uri       string
		wantBlock bool
	}{
		"a URI at the bound":   {uri: base + strings.Repeat("a", api.EdgeWAFMaxInspectURIBytes-len(base))},
		"a URI past the bound": {uri: base + strings.Repeat("a", api.EdgeWAFMaxInspectURIBytes), wantBlock: true},
	} {
		t.Run(name, func(t *testing.T) {
			box := NewPenaltyBox()
			inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}
			h := &Handler{
				edgeRules:  wafPenaltyMatcher{wafRuleMatcher: wafRuleMatcher{rule: rule}, penalty: api.EdgeRulePenalty{Seconds: 600, After: 1, WithinSeconds: 60}},
				wafInline:  inline,
				penaltyBox: box,
			}
			r := httptest.NewRequest(http.MethodGet, "http://shop.example.test"+tc.uri, nil)
			r.RemoteAddr = "198.51.100.23:51234"
			w := httptest.NewRecorder()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
			handled, _ := h.applyEdgeRuleWAF(rec, r, app, rec)
			if handled != tc.wantBlock || (tc.wantBlock && w.Code != http.StatusForbidden) {
				t.Fatalf("handled=%v status=%d, want block=%v", handled, w.Code, tc.wantBlock)
			}
			wantCalls := 1
			if tc.wantBlock {
				wantCalls = 0
			}
			if inline.calls != wantCalls {
				t.Fatalf("check ran %d times, want %d", inline.calls, wantCalls)
			}
			if _, _, boxed := box.Boxed(app.ID, challengeClientIP(r)); boxed {
				t.Fatal("an over-bound URI boxed the client")
			}
		})
	}
}

// ADR-831 amendment 21: under warn, an oversized request still reaches the
// app, so it is sampled: padding the query cannot hide a body payload from
// the report.
func TestWAFWarnOversizedRequestIsStillSampled(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeWarn}
	inspector := &recordingWAFInspector{}
	h := &Handler{edgeRules: wafRuleMatcher{rule: rule}, wafInspector: inspector,
		wafInline: &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineClean}}}
	uri := "/api/comments?pad=" + strings.Repeat("a", api.EdgeWAFMaxInspectURIBytes)
	r := httptest.NewRequest(http.MethodPost, "http://shop.example.test"+uri, strings.NewReader(`{"q":"' OR 1=1--"}`))
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
	handled, submit := h.applyEdgeRuleWAF(rec, r, app, rec)
	if handled || submit == nil {
		t.Fatalf("handled=%v submit=%v, want the request passed and sampled", handled, submit != nil)
	}
	_, _ = io.ReadAll(r.Body)
	submit()
	if len(inspector.samples) != 1 || !strings.Contains(string(inspector.samples[0].Body), "OR 1=1") {
		t.Fatalf("samples = %d, want one carrying the body", len(inspector.samples))
	}
	// Without a body there is nothing more to find, so it is not sampled.
	get := httptest.NewRequest(http.MethodGet, "http://shop.example.test"+uri, nil)
	getRec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK, request: get}
	if handled, submit := h.applyEdgeRuleWAF(getRec, get, app, getRec); handled || submit != nil {
		t.Fatalf("bodyless oversized request: handled=%v sampled=%v, want passed and not sampled", handled, submit != nil)
	}
}
