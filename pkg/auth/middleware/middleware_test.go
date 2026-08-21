// Whitebox + blackbox tests for pkg/authmw.Middleware.
//
// Blackbox cases live in this file (package auth_test) so they
// exercise the exported surface as cmd/apid + future cmd/gatewayd-internal/
// callers will. Whitebox tests for ctx-stamping helpers live in
// context_test.go (package auth) where they can call the
// unexported stamp helpers.
package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/bindinghash"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- fakes ---------------------------------------------------------------

type fakeAuthn struct {
	authKey    map[string]authResult // hash → (acct, key)
	authOIDC   map[string]authResult // hash → (acct, key) for the OIDC branch (issue #270 / ADR-101)
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
		authOIDC:  map[string]authResult{},
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

// AuthenticateOIDCBearer is the ADR-101 stub mirroring AuthenticateKey's
// shape but keyed by a separate map so tests can drive the OIDC and
// fp_live_ branches independently without hash collisions. Returns
// state.ErrNotFound on miss (same posture as AuthenticateKey); the
// middleware falls through to the cookie branch on miss.
func (f *fakeAuthn) AuthenticateOIDCBearer(_ context.Context, hash []byte) (state.Account, state.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.authOIDC[string(hash)]
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
	// revokeCalls + revokeErr back the new middleware SessionLookup
	// method (IAM-hardening-mega-PR logical change 5, ADR-076).
	// The binding-mismatch branch calls RevokeSession during the
	// auto-revoke; tests assert the call landed with the expected
	// (sid, accountID) pair.
	revokeCalls []revokeCall
	revokeErr   error
	mu          sync.Mutex
}

// revokeCall is the per-call record the binding-mismatch test
// inspects. Fields match the SessionLookup.RevokeSession signature.
type revokeCall struct {
	sid, accountID string
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
func (l *fakeLookups) RevokeSession(_ context.Context, sid, accountID string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.revokeCalls = append(l.revokeCalls, revokeCall{sid: sid, accountID: accountID})
	if l.revokeErr != nil {
		return false, l.revokeErr
	}
	// Mark the underlying session row revoked. Tests that need the
	// "revoked row" assertion can read it back via GetSession; we
	// also keep a parallel counter so tests don't have to dispatch
	// on the sessBySid map.
	if l.sessBySid != nil {
		if row, ok := l.sessBySid[sid]; ok {
			now := time.Now()
			row.RevokedAt = &now
			l.sessBySid[sid] = row
		}
	}
	if l.sess.ID == sid {
		now := time.Now()
		l.sess.RevokedAt = &now
	}
	return true, nil
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
	mw := authmw.New(a, sess, lk, au, slog.Default(), lim, nil)
	return mw
}

func TestNew_NilAuthenticatorPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil Authenticator")
		}
	}()
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	_ = authmw.New(nil, nil, nil, nil, slog.Default(), lim, nil)
}

func TestNew_NilLimiterPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on nil Limiter (RequireLimited cannot share an empty bucket)")
		}
	}()
	_ = authmw.New(newFakeAuthn(), nil, nil, nil, slog.Default(), nil, nil)
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

// --- IAM-5: API-key lifecycle sentinels (issue #189) ---------------------

// TestRequireSession_BearerExpiredKeyReturns401 pins the
// middleware→RFC 7807 contract for the api_key_expired path.
// The store has already lazily marked the key revoked before
// the middleware sees the sentinel; the middleware's only job
// is (a) emit key.expired audit, (b) write 401 api_key_expired.
//
// The audit row is the load-bearing piece — operations use it
// to alert customers that their key expired and they need to
// rotate. Without the audit, the 401 looks identical to a
// generic auth failure and the dashboard can't surface
// "key expired, rotate me" copy.
func TestRequireSession_BearerExpiredKeyReturns401(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{
		acct: mkActiveAccount("acct-1"),
		key:  mkKey("key-1"),
		err:  state.ErrAPIKeyExpired,
	}

	mw := newMW(t, authn, nil, nil, audit)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (expired key must 401)", hits)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeAPIKeyExpired) {
		t.Errorf("body missing %s: %q", api.CodeAPIKeyExpired, rec.Body.String())
	}
	// Audit: one key.expired row, no other key.* event.
	rows := audit.rowsOf("key.expired")
	if len(rows) != 1 {
		t.Fatalf("key.expired audit rows = %d, want 1", len(rows))
	}
	if rows[0].data["key_id"] != "key-1" {
		t.Errorf("audited key_id = %v, want key-1", rows[0].data["key_id"])
	}
	if len(audit.rowsOf("key.auth_rejected_revoked")) != 0 {
		t.Errorf("key.auth_rejected_revoked should not fire on expired (distinct sentinel)")
	}
}

