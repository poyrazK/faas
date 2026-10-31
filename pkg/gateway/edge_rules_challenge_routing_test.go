package gateway

// adr: 831 — amendment 17: a challenge solution is the gateway's own
// request, so no routing gate moves or refuses it, and a pass covers its
// host for the account whichever of the account's apps serves a path there.

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// challengeFlowMatcher serves one challenge rule and, optionally, a rewrite
// rule for every path. host says whether HostChallengeRule sees the rule,
// as it does not when the rule is an app-wide rule of another app the
// host's paths route to.
type challengeFlowMatcher struct {
	noOpEdgeRuleMatcher
	challenge *EdgeRuleChallengeResolved
	host      bool
	rewrite   *EdgeRuleRewriteResolved
}

func (m challengeFlowMatcher) MatchChallenge(context.Context, string, string, string) *EdgeRuleChallengeResolved {
	return m.challenge
}

func (m challengeFlowMatcher) HostChallengeRule(context.Context, string) *EdgeRuleChallengeResolved {
	if !m.host {
		return nil
	}
	return m.challenge
}

func (m challengeFlowMatcher) MatchRewrite(context.Context, string, string, string) *EdgeRuleRewriteResolved {
	return m.rewrite
}

// wellKnownUndeclared declares every route but the .well-known ones, like
// an OpenAPI document that lists only the app's own API.
type wellKnownUndeclared struct{}

func (wellKnownUndeclared) MatchDeclaredRoute(_ context.Context, _ App, path, _ string) (bool, error) {
	return !strings.HasPrefix(path, "/.well-known/"), nil
}

var formActionAttr = regexp.MustCompile(`action="([^"]+)"`)

// challengePage returns the token and form action of a challenge page.
func challengePage(t *testing.T, body string) (token, action string) {
	t.Helper()
	tm, am := tokenAttr.FindStringSubmatch(body), formActionAttr.FindStringSubmatch(body)
	if tm == nil || am == nil {
		t.Fatalf("no challenge page: %.300s", body)
	}
	return tm[1], html.UnescapeString(am[1])
}

func flowRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, "http://jane-api.apps.dom"+target, strings.NewReader(body))
	r.RemoteAddr = "198.51.100.23:51234"
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh) Chrome/129.0")
	r.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	if body != "" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return r
}

// TestChallengeSolvePassesRouteGates walks a browser through ServeHTTP on
// apps whose declared routes leave out the solve path, or whose rewrite
// rule moves every path: the page's post must still earn a pass.
func TestChallengeSolvePassesRouteGates(t *testing.T) {
	rewriteAll := &EdgeRuleRewriteResolved{ID: "rewrite-1", AccountID: "acct-1", AppID: "app-1", PathGlob: "/**", To: "/site"}
	for name, tc := range map[string]struct {
		declared bool
		rewrite  *EdgeRuleRewriteResolved
	}{
		"declared routes only":  {declared: true},
		"rewrite of every path": {rewrite: rewriteAll},
		"both":                  {declared: true, rewrite: rewriteAll},
	} {
		t.Run(name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
			h.WithEdgeRules(challengeFlowMatcher{challenge: rule, host: true, rewrite: tc.rewrite}, nil, nil)
			h.WithEdgeChallengeKey(challengeTestKey)
			if tc.declared {
				b.app.OnlyAllowDeclaredRoutes = true
				h.WithDeclaredRouteMatcher(wellKnownUndeclared{})
			}
			serve := func(r *http.Request) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}

			page := serve(flowRequest(http.MethodGet, "/account", ""))
			if page.Code != http.StatusForbidden {
				t.Fatalf("page status = %d, want 403: %s", page.Code, page.Body)
			}
			token, action := challengePage(t, page.Body.String())
			form := url.Values{"token": {token}, "counter": {solveCounter(t, token, rule.Difficulty)}, "return": {"/account"}}
			solved := serve(flowRequest(http.MethodPost, action, form.Encode()))
			if solved.Code != http.StatusSeeOther || solved.Header().Get("Location") != "/account" {
				t.Fatalf("solve status = %d location %q, want 303 to /account: %s", solved.Code, solved.Header().Get("Location"), solved.Body)
			}
			cookies := solved.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != ChallengeCookieName {
				t.Fatalf("pass cookie = %+v", cookies)
			}

			next := flowRequest(http.MethodGet, "/account", "")
			next.AddCookie(cookies[0])
			if got := serve(next); got.Code != http.StatusOK || got.Body.String() != "hello from app" {
				t.Fatalf("with the pass: status %d body %q, want the app", got.Code, got.Body)
			}
		})
	}
}

