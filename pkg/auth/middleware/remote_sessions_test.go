// adr: 20261102191500
package middleware_test

import (
	"context"
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

// remoteSessions opens cookies the way gatewayd-internal does, by asking
// apid; it records the context it was asked under.
type remoteSessions struct {
	env    session.Envelope
	err    error
	gotCtx bool
}

func (s *remoteSessions) Verify(string) (session.Envelope, error) {
	return session.Envelope{}, errors.New("Verify must not be called on a ContextSessions")
}

func (s *remoteSessions) VerifyContext(ctx context.Context, _ string) (session.Envelope, error) {
	s.gotCtx = ctx != nil
	return s.env, s.err
}

// A gateway without the session key asks apid to open the cookie
// (ADR-20261102191500). apid being unreachable must not read as a bad
// cookie: the request gets a retryable 503 and the browser keeps its
// session, while a cookie apid rejects is a plain 401.
func TestRequireSessionAsksContextSessions(t *testing.T) {
	authn := newFakeAuthn()
	authn.acctByID["acct-1"] = mkActiveAccount("acct-1")
	lookups := &fakeLookups{sessBySid: map[string]state.Session{"sid-1": {ID: "sid-1", AccountID: "acct-1"}}}
	cookies := map[string]string{sessioncookie.Name: "cookie"}

	for name, tc := range map[string]struct {
		sessions   *remoteSessions
		wantStatus int
	}{
		"apid opens it":    {&remoteSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}, http.StatusOK},
		"apid rejects it":  {&remoteSessions{err: session.ErrInvalid}, http.StatusUnauthorized},
		"apid unreachable": {&remoteSessions{err: errors.New("apidgrpc: VerifySession: connection refused")}, http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			mw := authmw.New(authn, tc.sessions, lookups, nil, slog.Default(), middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
			h := mw.RequireSession(func(w http.ResponseWriter, _ *http.Request, _ state.Account) { w.WriteHeader(http.StatusOK) })
			rec := httptest.NewRecorder()
			h(rec, mkRequest("GET", "/v1/apps", nil, cookies))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if !tc.sessions.gotCtx {
				t.Fatal("VerifyContext was not called with the request context")
			}
			for _, c := range rec.Result().Cookies() {
				if c.MaxAge == -1 {
					t.Fatalf("cookie %s was cleared", c.Name)
				}
			}
		})
	}
}
