package gateway

// adr: 831 — amendment 7: kind=challenge serves a proof-of-work page to
// browser navigations without a pass, 403 challenge_required to everything
// else, and never challenges a verified credential.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type challengeRuleMatcher struct {
	noOpEdgeRuleMatcher
	rule *EdgeRuleChallengeResolved
}

func (m challengeRuleMatcher) MatchChallenge(context.Context, string, string, string) *EdgeRuleChallengeResolved {
	return m.rule
}

func (m challengeRuleMatcher) HostChallengeRule(context.Context, string) *EdgeRuleChallengeResolved {
	return m.rule
}

var challengeTestKey = DeriveEdgeChallengeKey([]byte("0123456789abcdef0123456789abcdef"))

func challengeTestHandler(rule *EdgeRuleChallengeResolved, now time.Time) *Handler {
	h := &Handler{edgeRules: challengeRuleMatcher{rule: rule}}
	h.WithEdgeChallengeKey(challengeTestKey)
	h.challengeNow = func() time.Time { return now }
	return h
}

func browserRequest(method, target string) *http.Request {
	return browserRequestBody(method, target, "")
}

func browserRequestBody(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = "shop.example.test"
	r.RemoteAddr = "198.51.100.23:51234"
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh) Chrome/129.0")
	r.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	return r
}

// solveCounter brute-forces the work the browser does.
func solveCounter(t *testing.T, token string, difficulty int) string {
	t.Helper()
	for n := 0; n < 1<<24; n++ {
		if c := strconv.Itoa(n); challengeWorkValid(token, c, difficulty) {
			return c
		}
	}
	t.Fatal("no counter found")
	return ""
}

var tokenAttr = regexp.MustCompile(`data-token="([^"]+)"`)

func TestApplyEdgeRuleChallenge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 3600}
	app := App{ID: "app-1", AccountID: "acct-1"}
	validPassCookie := func(r *http.Request, appID string) {
		v := sealChallenge(challengeTestKey, "pass", challengePassPrefix, challengePass{AppID: appID, Expires: now.Add(time.Hour).Unix()}, bindingFor(r))
		r.AddCookie(&http.Cookie{Name: ChallengeCookieName, Value: v})
	}
	for _, tc := range []struct {
		name        string
		req         func() *http.Request
		smoke       bool
		wantHandled bool
		wantPage    bool
		wantCode    string
	}{
		{name: "navigation without pass gets the page", req: func() *http.Request { return browserRequest(http.MethodGet, "/account?tab=orders") },
			wantHandled: true, wantPage: true},
		{name: "head navigation gets the page headers", req: func() *http.Request { return browserRequest(http.MethodHead, "/") },
			wantHandled: true},
		{name: "fetch without pass is refused", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/api/cart")
			r.Header.Set("Sec-Fetch-Mode", "cors")
			return r
		}, wantHandled: true, wantCode: api.CodeChallengeRequired},
		{name: "non-HTML accept is no bypass", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/")
			r.Header.Del("Sec-Fetch-Mode")
			r.Header.Set("Accept", "application/json")
			return r
		}, wantHandled: true, wantCode: api.CodeChallengeRequired},
		{name: "post without pass is refused", req: func() *http.Request { return browserRequest(http.MethodPost, "/checkout") },
			wantHandled: true, wantCode: api.CodeChallengeRequired},
		{name: "valid pass passes", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/")
			validPassCookie(r, "app-1")
			return r
		}},
		{name: "pass for another app is challenged", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/")
			validPassCookie(r, "app-2")
			return r
		}, wantHandled: true, wantPage: true},
		{name: "pass from another browser is challenged", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/")
			validPassCookie(r, "app-1")
			r.Header.Set("User-Agent", "python-requests/2.32")
			return r
		}, wantHandled: true, wantPage: true},
		{name: "verified JWT bypasses", req: func() *http.Request {
			r := browserRequest(http.MethodGet, "/")
			return r.WithContext(withAuthenticated(r.Context(), Authenticated{JWTSubject: "user-1"}))
		}},
		{name: "verified consumer key bypasses", req: func() *http.Request {
			r := browserRequest(http.MethodPost, "/api/orders")
			return r.WithContext(withAuthenticated(r.Context(), Authenticated{ConsumerID: "cons-1"}))
		}},
		{name: "deployment smoke bypasses", req: func() *http.Request { return browserRequest(http.MethodGet, "/") }, smoke: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			got := challengeTestHandler(rule, now).applyEdgeRuleChallenge(w, tc.req(), app, tc.smoke)
			if got != tc.wantHandled {
				t.Fatalf("handled = %v, want %v", got, tc.wantHandled)
			}
			if !got {
				return
			}
			if w.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", w.Code)
			}
			body := w.Body.String()
			if tc.wantPage != tokenAttr.MatchString(body) {
				t.Errorf("challenge page served = %v, want %v: %.200s", !tc.wantPage, tc.wantPage, body)
			}
			if tc.wantCode != "" {
				var p api.Problem
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.Code != tc.wantCode {
					t.Errorf("problem code = %q (err %v), want %q", p.Code, err, tc.wantCode)
				}
			}
			if tc.wantPage {
				if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
					t.Errorf("CSP = %q", csp)
				}
				if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
					t.Errorf("Cache-Control = %q", cc)
				}
			}
		})
	}
}

