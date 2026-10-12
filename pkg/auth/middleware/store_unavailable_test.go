package middleware_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func newMWWith(t *testing.T, a authmw.Authenticator, sess *fakeSessions, lk *fakeLookups) *authmw.Middleware {
	t.Helper()
	return authmw.New(a, sess, lk, nil, slog.Default(), middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
}

// deployTokenAuthn adds the optional deploy-token branch to fakeAuthn.
type deployTokenAuthn struct {
	*fakeAuthn
	err error
}

func (d deployTokenAuthn) AuthenticateDeployToken(context.Context, []byte) (state.Account, state.APIKey, error) {
	return state.Account{}, state.APIKey{}, d.err
}

// TestRequireSession_StoreFailureIsNotUnauthorized pins H9-160: on
// production-us a valid CLI key got "401 provide a valid API key … run
// gregale login" after 21 s because the key lookup timed out, and every
// non-sentinel lookup error fell through to the no-credentials 401. A store
// failure says nothing about the credential: answer a retryable 503 and keep
// the session cookie.
func TestRequireSession_StoreFailureIsNotUnauthorized(t *testing.T) {
	timeout := context.DeadlineExceeded
	plainDeployToken, _, err := api.GenerateDeployToken()
	if err != nil {
		t.Fatal(err)
	}
	sessionEnv := session.Envelope{AccountID: "acct-1", Sid: "sid-1"}
	cases := []struct {
		name    string
		authn   func() authmw.Authenticator
		lookups *fakeLookups
		sess    *fakeSessions
		req     *http.Request
	}{
		{
			name: "api key lookup",
			authn: func() authmw.Authenticator {
				a := newFakeAuthn()
				a.authKey[string(api.HashAPIKey(validBearerKey))] = authResult{err: timeout}
				return a
			},
			req: mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil),
		},
		{
			name: "oidc bearer lookup",
			authn: func() authmw.Authenticator {
				a := newFakeAuthn()
				a.authOIDC[string(api.HashAPIKey(validOIDCBearerKey))] = authResult{err: timeout}
				return a
			},
			req: mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validOIDCBearerKey}, nil),
		},
		{
			name:  "deploy token lookup",
			authn: func() authmw.Authenticator { return deployTokenAuthn{fakeAuthn: newFakeAuthn(), err: timeout} },
			req:   mkRequest("GET", "/v1/apps/demo", map[string]string{"Authorization": "Bearer " + plainDeployToken}, nil),
		},
		{
			name:    "session row lookup",
			authn:   func() authmw.Authenticator { return newFakeAuthn() },
			lookups: &fakeLookups{getErr: timeout},
			sess:    &fakeSessions{env: sessionEnv},
			req:     mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"}),
		},
		{
			name: "session account lookup",
			authn: func() authmw.Authenticator {
				a := newFakeAuthn()
				a.acctErr = timeout
				return a
			},
			lookups: &fakeLookups{sess: state.Session{ID: "sid-1", AccountID: "acct-1"}},
			sess:    &fakeSessions{env: sessionEnv},
			req:     mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookups := tc.lookups
			if lookups == nil {
				lookups = &fakeLookups{getErr: state.ErrNotFound}
			}
			sess := tc.sess
			if sess == nil {
				sess = &fakeSessions{err: state.ErrNotFound}
			}
			mw := newMWWith(t, tc.authn(), sess, lookups)
			// The limiter counts 401s; a store outage must not spend a
			// valid caller's failed-auth budget and end in 429s.
			for attempt := range 15 {
				w := httptest.NewRecorder()
				mw.RequireLimited(func(http.ResponseWriter, *http.Request, state.Account) {
					t.Fatal("handler ran without an authenticated principal")
				})(w, tc.req.Clone(context.Background()))
				if w.Code != http.StatusServiceUnavailable {
					t.Fatalf("attempt %d: status = %d, want 503; body %s", attempt, w.Code, w.Body.String())
				}
				if w.Header().Get("Retry-After") == "" {
					t.Fatalf("attempt %d: 503 without Retry-After", attempt)
				}
				for _, c := range w.Result().Cookies() {
					if c.Name == "faas_sid" && c.MaxAge < 0 {
						t.Fatalf("attempt %d: session cookie cleared on a store failure", attempt)
					}
				}
			}
		})
	}
}
