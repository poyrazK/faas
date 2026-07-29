// Whitebox + blackbox tests for pkg/authmw.Middleware.
//
// Blackbox cases live in this file (package auth_test) so they
// exercise the exported surface as cmd/apid + future cmd/gatewayd
// callers will. Whitebox tests for ctx-stamping helpers live in
// context_test.go (package auth) where they can call the
// unexported stamp helpers.
package middleware_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- fakes ---------------------------------------------------------------

type fakeAuthn struct {
	authKey    map[string]authResult // hash → (acct, key)
	acctByID   map[string]state.Account
	appBySlug  map[string]state.App
	touchCalls []string
	mu         sync.Mutex
}

type authResult struct {
	acct state.Account
	key  state.APIKey
	err  error
}

func newFakeAuthn() *fakeAuthn {
	return &fakeAuthn{
		authKey:   map[string]authResult{},
		acctByID:  map[string]state.Account{},
		appBySlug: map[string]state.App{},
	}
}

func (f *fakeAuthn) AuthenticateKey(_ context.Context, hash []byte) (state.Account, state.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.authKey[string(hash)]
	if !ok {
		return state.Account{}, state.APIKey{}, state.ErrNotFound
	}
	return r.acct, r.key, r.err
}

func (f *fakeAuthn) AccountByID(_ context.Context, id string) (state.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.acctByID[id]
	if !ok {
		return state.Account{}, state.ErrNotFound
	}
	return a, nil
}

func (f *fakeAuthn) AppBySlug(_ context.Context, slug string) (state.App, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.appBySlug[slug]
	if !ok {
		return state.App{}, state.ErrNotFound
	}
	return a, nil
}

func (f *fakeAuthn) TouchKeyLastUsed(_ context.Context, keyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touchCalls = append(f.touchCalls, keyID)
	return nil
}

type fakeSessions struct {
	env session.Envelope
	err error
}

func (s *fakeSessions) Verify(_ string) (session.Envelope, error) { return s.env, s.err }

type fakeLookups struct {
	// sessBySid keys state.Session rows by sid so tests that
	// drive multiple sessions through RequireSession see the
	// correct row per request. Default lookup falls back to the
	// single `sess` field for tests that only seed one row.
	sessBySid  map[string]state.Session
	sess       state.Session
	getErr     error
	touchCalls []string
	touchErr   error
	mu         sync.Mutex
}

func (l *fakeLookups) GetSession(_ context.Context, sid string) (state.Session, error) {
	if l.sessBySid != nil {
		if row, ok := l.sessBySid[sid]; ok {
			return row, nil
		}
	}
	return l.sess, l.getErr
}
func (l *fakeLookups) TouchSessionLastSeen(_ context.Context, sid string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.touchCalls = append(l.touchCalls, sid)
	return l.touchErr
}

type fakeAuditor struct {
	mu   sync.Mutex
	rows []auditRow
}

type auditRow struct {
	kind      string
	accountID *string
	data      map[string]any
}

func (a *fakeAuditor) Emit(_ context.Context, kind string, accountID *string, data map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, auditRow{kind, accountID, data})
}

func (a *fakeAuditor) rowsOf(kind string) []auditRow {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []auditRow{}
	for _, r := range a.rows {
		if r.kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// Compile-time interface assertions: a future method added to any
// of the exported interfaces will surface as a compile error here.
var (
	_ authmw.Authenticator = (*fakeAuthn)(nil)
	_ authmw.Sessions      = (*fakeSessions)(nil)
	_ authmw.SessionLookup = (*fakeLookups)(nil)
	_ authmw.Auditor       = (*fakeAuditor)(nil)
)

// --- helpers -------------------------------------------------------------

func newMW(t *testing.T, a *fakeAuthn, sess *fakeSessions, lk *fakeLookups, au *fakeAuditor) *authmw.Middleware {
	t.Helper()
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(a, sess, lk, au, slog.Default(), lim)
	return mw
}

func TestNew_NilAuthenticatorPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil Authenticator")
		}
	}()
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	_ = authmw.New(nil, nil, nil, nil, slog.Default(), lim)
}

