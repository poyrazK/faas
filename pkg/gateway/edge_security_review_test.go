package gateway

// adr: 831 — amendment 18: the apps domain is not on the Public Suffix List,
// so a page on another app's host can plant cookies in a visitor's browser.
// The challenge pass cannot be hidden by one, a WAF detection made only of
// cookies never boxes the client, and a CORS preflight on a challenged route
// is answered at the edge instead of refused.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestChallengePassSurvivesAShadowingCookie(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
	app := App{ID: "app-1", AccountID: "acct-1"}
	r := browserRequest(http.MethodGet, "/account")
	pass := sealChallenge(challengeTestKey, "pass", challengePassPrefix,
		challengePass{AccountID: "acct-1", Host: "shop.example.test", Expires: now.Add(time.Hour).Unix()}, bindingFor(r))
	// The browser sends a cookie of the same name first, as it does for one
	// set on a longer path.
	r.AddCookie(&http.Cookie{Name: ChallengeCookieName, Value: "planted"})
	r.AddCookie(&http.Cookie{Name: ChallengeCookieName, Value: pass})
	if challengeTestHandler(rule, now).applyEdgeRuleChallenge(httptest.NewRecorder(), r, app, false) {
		t.Fatal("a cookie sent before the pass hid it")
	}
	if ChallengeCookieName != "__Host-gregale_challenge" {
		t.Fatalf("pass cookie %q lacks the __Host- prefix another host cannot set", ChallengeCookieName)
	}
	legacy := browserRequest(http.MethodGet, "/account")
	legacy.AddCookie(&http.Cookie{Name: "gregale_challenge", Value: sealChallenge(challengeTestKey, "pass", challengePassPrefix,
		challengePass{AccountID: "acct-1", Host: "shop.example.test", Expires: now.Add(time.Hour).Unix()}, bindingFor(legacy))})
	if !challengeTestHandler(rule, now).applyEdgeRuleChallenge(httptest.NewRecorder(), legacy, app, false) {
		t.Fatal("a pass under the unprefixed name, which any host of the domain can set, was honoured")
	}
}

func TestChallengedPreflightIsAnsweredAtTheEdge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
	app := App{ID: "app-1", AccountID: "acct-1"}
	preflight := func(origin, method, headers string) *http.Request {
		r := httptest.NewRequest(http.MethodOptions, "http://shop.example.test/api/orders", nil)
		r.RemoteAddr = "198.51.100.23:51234"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if method != "" {
			r.Header.Set("Access-Control-Request-Method", method)
		}
		if headers != "" {
			r.Header.Set("Access-Control-Request-Headers", headers)
		}
		return r
	}

	w := httptest.NewRecorder()
	if !challengeTestHandler(rule, now).applyEdgeRuleChallenge(w, preflight("https://app.example.org", "POST", "Authorization, Content-Type"), app, false) {
		t.Fatal("preflight not answered")
	}
	h := w.Header()
	if w.Code != http.StatusNoContent || h.Get("Access-Control-Allow-Origin") != "https://app.example.org" ||
		h.Get("Access-Control-Allow-Methods") != "POST" || h.Get("Access-Control-Allow-Headers") != "Authorization, Content-Type" {
		t.Fatalf("preflight answer = %d %v", w.Code, h)
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("preflight answer allows credentials; the pass and cookies must stay same-site")
	}

	for name, r := range map[string]*http.Request{
		"null origin":           preflight("null", "POST", ""),
		"origin with a path":    preflight("https://app.example.org/x", "POST", ""),
		"no requested method":   preflight("https://app.example.org", "", ""),
		"malformed header name": preflight("https://app.example.org", "POST", "x bad"),
		"plain OPTIONS":         preflight("", "", ""),
	} {
		w := httptest.NewRecorder()
		if !challengeTestHandler(rule, now).applyEdgeRuleChallenge(w, r, app, false) {
			t.Fatalf("%s: not challenged", name)
		}
		var p api.Problem
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != http.StatusForbidden || p.Code != api.CodeChallengeRequired {
			t.Fatalf("%s: %d %s, want 403 challenge_required", name, w.Code, w.Body)
		}
	}
}

type wafPenaltyMatcher struct {
	wafRuleMatcher
	penalty api.EdgeRulePenalty
}

func (m wafPenaltyMatcher) RulePenalty(_ context.Context, _, ruleID string) (api.EdgeRulePenalty, bool) {
	return m.penalty, ruleID == m.rule.ID
}

func TestWAFCookieOnlyDetectionBlocksWithoutBoxing(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	rule := &EdgeRuleWAFResolved{ID: "rule-1", AccountID: "acct-1", ParanoiaLevel: 1, AnomalyThreshold: 5, Mode: api.EdgeWAFModeBlock}
	for name, tc := range map[string]struct {
		cookiesOnly bool
		wantBoxed   bool
	}{
		"attack in a cookie": {cookiesOnly: true},
		"attack in the URL":  {wantBoxed: true},
	} {
		t.Run(name, func(t *testing.T) {
			box := NewPenaltyBox()
			inline := &fakeWAFInline{result: WAFInlineResult{Outcome: WAFInlineDetected, RuleIDs: []int{942100}, CookiesOnly: tc.cookiesOnly}}
			h := &Handler{
				edgeRules:  wafPenaltyMatcher{wafRuleMatcher: wafRuleMatcher{rule: rule}, penalty: api.EdgeRulePenalty{Seconds: 600, After: 1, WithinSeconds: 60}},
				wafInline:  inline,
				penaltyBox: box,
			}
			r := httptest.NewRequest(http.MethodGet, "http://shop.example.test/", nil)
			r.RemoteAddr = "198.51.100.23:51234"
			w := httptest.NewRecorder()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: r}
			if handled, _ := h.applyEdgeRuleWAF(rec, r, app, rec); !handled || w.Code != http.StatusForbidden {
				t.Fatalf("handled=%v status=%d, want a 403 block", handled, w.Code)
			}
			if _, _, boxed := box.Boxed(app.ID, challengeClientIP(r)); boxed != tc.wantBoxed {
				t.Fatalf("boxed = %v, want %v", boxed, tc.wantBoxed)
			}
		})
	}
}