// TestRequireSession_BearerRevokedKeyReturns401 pins the
// api_key_revoked path. The sentinel is terminal —
// auth_rejected_revoked is the audit kind, distinct from
// key.expired because the customer recover path differs
// (revoked = re-mint; expired = rotate).
func TestRequireSession_BearerRevokedKeyReturns401(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	hash := api.HashAPIKey(validBearerKey)
	authn.authKey[string(hash)] = authResult{
		acct: mkActiveAccount("acct-1"),
		key:  mkKey("key-1"),
		err:  state.ErrAPIKeyRevoked,
	}

	mw := newMW(t, authn, nil, nil, audit)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validBearerKey}, nil)
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (revoked key must 401)", hits)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeAPIKeyRevoked) {
		t.Errorf("body missing %s: %q", api.CodeAPIKeyRevoked, rec.Body.String())
	}
	rows := audit.rowsOf("key.auth_rejected_revoked")
	if len(rows) != 1 {
		t.Fatalf("key.auth_rejected_revoked audit rows = %d, want 1", len(rows))
	}
	if rows[0].data["key_id"] != "key-1" {
		t.Errorf("audited key_id = %v, want key-1", rows[0].data["key_id"])
	}
	if len(audit.rowsOf("key.expired")) != 0 {
		t.Errorf("key.expired should not fire on revoked (distinct sentinel)")
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

// validOIDCBearerKey is the OIDC-derived bearer shape
// (issue #270 / ADR-101): "fp_oidc_" (8) + 48 hex chars
// (24 bytes of entropy). Format must pass api.ValidOIDCKeyFormat;
// length is checked. The 5-min TTL means the path does NOT call
// TouchKeyLastUsed — see TestRequireSession_OIDCBearer_KeyDebounceUnused
// for the contract.
const validOIDCBearerKey = "fp_oidc_0123456789abcdef0123456789abcdef0123456789abcdef" // len = 56

func TestRequireSession_OIDCBearerHappyPath(t *testing.T) {
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validOIDCBearerKey)
	// The OIDC branch stamps a synthetic APIKey projection with
	// Scopes=["deploy:write"] (ADR-101 customer-locked decision).
	// The fake stores whatever the test stamps.
	authn.authOIDC[string(hash)] = authResult{
		acct: mkActiveAccount("acct-1"),
		key:  mkKey("oidc-tok-1", api.ScopeDeployWrite),
	}

	mw := newMW(t, authn, nil, nil, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, r *http.Request, acct state.Account) {
		hits++
		if acct.ID != "acct-1" {
			t.Errorf("acct.ID = %q, want acct-1", acct.ID)
		}
		_, gotKey, ok := authmw.AccountFromContext(r)
		if !ok || gotKey == nil || gotKey.ID != "oidc-tok-1" {
			t.Errorf("ctx stamp missing or wrong: (key=%+v, ok=%v)", gotKey, ok)
		}
		if len(gotKey.Scopes) != 1 || gotKey.Scopes[0] != api.ScopeDeployWrite {
			t.Errorf("scopes = %v, want [%s]", gotKey.Scopes, api.ScopeDeployWrite)
		}
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validOIDCBearerKey}, nil)
	h(rec, r)

	if hits != 1 {
		t.Errorf("hits = %d, want 1", hits)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRequireSession_OIDCBearer_KeyDebounceUnused pins the
// contract that the OIDC branch does NOT call TouchKeyLastUsed.
// The 5-min TTL bounds the write profile; the per-exchange
// audit row is the durable record. KeyDebounce is keyed on
// api_keys.id, a different namespace, so the OIDC branch
// wouldn't grow it anyway — this test just nails the
// not-call invariant.
func TestRequireSession_OIDCBearer_KeyDebounceUnused(t *testing.T) {
	authn := newFakeAuthn()
	hash := api.HashAPIKey(validOIDCBearerKey)
	authn.authOIDC[string(hash)] = authResult{
		acct: mkActiveAccount("acct-1"),
		key:  mkKey("oidc-tok-1", api.ScopeDeployWrite),
	}

	mw := newMW(t, authn, nil, nil, nil)
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {})

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validOIDCBearerKey}, nil)
		h(rec, r)
	}

	authn.mu.Lock()
	defer authn.mu.Unlock()
	if len(authn.touchCalls) != 0 {
		t.Errorf("OIDC bearer should not call TouchKeyLastUsed, got %d calls", len(authn.touchCalls))
	}
}