func TestNew_NilLimiterPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil Limiter (RequireLimited cannot share an empty bucket)")
		}
	}()
	_ = authmw.New(newFakeAuthn(), nil, nil, nil, slog.Default(), nil)
}

func mkActiveAccount(id string) state.Account {
	return state.Account{ID: id, Email: id + "@x", Status: state.AccountActive}
}

func mkKey(id string, scopes ...string) state.APIKey {
	return state.APIKey{ID: id, Scopes: scopes}
}

func mkRequest(method, path string, headers map[string]string, cookies map[string]string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	for k, v := range cookies {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	return r
}

// --- RequireSession: bearer-key branch ----------------------------------

// validBearerKey is "fp_live_" (8) + 48 hex chars (24 bytes of
// entropy per pkg/api/apikey.go:apiKeyRandomBytes). Format must
// pass api.ValidAPIKeyFormat; length is checked.
const validBearerKey = "fp_live_0123456789abcdef0123456789abcdef0123456789abcdef" // len = 56 (8 prefix + 48 hex)

func TestRequireSession_BearerHappyPath(t *testing.T) {
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{acct: mkActiveAccount("acct-1"), key: mkKey("key-1", api.ScopeAdmin)}

	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, r *http.Request, acct state.Account) {
		hits++
		if acct.ID != "acct-1" {
			t.Errorf("acct.ID = %q, want acct-1", acct.ID)
		}
		gotAcct, gotKey, ok := authmw.AccountFromContext(r)
		if !ok || gotAcct.ID != "acct-1" || gotKey == nil || gotKey.ID != "key-1" {
			t.Errorf("ctx stamp missing or wrong: (%+v, %+v, %v)", gotAcct, gotKey, ok)
		}
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
	h(rec, r)

	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestRequireSession_BearerSchemeCaseInsensitive pins RFC 6750 §2.1:
// the "Bearer" scheme name is case-insensitive. A client that
// sends "bearer <token>" or "BEARER <token>" must reach the same
// authentication path as "Bearer <token>".
func TestRequireSession_BearerSchemeCaseInsensitive(t *testing.T) {
	for _, scheme := range []string{"Bearer", "bearer", "BEARER", "BeArEr"} {
		t.Run(scheme, func(t *testing.T) {
			authn := newFakeAuthn()
			hash := api.HashAPIKey(validBearerKey)
			authn.authKey[string(hash)] = authResult{acct: mkActiveAccount("acct-1"), key: mkKey("key-1", api.ScopeAdmin)}

			mw := newMW(t, authn, nil, nil, nil)
			hits := 0
			h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

			rec := httptest.NewRecorder()
			r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": scheme + " " + validBearerKey}, nil)
			h(rec, r)

			if hits != 1 {
				t.Errorf("scheme=%q hits=%d, want 1", scheme, hits)
			}
			if rec.Code != http.StatusOK {
				t.Errorf("scheme=%q status=%d, want 200", scheme, rec.Code)
			}
		})
	}
}

func TestRequireSession_BearerInvalidFormat(t *testing.T) {
	authn := newFakeAuthn()
	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer not-a-valid-key"}, nil)
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeUnauthorized) {
		t.Errorf("body missing CodeUnauthorized: %q", rec.Body.String())
	}
}

func TestRequireSession_BearerInactiveAccount(t *testing.T) {
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{
		acct: state.Account{ID: "acct-1", Email: "x@y", Status: state.AccountSuspended},
		key:  mkKey("key-1"),
	}

	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (inactive account must 402)", hits)
	}
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeBillingPastDue) {
		t.Errorf("body missing CodeBillingPastDue: %q", rec.Body.String())
	}
}

func TestRequireSession_BearerDeletedPendingAccountScopedPath(t *testing.T) {
	// Account in deleted_pending but path is /v1/account — must
	// still 200 (the carve-out for /v1/account during grace).
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{
		acct: state.Account{ID: "acct-1", Email: "x@y", Status: state.AccountDeletedPending},
		key:  mkKey("key-1"),
	}

	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/account", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
	h(rec, r)

	if hits != 1 {
		t.Errorf("hits = %d, want 1 (deleted_pending + /v1/account carve-out)", hits)
	}
}

