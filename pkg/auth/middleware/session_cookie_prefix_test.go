// adr: 20261026112500
package middleware_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"github.com/onebox-faas/faas/pkg/state"
)

// cookieValueSessions verifies by cookie value, so a test can tell which
// cookie the middleware read.
type cookieValueSessions map[string]session.Envelope

func (s cookieValueSessions) Verify(value string) (session.Envelope, error) {
	if env, ok := s[value]; ok {
		return env, nil
	}
	return session.Envelope{}, errors.New("unknown session")
}

// A tenant page on another host of the registrable domain can set faas_sid
// with Domain=<domain>; it must never replace the session the browser holds
// as __Host-faas_sid. Without a prefixed cookie the legacy one still works
// until the removal release.
func TestRequireSessionPrefersTheHostPrefixedCookie(t *testing.T) {
	authn := newFakeAuthn()
	authn.acctByID["acct-victim"] = mkActiveAccount("acct-victim")
	authn.acctByID["acct-attacker"] = mkActiveAccount("acct-attacker")
	sessions := cookieValueSessions{
		"victim-cookie":   {AccountID: "acct-victim", Sid: "sid-victim"},
		"attacker-cookie": {AccountID: "acct-attacker", Sid: "sid-attacker"},
	}
	lookups := &fakeLookups{sessBySid: map[string]state.Session{
		"sid-victim":   {ID: "sid-victim", AccountID: "acct-victim"},
		"sid-attacker": {ID: "sid-attacker", AccountID: "acct-attacker"},
	}}
	mw := authmw.New(authn, sessions, lookups, nil, slog.Default(), middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)

	for name, tc := range map[string]struct {
		cookies map[string]string
		want    string
	}{
		"prefixed only":      {map[string]string{sessioncookie.Name: "victim-cookie"}, "acct-victim"},
		"legacy only":        {map[string]string{sessioncookie.LegacyName: "victim-cookie"}, "acct-victim"},
		"shadowed by tenant": {map[string]string{sessioncookie.LegacyName: "attacker-cookie", sessioncookie.Name: "victim-cookie"}, "acct-victim"},
	} {
		t.Run(name, func(t *testing.T) {
			var got string
			h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, acct state.Account) { got = acct.ID })
			rec := httptest.NewRecorder()
			h(rec, mkRequest("GET", "/v1/apps", nil, tc.cookies))
			if rec.Code != http.StatusOK || got != tc.want {
				t.Fatalf("status %d account %q, want 200 %q", rec.Code, got, tc.want)
			}
		})
	}
}

func TestClearSessionCookieExpiresBothNames(t *testing.T) {
	mw := authmw.New(newFakeAuthn(), cookieValueSessions{}, &fakeLookups{}, nil, slog.Default(), middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
	rec := httptest.NewRecorder()
	mw.ClearSessionCookie(rec)
	seen := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge != -1 || c.Value != "" {
			t.Fatalf("cookie %+v not expired", c)
		}
		seen[c.Name] = true
	}
	if !seen[sessioncookie.Name] || !seen[sessioncookie.LegacyName] {
		t.Fatalf("expired cookies = %v, want both names", seen)
	}
}