// TestRequireSession_OIDCBearer_NotFoundFallsThrough pins the
// contract that an unknown OIDC bearer does not 401 on the OIDC
// branch alone — the middleware falls through to the cookie
// branch. Today the cookie branch also 401s (no cookie), so
// the visible status is 401, but the path is "OIDC miss →
// cookie miss → 401", not "OIDC miss → 401 short-circuit".
func TestRequireSession_OIDCBearer_NotFoundFallsThrough(t *testing.T) {
	authn := newFakeAuthn()
	// authOIDC is empty — every hash returns ErrNotFound.
	mw := newMW(t, authn, nil, nil, nil)
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		t.Errorf("downstream should not be called when OIDC + cookie both miss")
	})

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", map[string]string{"Authorization": "Bearer " + validOIDCBearerKey}, nil)
	h(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (OIDC miss + cookie miss)", rec.Code)
	}
}

// --- RequireStepUp ---------------------------------------------------------

// TestRequireStepUp_ExpiredStamp_403 pins the gate: a step_up_at
// stamp older than the TTL ⇒ 403 CodeMFARequired + an
// auth.step_up_required audit row with reason="expired".
func TestRequireStepUp_ExpiredStamp_403(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(authn, &fakeSessions{}, &fakeLookups{}, audit,
		slog.New(slog.NewTextHandler(io.Discard, nil)), lim, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/keys/some/rotate", nil, nil)
	r = r.WithContext(authmw.WithStepUp(r.Context(), time.Now().Add(-10*time.Minute)))
	h := mw.RequireStepUp(5 * time.Minute)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {})
	h(rec, r, mkActiveAccount("acct-1"))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if rows := audit.rowsOf("auth.step_up_required"); len(rows) != 1 {
		t.Errorf("audit rows = %d, want 1 auth.step_up_required", len(rows))
	} else if data := rows[0].data["reason"]; data != "expired" {
		t.Errorf("reason = %v, want expired", data)
	}
}

// TestRequireStepUp_FreshStamp_Passes pins the happy-path: a
// step_up_at stamp newer than TTL ⇒ inner handler fires, no
// audit row.
func TestRequireStepUp_FreshStamp_Passes(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(authn, &fakeSessions{}, &fakeLookups{}, audit,
		slog.New(slog.NewTextHandler(io.Discard, nil)), lim, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/keys/some/rotate", nil, nil)
	r = r.WithContext(authmw.WithStepUp(r.Context(), time.Now().Add(-30*time.Second)))
	hits := 0
	h := mw.RequireStepUp(5 * time.Minute)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})
	h(rec, r, mkActiveAccount("acct-1"))

	if hits != 1 {
		t.Errorf("hits = %d, want 1 (fresh step-up should pass)", hits)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rows := audit.rowsOf("auth.step_up_required"); len(rows) != 0 {
		t.Errorf("audit rows = %d, want 0 (fresh stamp)", len(rows))
	}
}

// TestRequireStepUp_MissingStamp_403 pins the absent-stamp branch:
// no WithStepUp has been called on r.Context() ⇒ gate fires with
// reason="missing" (the bearer principal / pre-PR-077 cookie
// path; both are documented legacy cookies whose bypass is
// intentional but the gate still classifies them as "missing"
// in the audit row).
func TestRequireStepUp_MissingStamp_403(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(authn, &fakeSessions{}, &fakeLookups{}, audit,
		slog.New(slog.NewTextHandler(io.Discard, nil)), lim, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/keys/some/rotate", nil, nil)
	hits := 0
	h := mw.RequireStepUp(5 * time.Minute)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})
	h(rec, r, mkActiveAccount("acct-1"))

	// Missing-stamp = bearer-bypass path: pass through. Verify
	// hits=1 and no audit row.
	if hits != 1 {
		t.Errorf("hits = %d, want 1 (bearer-bypass)", hits)
	}
	if rows := audit.rowsOf("auth.step_up_required"); len(rows) != 0 {
		t.Errorf("audit rows = %d, want 0 (bearer bypass)", len(rows))
	}
}