func TestRequireSession_BearerDeletedPendingNonAccountScoped(t *testing.T) {
	// Account in deleted_pending but path is /v1/apps — must 402
	// (no carve-out for non-account paths).
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{
		acct: state.Account{ID: "acct-1", Email: "x@y", Status: state.AccountDeletedPending},
		key:  mkKey("key-1"),
	}

	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (deleted_pending + non-account path)", hits)
	}
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("status = %d, want 402", rec.Code)
	}
}

func TestRequireSession_BearerTouchKeyLastUsedDebounce(t *testing.T) {
	// Two back-to-back bearer auths on the same key must only
	// fire one detached TouchKeyLastUsed (the 30s debounce).
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{acct: mkActiveAccount("acct-1"), key: mkKey("key-1", api.ScopeAdmin)}

	mw := newMW(t, authn, nil, nil, nil)
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {})

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
		h(rec, r)
	}

	// Wait briefly for the detached goroutine to land.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		authn.mu.Lock()
		n := len(authn.touchCalls)
		authn.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	authn.mu.Lock()
	defer authn.mu.Unlock()
	if len(authn.touchCalls) != 1 {
		t.Errorf("touch calls = %d, want 1 (debounce should fire only one)", len(authn.touchCalls))
	}
}

// --- RequireSession: session-cookie branch -------------------------------

func TestRequireSession_SessionHappyPath(t *testing.T) {
	authn := newFakeAuthn()
	authn.acctByID["acct-1"] = mkActiveAccount("acct-1")

	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}
	lookups := &fakeLookups{sess: state.Session{ID: "sid-1", AccountID: "acct-1"}}

	mw := newMW(t, authn, sess, lookups, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, r *http.Request, acct state.Account) {
		hits++
		if acct.ID != "acct-1" {
			t.Errorf("acct.ID = %q, want acct-1", acct.ID)
		}
		gotAcct, gotKey, ok := authmw.AccountFromContext(r)
		if !ok || gotAcct.ID != "acct-1" || gotKey != nil {
			t.Errorf("session-cookie principal should be (acct, nil, true): got (%+v, %+v, %v)", gotAcct, gotKey, ok)
		}
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	h(rec, r)

	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRequireSession_SessionCrossCheckFailsClearsCookie(t *testing.T) {
	authn := newFakeAuthn()
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}
	// GetSession returns ErrNotFound — the cookie was revoked.
	lookups := &fakeLookups{getErr: state.ErrNotFound}

	mw := newMW(t, authn, sess, lookups, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (revoked cookie)", hits)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	// Set-Cookie should clear the session cookie. stdlib
	// serialises MaxAge=-1 as "Max-Age=0" in the Set-Cookie
	// header; the deletion semantic is the empty Value +
	// MaxAge<=0 + Path=/. Matching the literal output.
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "faas_sid=") || !strings.Contains(setCookie, "Max-Age=0") {
		t.Errorf("Set-Cookie should clear faas_sid: %q", setCookie)
	}
}

func TestRequireSession_SessionRevokedEmitsStolenAudit(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}
	now := time.Now()
	lookups := &fakeLookups{sess: state.Session{ID: "sid-1", AccountID: "acct-1", RevokedAt: &now}}

	mw := newMW(t, authn, sess, lookups, audit)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (revoked)", hits)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	rows := audit.rowsOf("auth.session.stolen")
	if len(rows) != 1 {
		t.Errorf("audit rows = %d, want 1 auth.session.stolen", len(rows))
	}
}

func TestRequireSession_NoCredentialsReturns401(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, nil)
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

// --- RequireMFA ----------------------------------------------------------

func TestRequireMFA_BearerBypassesGate(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, &fakeAuditor{})
	hits := 0
	h := mw.RequireMFA(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, nil) // no mfa-pending stamp
	h(rec, r, state.Account{ID: "acct-1"})

	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