// TestChallengePassCoversTheHostForTheAccount serves the page for an app a
// path of the host routes to, and the solve where the host's own app serves
// the solve path and loads none of that app's rules. The pass then lets the
// visitor through both apps' rules on the host, and through nothing of
// another account or on another host.
func TestChallengePassCoversTheHostForTheAccount(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	routed := App{ID: "app-b", AccountID: "acct-1"}
	hostApp := App{ID: "app-a", AccountID: "acct-1"}
	rule := &EdgeRuleChallengeResolved{ID: "rule-b", AccountID: "acct-1", AppID: "app-b", Difficulty: 8, PassTTLSeconds: 3600}

	page := httptest.NewRecorder()
	if !challengeTestHandler(rule, now).applyEdgeRuleChallenge(page, browserRequest(http.MethodGet, "/api/orders"), routed, false) {
		t.Fatal("no challenge served")
	}
	token, action := challengePage(t, page.Body.String())
	form := url.Values{"counter": {solveCounter(t, token, rule.Difficulty)}, "return": {"/api/orders"}}
	solving := &Handler{edgeRules: challengeFlowMatcher{challenge: rule}}
	solving.WithEdgeChallengeKey(challengeTestKey)
	solving.challengeNow = func() time.Time { return now }
	post := browserRequestBody(http.MethodPost, action, form.Encode())
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	if !solving.handleChallengeSolve(w, post, hostApp) {
		t.Fatal("the host's app did not take the solve")
	}
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 1 {
		t.Fatalf("solve status = %d cookies %+v: %s", w.Code, w.Result().Cookies(), w.Body)
	}
	pass := w.Result().Cookies()[0]

	for name, tc := range map[string]struct {
		app  App
		host string
		want bool
	}{
		"the routed app":  {routed, "shop.example.test", true},
		"the host's app":  {hostApp, "shop.example.test", true},
		"another account": {App{ID: "app-c", AccountID: "acct-2"}, "shop.example.test", false},
		"another host":    {routed, "blog.example.test", false},
	} {
		t.Run(name, func(t *testing.T) {
			own := *rule
			own.AccountID = tc.app.AccountID
			r := browserRequest(http.MethodGet, "/")
			r.Host = tc.host
			r.AddCookie(pass)
			challenged := challengeTestHandler(&own, now.Add(time.Minute)).applyEdgeRuleChallenge(httptest.NewRecorder(), r, tc.app, false)
			if challenged == tc.want {
				t.Fatalf("challenged = %v, want the pass honoured = %v", challenged, tc.want)
			}
		})
	}
}

// TestChallengeSolveRefusesForeignTokens: a token in the query claims the
// solve path, but earns a pass only on the host it was issued for, while
// the same account owns it.
func TestChallengeSolveRefusesForeignTokens(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
	page := httptest.NewRecorder()
	challengeTestHandler(rule, now).applyEdgeRuleChallenge(page, browserRequest(http.MethodGet, "/"), App{ID: "app-1", AccountID: "acct-1"}, false)
	token, action := challengePage(t, page.Body.String())
	form := url.Values{"counter": {solveCounter(t, token, rule.Difficulty)}, "return": {"/"}}
	for name, tc := range map[string]struct {
		app  App
		host string
	}{
		"another account's app on the host": {App{ID: "app-2", AccountID: "acct-2"}, "shop.example.test"},
		"another host of the account":       {App{ID: "app-1", AccountID: "acct-1"}, "blog.example.test"},
	} {
		t.Run(name, func(t *testing.T) {
			h := &Handler{edgeRules: challengeFlowMatcher{}}
			h.WithEdgeChallengeKey(challengeTestKey)
			h.challengeNow = func() time.Time { return now }
			r := browserRequestBody(http.MethodPost, action, form.Encode())
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Host = tc.host
			w := httptest.NewRecorder()
			if !h.handleChallengeSolve(w, r, tc.app) {
				t.Fatal("a post with a token in its query was not taken")
			}
			if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
				t.Fatalf("status = %d cookies %+v, want 403 and no pass", w.Code, w.Result().Cookies())
			}
		})
	}
}

func TestEdgeChallengeKeyID(t *testing.T) {
	a, b := DeriveEdgeChallengeKey([]byte("fleet secret one")), DeriveEdgeChallengeKey([]byte("fleet secret two"))
	id := EdgeChallengeKeyID(a)
	if len(id) != 12 || strings.Trim(id, "0123456789abcdef") != "" {
		t.Fatalf("key id %q is not 12 hex digits", id)
	}
	if id != EdgeChallengeKeyID(append([]byte(nil), a...)) {
		t.Error("one key, two ids")
	}
	if id == EdgeChallengeKeyID(b) {
		t.Error("two keys, one id")
	}
}