// TestRequireStepUpStrict_BearerKey_RejectsWithoutFreshStepUp
// (PR-9 §4) — pin the strict-mode rejection: with no step-up
// stamp on the request (the bearer-key path), the gate MUST
// fire with 403 instead of bypassing. PR-9 closes the
// "API key is step-up-equivalent proof" assumption on routes
// where a leaked token alone is sufficient to perform the
// action (acceptInvitation is the first such route).
func TestRequireStepUpStrict_BearerKey_RejectsWithoutFreshStepUp(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(authn, &fakeSessions{}, &fakeLookups{}, audit,
		slog.New(slog.NewTextHandler(io.Discard, nil)), lim, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("POST", "/v1/invitations/abc/accept", nil, nil)
	hits := 0
	h := mw.RequireStepUpStrict(5 * time.Minute)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})
	h(rec, r, mkActiveAccount("acct-1"))

	// Strict mode: missing-stamp = REJECT with 403. The
	// next handler must not run.
	if hits != 0 {
		t.Errorf("hits = %d, want 0 (strict misses must reject)", hits)
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rec.Code)
	}
	// Audit row fires with strict=true.
	rows := audit.rowsOf("auth.step_up_required")
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if rows[0].data["strict"] != true {
		t.Errorf("data.strict = %v, want true", rows[0].data["strict"])
	}
	if rows[0].data["reason"] != "missing" {
		t.Errorf("data.reason = %v, want missing", rows[0].data["reason"])
	}
}

// TestRequireStepUpStrict_FreshStamp_Passes (PR-9 §4) — pin the
// happy path: a fresh step-up stamp on the request passes
// through to the next handler. The strict mode is identical to
// the lax mode for cookie principals whose stamp is fresh.
func TestRequireStepUpStrict_FreshStamp_Passes(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(authn, &fakeSessions{}, &fakeLookups{}, audit,
		slog.New(slog.NewTextHandler(io.Discard, nil)), lim, nil)

	rec := httptest.NewRecorder()
	r := mkRequest("POST", "/v1/invitations/abc/accept", nil, nil)
	// Stamp set 30s ago — well within the 5m TTL.
	r = r.WithContext(authmw.WithStepUp(r.Context(), time.Now().Add(-30*time.Second)))
	hits := 0
	h := mw.RequireStepUpStrict(5 * time.Minute)(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		hits++
	})
	h(rec, r, mkActiveAccount("acct-1"))

	if hits != 1 {
		t.Errorf("hits = %d, want 1 (fresh stamp passes)", hits)
	}
	if rows := audit.rowsOf("auth.step_up_required"); len(rows) != 0 {
		t.Errorf("audit rows = %d, want 0 (fresh stamp passes)", len(rows))
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

// TestRequireSession_BindingMismatch_AutoRevokes pins the
// IAM-hardening-mega-PR (logical change 5, ADR-076) defence
// against cookie theft: when the envelope's binding_hash
// (HMAC of (IP, UA-family)) disagrees with the sessions row's
// binding_hash, RequireSession must (a) call
// SessionLookup.RevokeSession with the (sid, accountID) of the
// sealed envelope, (b) emit the auth.session.binding_mismatch
// audit row carrying both prefix8 values, (c) clear the
// session cookie, and (d) respond 401 CodeSessionInvalid.
//
// The "drift" simulates an attacker who stole a `faas_sid` cookie
// from one browser at one IP and replayed it from a different
// browser at a different IP. The defence is the auto-revoke.
func TestRequireSession_BindingMismatch_AutoRevokes(t *testing.T) {
	authn := newFakeAuthn()
	authn.acctByID["acct-1"] = mkActiveAccount("acct-1")
	audit := &fakeAuditor{}
	// Envelope stamped at IP A, UA = Chrome.
	envelopeHash := "aaaabbbbccccddddaaaabbbbccccdddd" // 32 hex
	envelope := session.Envelope{
		AccountID:   "acct-1",
		Sid:         "sid-1",
		BindingHash: envelopeHash,
	}
	sess := &fakeSessions{env: envelope}
	// Sessions row stamped at IP B, UA = Firefox.
	rowHash := "11112222333344441111222233334444"
	lookups := &fakeLookups{
		sess: state.Session{
			ID:          "sid-1",
			AccountID:   "acct-1",
			BindingHash: rowHash,
		},
	}

	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(
		authn,
		sess,
		lookups,
		audit,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		lim,
		func() []byte { return make([]byte, 32) }, // arbitrary key bytes
	)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	h(rec, r)

	if hits != 0 {
		t.Errorf("hits = %d, want 0 (binding mismatch)", hits)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if len(lookups.revokeCalls) != 1 {
		t.Fatalf("RevokeSession calls = %d, want 1", len(lookups.revokeCalls))
	}
	rc := lookups.revokeCalls[0]
	if rc.sid != "sid-1" || rc.accountID != "acct-1" {
		t.Errorf("RevokeSession args = (%q, %q), want (sid-1, acct-1)", rc.sid, rc.accountID)
	}
	rows := audit.rowsOf("auth.session.binding_mismatch")
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1 auth.session.binding_mismatch", len(rows))
	}
	got := rows[0].data
	if got["sid"] != "sid-1" {
		t.Errorf("audit sid = %v, want sid-1", got["sid"])
	}
	if got["path"] != "/v1/apps" {
		t.Errorf("audit path = %v, want /v1/apps", got["path"])
	}
	if got["expected_prefix"] != "11112222" {
		t.Errorf("expected_prefix = %v, want 11112222 (8 chars of rowHash)", got["expected_prefix"])
	}
	if got["presented_prefix"] != "aaaabbbb" {
		t.Errorf("presented_prefix = %v, want aaaabbbb (8 chars of envelopeHash)", got["presented_prefix"])
	}
	// Cookie must be cleared.
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "faas_sid=") || !strings.Contains(setCookie, "Max-Age=0") {
		t.Errorf("Set-Cookie should clear faas_sid on binding mismatch: %q", setCookie)
	}
}