func TestRequireMFA_PendingSessionBlocks(t *testing.T) {
	audit := &fakeAuditor{}
	mw := newMW(t, newFakeAuthn(), nil, nil, audit)
	hits := 0
	h := mw.RequireMFA(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("POST", "/v1/apps/foo/deploy", nil, nil)
	r = r.WithContext(authmw.WithMFAPending(r.Context(), true))
	h(rec, r, state.Account{ID: "acct-1"})

	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	rows := audit.rowsOf("auth.mfa_gate_hit")
	if len(rows) != 1 {
		t.Errorf("audit rows = %d, want 1", len(rows))
	}
}

func TestRequireMFA_AllowlistedPathPasses(t *testing.T) {
	audit := &fakeAuditor{}
	mw := newMW(t, newFakeAuthn(), nil, nil, audit)
	hits := 0
	h := mw.RequireMFA(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	for _, p := range []string{
		"/v1/account",
		"/v1/account/mfa/enroll",
		"/v1/account/mfa/verify",
		"/v1/account/mfa/recover",
		"/v1/account/mfa/disable",
		"/v1/auth/logout",
		"/v1/auth/sessions",
		"/v1/auth/sessions/revoke_all",
		"/v1/auth/sessions/sid-1", // prefix wildcard
	} {
		t.Run(p, func(t *testing.T) {
			hits = 0
			rec := httptest.NewRecorder()
			r := mkRequest("GET", p, nil, nil)
			r = r.WithContext(authmw.WithMFAPending(r.Context(), true))
			h(rec, r, state.Account{ID: "acct-1"})
			if hits != 1 {
				t.Errorf("hits = %d, want 1 (allowlisted)", hits)
			}
		})
	}
}

// --- RequireScope --------------------------------------------------------

func TestRequireScope_BearerWithScopePasses(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, nil)
	hits := 0
	h := mw.RequireScope(api.ScopeAppsRead)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, nil)
	// Stamp a bearer principal carrying the required scope.
	r = r.WithContext(authWithPrincipal(r.Context(), mkActiveAccount("acct-1"), &state.APIKey{Scopes: []string{api.ScopeAppsRead}}))
	h(rec, r, state.Account{ID: "acct-1"})

	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
}

func TestRequireScope_BearerWithWrongScopeForbidden(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, nil)
	hits := 0
	h := mw.RequireScope(api.ScopeAppsRead)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, nil)
	r = r.WithContext(authWithPrincipal(r.Context(), mkActiveAccount("acct-1"), &state.APIKey{Scopes: []string{"bogus"}}))
	h(rec, r, state.Account{ID: "acct-1"})

	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeForbidden) {
		t.Errorf("body missing CodeForbidden: %q", rec.Body.String())
	}
}

func TestRequireScope_SessionCookieIsImplicitAdmin(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, nil)
	hits := 0
	h := mw.RequireScope(api.ScopeSecretsWrite)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps/x/secrets/list", nil, nil)
	// Session-cookie principal: Key == nil.
	r = r.WithContext(authWithPrincipal(r.Context(), mkActiveAccount("acct-1"), nil))
	h(rec, r, state.Account{ID: "acct-1"})

	if hits != 1 {
		t.Errorf("hits = %d, want 1 (session cookie = admin)", hits)
	}
}

func TestRequireScope_MissingPrincipalFailsClosed(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, nil)
	hits := 0
	h := mw.RequireScope(api.ScopeAppsRead)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, nil) // no principal stamp
	h(rec, r, state.Account{ID: "acct-1"})

	if hits != 0 {
		t.Errorf("hits = %d, want 0", hits)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeCapacity) {
		t.Errorf("body missing CodeCapacity: %q", rec.Body.String())
	}
}

func TestRequireScope_EmptyAllowedIsNoOp(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, nil)
	hits := 0
	h := mw.RequireScope()(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/internal/health", nil, nil) // no principal, no scope
	h(rec, r, state.Account{ID: "acct-1"})

	if hits != 1 {
		t.Errorf("hits = %d, want 1 (empty allowed = no check)", hits)
	}
}

// --- RequireLimited ------------------------------------------------------

func TestRequireLimited_BlocksEleventhAttempt(t *testing.T) {
	authn := newFakeAuthn()
	// All keys invalid so every request 401s.
	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireLimited(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	// 10 401s within the bucket window.
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer bad"}, nil)
		h(rec, r)
	}
	// 11th attempt must be 429 — bucket exhausted.
	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer bad"}, nil)
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (all bearer-key auths are invalid)", hits)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("11th status = %d, want 429", rec.Code)
	}
}

