package gateway

// adr: 831 — amendment 7 follow-up: kind=challenge outcomes become sampled
// security events, and an enforced challenge rule's plain matches do not.

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestApplyEdgeRuleChallengeRecordsOutcomeEvents(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
	app := App{ID: "app-1", AccountID: "acct-1"}
	for _, tc := range []struct {
		name       string
		req        func() *http.Request
		overBudget bool
		want       string
	}{
		{name: "navigation is challenged", req: func() *http.Request { return browserRequest(http.MethodGet, "/account?tab=orders") },
			want: EdgeRuleEventChallenged},
		{name: "fetch is rejected", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/api/cart?id=7")
			r.Header.Set("Sec-Fetch-Mode", "cors")
			return r
		}, want: EdgeRuleEventRejected},
		{name: "a valid pass is not an event", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/account")
			v := sealChallenge(challengeTestKey, "pass", challengePassPrefix, challengePass{AccountID: "acct-1", Host: "shop.example.test", Expires: now.Add(time.Hour).Unix()}, bindingFor(r))
			r.AddCookie(&http.Cookie{Name: ChallengeCookieName, Value: v})
			return r
		}},
		{name: "a verified credential is not an event", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/account")
			return r.WithContext(withAuthenticated(r.Context(), Authenticated{APIKeyID: "key-1"}))
		}},
		{name: "over the per-rule budget", req: func() *http.Request { return browserRequest(http.MethodGet, "/account") }, overBudget: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &wafEventsRecorder{allowDetections: !tc.overBudget}
			h := challengeTestHandler(rule, now)
			h.edgeRuleHits = rec
			h.applyEdgeRuleChallenge(httptest.NewRecorder(), tc.req(), app, false)
			if tc.want == "" {
				if len(rec.events) != 0 {
					t.Fatalf("events = %+v, want none", rec.events)
				}
				return
			}
			if len(rec.events) != 1 {
				t.Fatalf("events = %d, want 1", len(rec.events))
			}
			ev := rec.events[0]
			if ev.Outcome != tc.want || ev.RuleID != "rule-1" || ev.AppID != "app-1" || ev.ClientIP != "198.51.100.23" ||
				ev.Host != "shop.example.test" || ev.UserAgent != "Mozilla/5.0 (Macintosh) Chrome/129.0" || ev.Logged {
				t.Errorf("event = %+v", ev)
			}
			if u, _ := url.Parse(tc.req().URL.String()); ev.Path != u.Path || ev.Method != http.MethodGet {
				t.Errorf("event request = %s %q, want %s without the query string", ev.Method, ev.Path, u.Path)
			}
		})
	}
}

// With the handler's match context the event takes the trusted client IP
// and the country it resolves, like a sampled match.
func TestChallengeEventUsesMatchContext(t *testing.T) {
	rec := &wafEventsRecorder{allowDetections: true}
	r := browserRequest(http.MethodGet, "/account")
	m := NewEdgeRuleMatchContext(r, net.ParseIP("203.0.113.7"), func(net.IP) string { return "DE" }, rec)
	r = r.WithContext(WithEdgeRuleMatchContext(r.Context(), m))
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
	h := challengeTestHandler(rule, time.Unix(1_800_000_000, 0))
	h.edgeRuleHits = rec
	h.applyEdgeRuleChallenge(httptest.NewRecorder(), r, App{ID: "app-1", AccountID: "acct-1"}, false)
	if len(rec.events) != 1 || rec.events[0].ClientIP != "203.0.113.7" || rec.events[0].Country != "DE" || rec.events[0].Outcome != EdgeRuleEventChallenged {
		t.Fatalf("events = %+v", rec.events)
	}
}

func TestChallengeSolveRecordsOutcomeEvents(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	issuer := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 1800}
	// The host's enforced rule is another one: a solve names the rule
	// that issued its token once the token opens.
	hostRule := &EdgeRuleChallengeResolved{ID: "rule-2", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 1800}
	app := App{ID: "app-1", AccountID: "acct-1"}
	page := httptest.NewRecorder()
	challengeTestHandler(issuer, now).applyEdgeRuleChallenge(page, browserRequest(http.MethodGet, "/account"), app, false)
	m := tokenAttr.FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatal("no token in page")
	}
	token := m[1]
	counter := solveCounter(t, token, issuer.Difficulty)
	for _, tc := range []struct {
		name     string
		form     url.Values
		at       time.Time
		want     string
		wantRule string
	}{
		{name: "solved", form: url.Values{"token": {token}, "counter": {counter}, "return": {"/account"}}, at: now,
			want: EdgeRuleEventSolved, wantRule: "rule-1"},
		{name: "work short of the difficulty", form: url.Values{"token": {token}, "counter": {"x1"}}, at: now,
			want: EdgeRuleEventFailed, wantRule: "rule-1"},
		{name: "forged token", form: url.Values{"token": {token + "A"}, "counter": {counter}}, at: now,
			want: EdgeRuleEventFailed, wantRule: "rule-2"},
		{name: "expired token is not a failure", form: url.Values{"token": {token}, "counter": {counter}}, at: now.Add(10 * time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &wafEventsRecorder{allowDetections: true}
			h := challengeTestHandler(hostRule, tc.at)
			h.edgeRuleHits = rec
			r := browserRequestBody(http.MethodPost, ChallengeSolvePath, tc.form.Encode())
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if !h.handleChallengeSolve(httptest.NewRecorder(), r, app) {
				t.Fatal("solve post not handled")
			}
			if tc.want == "" {
				if len(rec.events) != 0 {
					t.Fatalf("events = %+v, want none", rec.events)
				}
				return
			}
			if len(rec.events) != 1 || rec.events[0].Outcome != tc.want || rec.events[0].RuleID != tc.wantRule || rec.events[0].Path != ChallengeSolvePath {
				t.Fatalf("events = %+v, want one %s for %s", rec.events, tc.want, tc.wantRule)
			}
		})
	}
}

// An enforced challenge rule's matches are counted but its events are the
// outcomes; a log-mode rule never acts, so its matches stay "logged" events.
func TestObserveEdgeRuleMatchSamplesOnlyLoggedChallengeMatches(t *testing.T) {
	rec := &wafEventsRecorder{}
	r := httptest.NewRequest(http.MethodGet, "http://shop.example.test/account", nil)
	ctx := WithEdgeRuleMatchContext(context.Background(), NewEdgeRuleMatchContext(r, nil, nil, rec))
	ObserveEdgeRuleMatch(ctx,
		&EdgeRuleChallengeResolved{EdgeRuleCondition: EdgeRuleCondition{RuleID: "enforced", AppID: "app"}},
		&EdgeRuleChallengeResolved{EdgeRuleCondition: EdgeRuleCondition{RuleID: "dry-run", AppID: "app", LogOnly: true}})
	if len(rec.hits) != 2 {
		t.Fatalf("hits = %v, want both rules counted", rec.hits)
	}
	if len(rec.events) != 1 || rec.events[0].RuleID != "dry-run" || !rec.events[0].Logged {
		t.Fatalf("events = %+v, want one logged event for the log-mode rule", rec.events)
	}
}