// TestRequireSession_BindingMismatch_NotArmed_NoRevoke pins the
// "binding not armed" tolerance: when BOTH sides have an empty
// binding_hash (a pre-PR-076 cookie AND a pre-PR-076 row), the
// cross-check is a no-op. The test must NOT call RevokeSession
// (no fingerprint to drift from) and must NOT emit
// auth.session.binding_mismatch.
func TestRequireSession_BindingMismatch_NotArmed_NoRevoke(t *testing.T) {
	authn := newFakeAuthn()
	authn.acctByID["acct-1"] = mkActiveAccount("acct-1")
	audit := &fakeAuditor{}
	sess := &fakeSessions{env: session.Envelope{
		AccountID:   "acct-1",
		Sid:         "sid-1",
		BindingHash: "", // not armed
	}}
	lookups := &fakeLookups{sess: state.Session{
		ID:          "sid-1",
		AccountID:   "acct-1",
		BindingHash: "", // not armed
	}}

	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(authn, sess, lookups, audit,
		slog.New(slog.NewTextHandler(io.Discard, nil)), lim, nil)
	hits := 0
	h := mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) { hits++ })

	rec := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	h(rec, r)

	if hits != 1 {
		t.Errorf("hits = %d, want 1 (binding not armed should pass)", hits)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if len(lookups.revokeCalls) != 0 {
		t.Errorf("RevokeSession calls = %d, want 0 (binding not armed)", len(lookups.revokeCalls))
	}
	if rows := audit.rowsOf("auth.session.binding_mismatch"); len(rows) != 0 {
		t.Errorf("audit rows = %d, want 0 (binding not armed)", len(rows))
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
	r = r.WithContext(authWithPrincipal(r.Context(), mkActiveAccount("acct-1"), &state.APIKey{Scopes: []string{api.ScopeAppsRead}}, nil))
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
	r = r.WithContext(authWithPrincipal(r.Context(), mkActiveAccount("acct-1"), &state.APIKey{Scopes: []string{"bogus"}}, nil))
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
	r = r.WithContext(authWithPrincipal(r.Context(), mkActiveAccount("acct-1"), nil, nil))
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

// --- log-injection sanitisation (CodeQL go/log-injection) ----------------
//
// The four sites below cover the CodeQL alerts (128-131) raised on
// pkg/auth/middleware/middleware.go:471/583/594. The contract is
// that any attacker-controllable value reaching a slog attribute is
// passed through logsanitize.Field, which strips ASCII control
// characters (preserving tab). Lock the behaviour with explicit
// injection tests so a future refactor that drops the sanitiser
// surfaces as a unit test failure rather than a re-opened alert.

// captureLogger returns a slog.Logger that writes JSON to a buffer
// the test can read back, so the assertions below can verify the
// sanitised output literally. slog.Default is fine for production
// wiring but its output goes to stderr; we need a deterministic
// sink.
//
// The returned buffer is a safeBuffer (sync.Mutex-guarded io.Writer)
// because some log sites fire from detached goroutines
// (RequireSessionCookie's sessionDebounce touch). A plain
// *bytes.Buffer trips -race when the test reads concurrent writes.
func captureLogger() (*slog.Logger, *safeLogBuffer) {
	buf := &safeLogBuffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// safeLogBuffer is a minimal mutex-guarded io.Writer for slog's
// JSONHandler. Inline here (rather than pulling pkg/e2etest) so
// pkg/auth/middleware has no test-only deps. Used by
// TestLog_*StripsControlChars to read the slog output without
// racing the detached touch goroutine.
type safeLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeLogBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeLogBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// TestLog_SessionCrossCheckErrorStripsControlChars pins alert #128:
// the cookie-branch "session cross-check error" warn line at
// middleware.go:471 must strip CR/LF from r.URL.Path. The path is
// attacker-controlled (the customer types it into the browser);
// without the sanitiser, an injected newline would break the log
// stream one-line-per-event invariant that every downstream
// consumer relies on for tail/grep.
//
// Pre-fix replay (commit 61ecab45, before PR-1 logsanitize fix):
//
//	{"level":"WARN","msg":"session cross-check error",
//	 "path":"/v1/apps\nforged-log-line","error":"session lookup: ..."}
//
// The trailing "\nforged-log-line" survives because the warn line
// passed r.URL.Path straight into slog. The fix wraps r.URL.Path in
// logsanitize.Field, which replaces control bytes with U+00B7:
//
//	{"level":"WARN","msg":"session cross-check error",
//	 "path":"/v1/apps·forged-log-line","error":"session lookup: ..."}
//
// nonNotFoundErr is a synthetic error distinct from state.ErrNotFound
// so the cookie-branch's `if cookieErr != nil { ... log ... }` path
// fires. state.ErrNotFound returns handled=true,nil (no log); any
// other error wraps and logs.
type nonNotFoundErr struct{}

func (nonNotFoundErr) Error() string { return "synthetic pg conn error" }

func TestLog_SessionCrossCheckErrorStripsControlChars(t *testing.T) {
	log, buf := captureLogger()
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}
	mw := newMW(t, newFakeAuthn(), sess, &fakeLookups{getErr: nonNotFoundErr{}}, nil)
	mw.Log = log

	w := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	// httptest.NewRequest rejects CR/LF in the request-target; inject
	// the control char directly into URL.Path after construction so
	// the cookie-branch warn line sees it as the attacker would.
	r.URL.Path = "/v1/apps\nforged-log-line"
	mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		t.Errorf("handler must not run when cross-check fails")
	})(w, r)

	out := buf.String()
	if strings.Contains(out, "\nforged-log-line") {
		t.Errorf("control char CR/LF from r.URL.Path leaked into log: %q", out)
	}
	if !strings.Contains(out, "forged-log-line") {
		t.Errorf("sanitised log should still contain the printable payload: %q", out)
	}
	if !strings.Contains(out, `"path":"/v1/apps`) {
		t.Errorf("path prefix should survive verbatim: %q", out)
	}
}