func TestApplyEdgeRuleChallengeInactive(t *testing.T) {
	r := browserRequest(http.MethodGet, "/")
	app := App{ID: "app-1", AccountID: "acct-1"}
	foreign := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-other", Difficulty: 8, PassTTLSeconds: 3600}
	for name, h := range map[string]*Handler{
		"no key":            {edgeRules: challengeRuleMatcher{rule: &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", Difficulty: 8}}},
		"no rule":           challengeTestHandler(nil, time.Now()),
		"another account":   challengeTestHandler(foreign, time.Now()),
		"matcher not wired": (&Handler{edgeRules: noOpEdgeRuleMatcher{}}).WithEdgeChallengeKey(challengeTestKey),
	} {
		if h.applyEdgeRuleChallenge(httptest.NewRecorder(), r, app, false) {
			t.Errorf("%s: challenged", name)
		}
	}
}

// TestChallengeSolveRoundTrip walks a browser through the page, the
// solution post and the pass, and checks each way the post can fail.
func TestChallengeSolveRoundTrip(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", AppID: "app-1", Difficulty: 8, PassTTLSeconds: 1800}
	app := App{ID: "app-1", AccountID: "acct-1"}
	h := challengeTestHandler(rule, now)

	page := httptest.NewRecorder()
	if !h.applyEdgeRuleChallenge(page, browserRequest(http.MethodGet, "/account?tab=orders"), app, false) {
		t.Fatal("no challenge served")
	}
	m := tokenAttr.FindStringSubmatch(page.Body.String())
	if m == nil {
		t.Fatalf("no token in page: %.300s", page.Body.String())
	}
	token := m[1]
	counter := solveCounter(t, token, rule.Difficulty)
	post := func(form url.Values, mutate func(*http.Request), at time.Time) *httptest.ResponseRecorder {
		r := browserRequestBody(http.MethodPost, ChallengeSolvePath, form.Encode())
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if mutate != nil {
			mutate(r)
		}
		w := httptest.NewRecorder()
		hh := challengeTestHandler(rule, at)
		if !hh.handleChallengeSolve(w, r, app) {
			t.Fatal("solve post not handled")
		}
		return w
	}
	form := url.Values{"token": {token}, "counter": {counter}, "return": {"/account?tab=orders"}}

	w := post(form, nil, now.Add(time.Minute))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/account?tab=orders" {
		t.Fatalf("solve: status %d location %q", w.Code, w.Header().Get("Location"))
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != ChallengeCookieName || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].MaxAge != 1800 {
		t.Fatalf("pass cookie = %+v", cookies)
	}
	next := browserRequest(http.MethodGet, "/account?tab=orders")
	next.AddCookie(cookies[0])
	if h.applyEdgeRuleChallenge(httptest.NewRecorder(), next, app, false) {
		t.Fatal("request with the new pass was challenged")
	}
	later := browserRequest(http.MethodGet, "/")
	later.AddCookie(cookies[0])
	if !challengeTestHandler(rule, now.Add(32*time.Minute)).applyEdgeRuleChallenge(httptest.NewRecorder(), later, app, false) {
		t.Fatal("expired pass was honoured")
	}

	for _, tc := range []struct {
		name     string
		form     url.Values
		mutate   func(*http.Request)
		at       time.Time
		wantCode int
	}{
		{name: "wrong counter", form: url.Values{"token": {token}, "counter": {"x1"}}, at: now, wantCode: http.StatusForbidden},
		{name: "tampered token", form: url.Values{"token": {token + "A"}, "counter": {counter}}, at: now, wantCode: http.StatusForbidden},
		{name: "other user agent", form: form, at: now, wantCode: http.StatusForbidden,
			mutate: func(r *http.Request) { r.Header.Set("User-Agent", "curl/8.5") }},
		{name: "other network", form: form, at: now, wantCode: http.StatusForbidden,
			mutate: func(r *http.Request) { r.RemoteAddr = "203.0.113.9:4000" }},
		{name: "expired token goes back for a fresh challenge", form: form, at: now.Add(10 * time.Minute), wantCode: http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := post(tc.form, tc.mutate, tc.at)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.wantCode, w.Body.String())
			}
			if len(w.Result().Cookies()) != 0 {
				t.Error("a failed solve set a pass")
			}
		})
	}
}