// --- LoadApp -------------------------------------------------------------

func TestLoadApp_SameAccountReturnsApp(t *testing.T) {
	authn := newFakeAuthn()
	authn.appBySlug["my-app"] = state.App{ID: "app-1", Slug: "my-app", AccountID: "acct-1"}
	mw := newMW(t, authn, nil, nil, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps/my-app", nil, nil)
	app, ok := mw.LoadApp(rec, r, mkActiveAccount("acct-1"), "my-app")

	if !ok {
		t.Errorf("ok = false, want true")
	}
	if app.ID != "app-1" {
		t.Errorf("app.ID = %q, want app-1", app.ID)
	}
}

func TestLoadApp_CrossAccountReturns404(t *testing.T) {
	authn := newFakeAuthn()
	authn.appBySlug["other"] = state.App{ID: "app-2", Slug: "other", AccountID: "acct-2"}
	mw := newMW(t, authn, nil, nil, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps/other", nil, nil)
	_, ok := mw.LoadApp(rec, r, mkActiveAccount("acct-1"), "other")

	if ok {
		t.Errorf("ok = true, want false (cross-tenant)")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (NOT 403 — never leak existence)", rec.Code)
	}
}

func TestLoadApp_MissingSlugReturns404(t *testing.T) {
	authn := newFakeAuthn()
	mw := newMW(t, authn, nil, nil, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps/missing", nil, nil)
	_, ok := mw.LoadApp(rec, r, mkActiveAccount("acct-1"), "missing")

	if ok {
		t.Errorf("ok = true, want false (missing slug)")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- TouchKeyLastUsed detachment ---------------------------------------

func TestTouchKeyLastUsed_DetachedGoroutineCompletes(t *testing.T) {
	// Issue: a request that cancels client-side (tab close, SSE
	// disconnect) must still leave the touch stamp behind. The
	// detached goroutine has a 2s bounded context independent of
	// the request ctx.
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{acct: mkActiveAccount("acct-1"), key: mkKey("key-1", api.ScopeAdmin)}

	mw := newMW(t, authn, nil, nil, nil)
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {})

	// Cancel the request context immediately after calling.
	ctx, cancel := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil).WithContext(ctx)
	h(rec, r)
	cancel()

	// The detached touch must still run within 1s.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		authn.mu.Lock()
		n := len(authn.touchCalls)
		authn.mu.Unlock()
		if n >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	authn.mu.Lock()
	defer authn.mu.Unlock()
	t.Errorf("touch did not complete after request cancel; calls = %d", len(authn.touchCalls))
}

// TestClearSessionCookie_Attributes pins the cookie attribute set
// on the eviction Set-Cookie. The issuer in handlers_auth.go sets
// Path=/ + Secure + SameSite=Lax + HttpOnly; the eviction must use
// the same attributes so the browser overwrites the live cookie
// instead of keeping a second entry with a different scope.
//
// Test the attr shape via http.ParseSetCookie so a future "let me
// drop HttpOnly because it's local dev" edit surfaces immediately.
func TestClearSessionCookie_Attributes(t *testing.T) {
	mw := newMW(t, newFakeAuthn(), nil, nil, nil)
	rec := httptest.NewRecorder()
	mw.ClearSessionCookie(rec)

	setCookies := rec.Result().Cookies()
	if len(setCookies) != 1 {
		t.Fatalf("Set-Cookie count = %d, want 1 (got %v)", len(setCookies), setCookies)
	}
	c := setCookies[0]
	if c.Name != "faas_sid" {
		t.Errorf("cookie name = %q, want faas_sid", c.Name)
	}
	if c.Value != "" {
		t.Errorf("cookie value = %q, want empty (eviction)", c.Value)
	}
	if c.Path != "/" {
		t.Errorf("cookie Path = %q, want / (must match issuer scope)", c.Path)
	}
	if !c.Secure {
		t.Errorf("cookie Secure = false; want true (§11 ship-blocker)")
	}
	if !c.HttpOnly {
		t.Errorf("cookie HttpOnly = false; want true")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.MaxAge >= 0 {
		t.Errorf("cookie MaxAge = %d, want < 0 (eviction)", c.MaxAge)
	}
}