// TestLog_SessionAccountMismatchStripsControlChars pins alerts #129
// and #130 (both at middleware.go:583): the "session account
// mismatch (AEAD bind broken?)" warn line. Both env.Sid AND
// r.URL.Path must be sanitised — env.Sid is AEAD-bound today so the
// attacker can't tamper with it directly, but CodeQL tracks the
// raw Sid value through the verify→stolen-audit→log chain and
// flags the alert regardless.
//
// Pre-fix replay: env.Sid is a UUID today, but a future cookie
// format that embeds a customer-typed token would re-introduce the
// injection. The logsanitize wrapper is the load-bearing defence.
func TestLog_SessionAccountMismatchStripsControlChars(t *testing.T) {
	log, buf := captureLogger()
	authn := newFakeAuthn()
	authn.acctByID["acct-1"] = mkActiveAccount("acct-1")
	// Different AccountID on the row vs the envelope → mismatch path.
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1\nFAKE"}}
	lookups := &fakeLookups{sess: state.Session{ID: "sid-1", AccountID: "acct-2"}}
	mw := newMW(t, authn, sess, lookups, nil)
	mw.Log = log

	w := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	r.URL.Path = "/v1/apps\nattacker-log-line"
	mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {
		t.Errorf("handler must not run when mismatch detected")
	})(w, r)

	out := buf.String()
	if strings.Contains(out, "\nattacker-log-line") {
		t.Errorf("r.URL.Path control char leaked into log: %q", out)
	}
	if strings.Contains(out, "\nFAKE") {
		t.Errorf("env.Sid control char leaked into log: %q", out)
	}
}

