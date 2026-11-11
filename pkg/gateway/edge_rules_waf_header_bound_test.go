package gateway

import (
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