func TestChallengeSolveIgnoresOtherRequests(t *testing.T) {
	app := App{ID: "app-1", AccountID: "acct-1"}
	rule := &EdgeRuleChallengeResolved{ID: "rule-1", AccountID: "acct-1", Difficulty: 8, PassTTLSeconds: 3600}
	for name, tc := range map[string]struct {
		h      *Handler
		method string
		path   string
	}{
		"get on the path":       {challengeTestHandler(rule, time.Now()), http.MethodGet, ChallengeSolvePath},
		"post elsewhere":        {challengeTestHandler(rule, time.Now()), http.MethodPost, "/login"},
		"host without the rule": {challengeTestHandler(nil, time.Now()), http.MethodPost, ChallengeSolvePath},
	} {
		if tc.h.handleChallengeSolve(httptest.NewRecorder(), browserRequest(tc.method, tc.path), app) {
			t.Errorf("%s: handled", name)
		}
	}
}

func TestChallengeSealing(t *testing.T) {
	r := browserRequest(http.MethodGet, "/")
	b := bindingFor(r)
	sealed := sealChallenge(challengeTestKey, "pass", challengePassPrefix, challengePass{AppID: "app-1", Expires: 42}, b)
	var p challengePass
	if !openChallenge(challengeTestKey, "pass", challengePassPrefix, sealed, b, &p) || p.AppID != "app-1" || p.Expires != 42 {
		t.Fatalf("round trip: %+v", p)
	}
	if openChallenge(challengeTestKey, "token", challengePassPrefix, sealed, b, &p) {
		t.Error("a pass opened as a token")
	}
	if openChallenge(DeriveEdgeChallengeKey([]byte("another secret")), "pass", challengePassPrefix, sealed, b, &p) {
		t.Error("opened with another key")
	}
	r2 := browserRequest(http.MethodGet, "/")
	r2.RemoteAddr = "198.51.100.200:1" // same /24
	if !openChallenge(challengeTestKey, "pass", challengePassPrefix, sealed, bindingFor(r2), &p) {
		t.Error("same /24 should keep the pass")
	}
	r2.RemoteAddr = "198.51.101.23:1"
	if openChallenge(challengeTestKey, "pass", challengePassPrefix, sealed, bindingFor(r2), &p) {
		t.Error("another /24 opened the pass")
	}
	r3 := browserRequest(http.MethodGet, "/")
	r3.Header.Set("X-Forwarded-For", "2001:db8:1:2::7")
	r4 := browserRequest(http.MethodGet, "/")
	r4.Header.Set("X-Forwarded-For", "2001:db8:1:ffff::9")
	if string(bindingFor(r3).prefix) != string(bindingFor(r4).prefix) {
		t.Error("addresses in one IPv6 /48 bound differently")
	}
}

func TestChallengeWorkValid(t *testing.T) {
	counter := solveCounter(t, "c1.token", 10)
	sum := sha256.Sum256([]byte("c1.token:" + counter))
	if sum[0] != 0 || sum[1]&0xc0 != 0 {
		t.Fatalf("counter %s does not give 10 zero bits: %x", counter, sum[:2])
	}
	for _, bad := range []string{"", "-1", "1e3", "12a", strings.Repeat("9", 21)} {
		if challengeWorkValid("c1.token", bad, 1) {
			t.Errorf("counter %q accepted", bad)
		}
	}
}

func TestIsBrowserNavigation(t *testing.T) {
	for _, tc := range []struct {
		method, mode, accept string
		want                 bool
	}{
		{"GET", "navigate", "text/html", true},
		{"GET", "", "text/html,application/xhtml+xml", true},
		{"HEAD", "", "text/html", true},
		{"GET", "cors", "text/html", false},
		{"GET", "no-cors", "*/*", false},
		{"GET", "", "*/*", false},
		{"GET", "", "application/json", false},
		{"POST", "navigate", "text/html", false},
	} {
		r := httptest.NewRequest(tc.method, "/", nil)
		if tc.mode != "" {
			r.Header.Set("Sec-Fetch-Mode", tc.mode)
		}
		r.Header.Set("Accept", tc.accept)
		if got := isBrowserNavigation(r); got != tc.want {
			t.Errorf("%s mode=%q accept=%q: %v, want %v", tc.method, tc.mode, tc.accept, got, tc.want)
		}
	}
}

func TestSafeReturnPath(t *testing.T) {
	for in, want := range map[string]string{
		"/account?tab=orders": "/account?tab=orders",
		"":                    "/",
		"https://evil.test/":  "/",
		"//evil.test/":        "/",
		"/\\evil.test":        "/",
		"/a\r\nSet-Cookie: x": "/",
		"relative":            "/",
	} {
		if got := safeReturnPath(in); got != want {
			t.Errorf("safeReturnPath(%q) = %q, want %q", in, got, want)
		}
	}
}