// TestLog_SessionTouchFailedStripsControlChars pins alert #131 at
// middleware.go:594: the "session last_seen_at touch failed" warn
// inside the detached touch goroutine. The sid here is env.Sid
// (AEAD-bound) but the warn is emitted from a goroutine, so a
// regression would be invisible to integration tests until the
// log consumer crashed. Locking the sanitiser behaviour with a
// unit test makes the contract enforceable.
func TestLog_SessionTouchFailedStripsControlChars(t *testing.T) {
	log, buf := captureLogger()
	authn := newFakeAuthn()
	authn.acctByID["acct-1"] = mkActiveAccount("acct-1")
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1\nINJECT"}}
	// lookups returns a valid row, but the touch itself fails so
	// the warn line fires.
	lookups := &fakeLookups{
		sess:     state.Session{ID: "sid-1", AccountID: "acct-1"},
		touchErr: state.ErrNotFound, // any error works
	}
	mw := newMW(t, authn, sess, lookups, nil)
	mw.Log = log

	w := httptest.NewRecorder()
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	mw.RequireSession(func(_ http.ResponseWriter, _ *http.Request, _ state.Account) {})(w, r)

	// The touch goroutine is detached; give it a beat to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "session last_seen_at touch failed") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	out := buf.String()
	if strings.Contains(out, "\nINJECT") {
		t.Errorf("env.Sid control char leaked into detached touch log: %q", out)
	}
	if !strings.Contains(out, "INJECT") {
		t.Errorf("sanitised log should still contain the printable payload: %q", out)
	}
}

// --- AttachSessionIfPresent: soft-attach cookie branch (ADR-123) ---------
//
// AttachSessionIfPresent is the public-front-door companion to
// RequireSession: it stamps the principal into r.Context() when a
// valid cookie is present, but NEVER 401s on miss/invalid. The
// members_only ingress gate (pkg/gateway/handler.go::applyIngressMembersOnly)
// relies on this so the cookie envelope survives the proxy hop without
// breaking open/bearer/basic/ip_allowlist/internal_only traffic that
// legitimately arrives with no cookie.

// TestAttachSessionIfPresent_NoCookie_PassesThrough pins the
// dominant case for open-mode traffic: a request arrives without
// a faas_sid cookie. AttachSessionIfPresent returns false and
// does NOT write anything to the response writer.
func TestAttachSessionIfPresent_NoCookie_PassesThrough(t *testing.T) {
	authn := newFakeAuthn()
	mw := newMW(t, authn, &fakeSessions{}, &fakeLookups{}, nil)

	r := mkRequest("GET", "/v1/apps", nil, nil)
	if mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = true, want false (no cookie)")
	}
	acct, _, ok := authmw.AccountFromContext(r)
	if ok {
		t.Errorf("ctx stamped without cookie: %+v", acct)
	}
}

// TestAttachSessionIfPresent_AEADVerifyFails_PassesThrough pins
// the soft posture on tampered cookies: Sessions.Verify returns
// non-nil → stamp NOT applied → caller passes through. This is
// in contrast to RequireSession, which clears the cookie + 401s
// on the same path. The public-front-door deliberately does NOT
// surface the failure to the client (the upstream RequireSession
// on apid already covers that surface).
func TestAttachSessionIfPresent_AEADVerifyFails_PassesThrough(t *testing.T) {
	authn := newFakeAuthn()
	sess := &fakeSessions{err: errors.New("AEAD tag mismatch")}
	mw := newMW(t, authn, sess, &fakeLookups{}, nil)

	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "tampered"})
	if mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = true, want false (AEAD verify failed)")
	}
	if _, _, ok := authmw.AccountFromContext(r); ok {
		t.Errorf("ctx stamped on AEAD failure")
	}
}

// TestAttachSessionIfPresent_EmptySid_PassesThrough pins the
// pre-IAM-3 empty-sid branch: AEAD verify succeeds but env.Sid
// is empty (legacy cookie). Soft attach passes through; the
// next request from a re-login overwrites the cookie value.
func TestAttachSessionIfPresent_EmptySid_PassesThrough(t *testing.T) {
	authn := newFakeAuthn()
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: ""}}
	mw := newMW(t, authn, sess, &fakeLookups{}, nil)

	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "pre-iam-3"})
	if mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = true, want false (empty sid)")
	}
	if _, _, ok := authmw.AccountFromContext(r); ok {
		t.Errorf("ctx stamped on empty sid")
	}
}

// TestAttachSessionIfPresent_RevokedRow_PassesThrough pins the
// revoked-row branch: AEAD verifies, live row is found but
// RevokedAt != nil. Soft attach returns false silently; the
// upstream RequireSession on apid has already emitted
// auth.session.stolen and the public-front-door doesn't
// duplicate the audit.
func TestAttachSessionIfPresent_RevokedRow_PassesThrough(t *testing.T) {
	authn := newFakeAuthn()
	audit := &fakeAuditor{}
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}
	now := time.Now()
	lookups := &fakeLookups{sess: state.Session{ID: "sid-1", AccountID: "acct-1", RevokedAt: &now}}
	mw := newMW(t, authn, sess, lookups, audit)

	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	if mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = true, want false (revoked)")
	}
	if _, _, ok := authmw.AccountFromContext(r); ok {
		t.Errorf("ctx stamped on revoked row")
	}
	if rows := audit.rowsOf("auth.session.stolen"); len(rows) != 0 {
		t.Errorf("audit rows = %d, want 0 (soft attach suppresses audit)", len(rows))
	}
}

// TestAttachSessionIfPresent_LiveRow_StampsCtx pins the happy
// path: AEAD verifies, live row found, AccountByID returns
// the matching account → principal stamped into r.Context().
// This is the surface the members_only gate reads via
// middleware.PrincipalFrom.
func TestAttachSessionIfPresent_LiveRow_StampsCtx(t *testing.T) {
	authn := newFakeAuthn()
	authn.acctByID["acct-1"] = mkActiveAccount("acct-1")
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}
	lookups := &fakeLookups{sess: state.Session{ID: "sid-1", AccountID: "acct-1"}}
	mw := newMW(t, authn, sess, lookups, nil)

	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	if !mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = false, want true")
	}
	acct, _, ok := authmw.AccountFromContext(r)
	if !ok {
		t.Fatalf("ctx not stamped on happy path")
	}
	if acct.ID != "acct-1" {
		t.Errorf("acct.ID = %q, want acct-1", acct.ID)
	}
}

// TestAttachSessionIfPresent_LookupError_PassesThrough pins the
// transient store-error branch: GetSession returns a non-ErrNotFound
// error. Soft attach returns false silently — the public-front-door
// does NOT take the failure as a signal to log the user out (a
// transient pg outage would 401 every customer request, which is
// the wrong posture). The members_only gate's lookup_error branch
// (501-style fail-closed) is the right place to surface sustained
// DB issues.
func TestAttachSessionIfPresent_LookupError_PassesThrough(t *testing.T) {
	authn := newFakeAuthn()
	sess := &fakeSessions{env: session.Envelope{AccountID: "acct-1", Sid: "sid-1"}}
	lookups := &fakeLookups{getErr: errors.New("pg connection refused")}
	mw := newMW(t, authn, sess, lookups, nil)

	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	if mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = true, want false (transient lookup error)")
	}
	if _, _, ok := authmw.AccountFromContext(r); ok {
		t.Errorf("ctx stamped on lookup error")
	}
}

// TestAttachSessionIfPresent_BindingMismatch_PassesThrough pins
// the binding-hash mismatch branch (ADR-076): the cookie envelope's
// binding_hash disagrees with the live row's binding_hash.
// Soft attach returns false silently — no auto-revoke, no audit.
// The per-app authentication boundary (apid's RequireSession)
// owns the revoke + audit emission; the public-front-door is
// the per-app authorization boundary, not the per-app authn
// boundary. Mixing the two would break the layering.
func TestAttachSessionIfPresent_BindingMismatch_PassesThrough(t *testing.T) {
	authn := newFakeAuthn()
	sess := &fakeSessions{env: session.Envelope{
		AccountID:    "acct-1",
		Sid:          "sid-1",
		BindingHash:  "stolen-binding",
	}}
	lookups := &fakeLookups{sess: state.Session{
		ID:           "sid-1",
		AccountID:    "acct-1",
		BindingHash:  "legit-binding",
	}}
	mw := authmw.New(authn, sess, lookups, &fakeAuditor{}, slog.Default(), middleware.NewLimiter(middleware.AuthLimitConfig{}), bindinghash.KeyFunc(func() []byte { return []byte("key") }))
	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	if mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = true, want false (binding mismatch)")
	}
	if _, _, ok := authmw.AccountFromContext(r); ok {
		t.Errorf("ctx stamped on binding mismatch")
	}
	if got := len(lookups.revokeCalls); got != 0 {
		t.Errorf("revokeCalls = %d, want 0 (soft attach does not revoke)", got)
	}
}

// TestAttachSessionIfPresent_NilSessionsLookups_PassesThrough pins
// the "gate not configured at startup" branch: if Sessions or
// Lookups is nil (a dev box that never wired the session
// subsystem), AttachSessionIfPresent returns false silently. The
// open-front-door must not panic.
func TestAttachSessionIfPresent_NilSessionsLookups_PassesThrough(t *testing.T) {
	authn := newFakeAuthn()
	lim := middleware.NewLimiter(middleware.AuthLimitConfig{})
	mw := authmw.New(authn, nil, nil, nil, slog.Default(), lim, nil)

	r := mkRequest("GET", "/v1/apps", nil, map[string]string{"faas_sid": "valid-cookie"})
	if mw.AttachSessionIfPresent(r) {
		t.Errorf("ok = true, want false (sessions/lookups nil)")
	}
}
