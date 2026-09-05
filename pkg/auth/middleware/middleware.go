// Package middleware lifts the authentication + authorization
// middleware out of cmd/apid so cmd/gatewayd-internal/(and any future daemon
// that needs to authenticate a customer request) can compose the
// same chain without duplicating cmd/apid's auth surface.
//
// The shape is the five pieces that today wrap every /v1/* route in
// cmd/apid (cmd/apid/server.go, cmd/apid/mfa_middleware.go,
// cmd/apid/session_middleware.go, cmd/apid/handlers_auth.go):
//
//   - RequireSession — authenticates the request via bearer-key or
//     session-cookie. Stamps the resolved Account (+ optional
//     APIKey) into r.Context() via withPrincipal.
//   - RequireMFA — gates the route on MFA-completion when the
//     session stamp carries a pending flag (per IAM-2 design
//     decision 3, API keys bypass MFA).
//   - RequireScope — enforces the api.Scope* vocabulary on the route,
//     with session-cookie principals implicitly admin.
//   - RequireLimited — wraps RequireSession in pkg/middleware.AuthLimit
//     so /v1/* routes share the spec §11 10-failures-per-minute-per-IP
//     bucket.
//   - LoadApp — IDOR-safe slug→App resolver (ownership predicate).
//
// ADR-046 records the architectural decision. The lift preserves
// the cmd/apid semantics 1:1 — same 401, same 402, same 403, same
// audit rows, same headers — so PR-2 (gatewayd-internal AppLogsHandler) and
// any future component can depend on pkg/auth without re-deriving
// the chain.
//
// Why a typed AccountHandler instead of http.Handler: the chain
// threads the resolved state.Account through three layers
// (RequireSession → RequireMFA → RequireScope → handler). An
// http.Handler boundary forces the Account lookup to be re-derived
// from the context inside every handler; the existing wiring avoids
// this by passing the value directly. pkg/auth exposes the same
// shape so the facade in cmd/apid/auth_facade.go is a one-line
// pass-through.
//
// Pointer mutation (issue #278): RequireSession mutates *r in
// place via `*r = *r.WithContext(...)` so the request seen by the
// outer observer (observeWrap in cmd/apid) carries the principal.
// Without this, observers reading via principalFrom(r) always see
// ok=false and the per-customer request-failure counter would be
// useless. PR-332 is the relevant precedent.
package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindinghash"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// --- interfaces ----------------------------------------------------------

// Authenticator is the subset of pkg/state.Store that the
// authentication middleware needs. Defined here so the concrete
// store wiring stays in cmd/apid and cmd/gatewayd-internal/ pkg/auth doesn't
// import pkg/state's PgStore directly (that would couple auth to a
// specific store implementation and make unit tests impossible
// without PG).
//
// The four methods map 1:1 to store calls cmd/apid's s.auth and
// s.loadApp already make.
type Authenticator interface {
	// AuthenticateKey returns the Account + APIKey for a hashed
	// bearer token. state.ErrNotFound = invalid key. The caller
	// (RequireSession) maps ErrNotFound to 401.
	AuthenticateKey(ctx context.Context, hash []byte) (state.Account, state.APIKey, error)
	// AuthenticateOIDCBearer returns the Account + APIKey for an
	// OIDC-derived short-lived bearer (issue #270 / ADR-101). The
	// returned APIKey is a synthetic projection (state.APIKey struct
	// with Scopes=["deploy:write"], Status="active") so the principal
	// stamp + downstream requireScope chain works unchanged. The
	// hash lookup hits oidc_exchanged_tokens.token_hash (UNIQUE
	// index); rows past ExpiresAt return state.ErrNotFound so the
	// 5-min TTL is the natural expiry path. state.ErrNotFound =
	// unknown / expired token. The caller maps ErrNotFound to 401.
	AuthenticateOIDCBearer(ctx context.Context, hash []byte) (state.Account, state.APIKey, error)
	// AccountByID is the post-session-verify account lookup.
	// state.ErrNotFound = unknown account (defensive — should not
	// happen since the AEAD binds AccountID into the envelope).
	AccountByID(ctx context.Context, id string) (state.Account, error)
	// AppBySlug is the IDOR-safe slug→App lookup. LoadApp wraps it
	// with the ownership predicate (app.AccountID == acct.ID); the
	// predicate is in pkg/auth, not in the store, because ownership
	// is an authorization concern, not a storage concern.
	AppBySlug(ctx context.Context, slug string) (state.App, error)
	// TouchKeyLastUsed stamps last_used_at on the api_key row.
	// MUST be detached + bounded (2s timeout) — the caller fires
	// it in a goroutine and the request context must not gate it.
	// Not invoked by the OIDC branch — a 5-min TTL row would
	// dominate write load if every request stamped last_used_at.
	TouchKeyLastUsed(ctx context.Context, keyID string) error
}

// Sessions is the subset of pkg/session.Manager.Verify that the
// session-cookie branch of RequireSession needs. Today that's one
// method; the interface exists so tests can inject a fake without
// dragging in AEAD plumbing.
type Sessions interface {
	Verify(value string) (session.Envelope, error)
}

// SessionLookup is the live-session-row check IAM-3 (ADR-039)
// requires. The seal accepts AEAD-bound envelopes regardless of DB
// state; the cross-check against the sessions table is the load-
// bearing defense against stolen-cookie replay. cmd/apid's
// *state.Store already provides this; gatewayd-internal's fake store will
// too. The error contract: state.ErrNotFound ⇒ session unknown /
// revoked (caller maps to 401); any other error is an operational
// failure (caller logs + 401).
type SessionLookup interface {
	GetSession(ctx context.Context, sid string) (state.Session, error)
	TouchSessionLastSeen(ctx context.Context, sid string) error
	// RevokeSession is the IAM-hardening-mega-PR (logical change 5)
	// auto-revoke path. The middleware calls it when the live
	// binding-hash check fails — the stolen-cookie defence
	// (ADR-076). Returns the underlying state.Store semantics.
	RevokeSession(ctx context.Context, id, accountID string) (bool, error)
}

// Auditor is the subset of pkg/audit.Auditor.Emit that the
// middleware needs for IAM-4 audit rows (auth.mfa_gate_hit,
// auth.session.stolen, etc.). The interface keeps pkg/auth free of
// cmd/apid's auditor type.
type Auditor interface {
	Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
}

// --- typed handler -------------------------------------------------------

// AccountHandler is the typed handler signature that
// RequireMFA + RequireScope compose with. Today cmd/apid calls it
// `accountHandler` (cmd/apid/server.go:1233) — the same shape is
// exported here so cmd/apid can use pkg/auth without renaming its
// internal closure type.
type AccountHandler func(w http.ResponseWriter, r *http.Request, acct state.Account)

// --- Middleware ----------------------------------------------------------

// Middleware is the auth constructor. cmd/apid and cmd/gatewayd-internal/
// each build one from their own dependency set and pass it down to
// the handler chain.
//
// The Limiter field is shared across every /v1/* route on the
// daemon — spec §11 enforces 10 failed auths per minute per IP
// across the whole API surface, not per (IP, endpoint). Tests
// inject a fresh limiter per test so the bucket doesn't bleed
// across cases.
type Middleware struct {
	Authn    Authenticator
	Sessions Sessions
	Lookups  SessionLookup       // live-session-row check; nil disables RequireSession's cookie branch
	Audit    Auditor             // nil-safe; nil disables audit emit
	Log      *slog.Logger        // nil-safe; nil disables the Warn path
	Limiter  *middleware.Limiter // shared per-IP bucket for RequireLimited

	// CookieDomain is the cookie attribute on the session-cookie
	// issued/cleared by RequireSession. Empty = omit Domain
	// (host-only cookie; dev/test default).
	CookieDomain string

	// SessionCookieName is the cookie name RequireSession reads
	// (and clearSessionCookie writes). Empty = "faas_sid".
	SessionCookieName string

	// keyDebounce gates per-key last_used_at touches to one
	// fire per keyTouchWindow. Hot keys (high-RPS cron apps)
	// would mint 1k+ UPDATEs/sec on api_keys.last_used_at
	// without this; with it the working set is "active keys in
	// the last 30 s" per the eviction rule in TouchTicket.
	keyDebounce keyTouchDebounce

	// sessionDebounce gates per-sid last_seen_at touches to one
	// fire per sessionTouchWindow. Same pattern as keyDebounce.
	sessionDebounce sessionTouchDebounce

	// keyTouchWindowForTest overrides the production 30s window
	// when non-zero. Tests set this to a small value (50ms) to
	// exercise the eviction contract without sleeping half a
	// minute per case.
	keyTouchWindowForTest time.Duration
	// sessionTouchWindowForTest: same override for sessions.
	sessionTouchWindowForTest time.Duration
	// BindingKeyFn is the IAM-hardening-mega-PR (logical change 5,
	// ADR-076) HMAC key source. nil disables binding-hash
	// computation (the envelope's `binding_hash` field is omitted;
	// the cross-check is skipped). Production wires this to the
	// first 32 bytes of the session-key AEAD secret.
	BindingKeyFn func() []byte
}

// keyTouchWindow returns the production 30s window unless an
// in-package test override has been set on keyTouchWindowForTest.
func (m *Middleware) keyTouchWindow() time.Duration {
	if m.keyTouchWindowForTest > 0 {
		return m.keyTouchWindowForTest
	}
	return keyTouchWindow
}

// sessionTouchWindow returns the production 5min window unless
// an in-package test override has been set.
func (m *Middleware) sessionTouchWindow() time.Duration {
	if m.sessionTouchWindowForTest > 0 {
		return m.sessionTouchWindowForTest
	}
	return sessionTouchWindow
}

// --- test seams (NOT production API) -------------------------------------
//
// The eight symbols below (KeyDebounceMapSize, SessionDebounceMapSize,
// KeyTouchWindowForTest, SessionTouchWindowForTest, KeyTouchWindow,
// SessionTouchWindow, KeyDebounceShouldTouch, SessionDebounceShouldTouch)
// and TouchTicket.FiredAtSet exist ONLY so whitebox tests in
// package middleware_test can observe + drive the debouncer
// election + eviction contract without becoming internal-package
// tests (which would force them to rebuild helpers that already
// live in middleware_test.go).
//
// Production code MUST NOT call any of these. The intended
// consumer is pkg/auth/middleware/debounce_whitebox_test.go.
// A future refactor that consolidates these into a single
// (*Middleware).ForTest helper is welcome; the wide surface is a
// stopgap so each test case can name exactly the seam it needs.

// KeyDebounceMapSize returns the current size of the per-key
// debounce map. Exported for whitebox tests that pin the eviction
// contract (pkg/auth/middleware/debounce_whitebox_test.go).
func (m *Middleware) KeyDebounceMapSize() int {
	n := 0
	m.keyDebounce.tickets.Range(func(_, _ any) bool { n++; return true })
	return n
}

// SessionDebounceMapSize returns the current size of the per-sid
// debounce map. Exported for whitebox tests.
func (m *Middleware) SessionDebounceMapSize() int {
	n := 0
	m.sessionDebounce.tickets.Range(func(_, _ any) bool { n++; return true })
	return n
}

// KeyTouchWindowForTest overrides the production 30s window.
// Tests set a small value (e.g. 30ms) so they can validate the
// eviction contract in milliseconds.
func (m *Middleware) KeyTouchWindowForTest(d time.Duration) { m.keyTouchWindowForTest = d }

// SessionTouchWindowForTest overrides the production 5min window.
func (m *Middleware) SessionTouchWindowForTest(d time.Duration) { m.sessionTouchWindowForTest = d }

// KeyTouchWindow returns the active key debounce window.
func (m *Middleware) KeyTouchWindow() time.Duration { return m.keyTouchWindow() }

// SessionTouchWindow returns the active session debounce window.
func (m *Middleware) SessionTouchWindow() time.Duration { return m.sessionTouchWindow() }

// KeyDebounceShouldTouch wraps the unexported debouncer
// shouldTouch so whitebox tests in package middleware_test can
// drive the election contract without making every test an
// internal-package test (which would force them to rebuild
// helpers that already exist in the blackbox file).
func (m *Middleware) KeyDebounceShouldTouch(keyID string, now time.Time) (*TouchTicket, bool) {
	return m.keyDebounce.shouldTouch(keyID, now, m.keyTouchWindow())
}

// SessionDebounceShouldTouch mirrors KeyDebounceShouldTouch for
// the per-sid debouncer.
func (m *Middleware) SessionDebounceShouldTouch(sid string, now time.Time) (*TouchTicket, bool) {
	return m.sessionDebounce.shouldTouch(sid, now, m.sessionTouchWindow())
}

// FiredAtSet backdates the ticket's firedAt stamp. Test-only seam
// for the CAS-replace invariant: a test stamps a past time, then
// calls shouldTouch again to drive the CAS-replace branch and
// validate that the stale ticket's eventual CompareAndDelete is a
// no-op (it doesn't delete the freshly-stamped entry).
func (t *TouchTicket) FiredAtSet(ts time.Time) { t.firedAt.Store(&ts) }

// New returns the Middleware value.
//
// Required:
//
//   - authn — every method reads from it. nil → panic.
//   - limiter — shared per-IP bucket for RequireLimited. nil → panic;
//     pass middleware.NewLimiter(cfg).
//
// Optional (nil tolerated, corresponding paths guarded):
//
//   - sessions + lookups — together they enable the cookie branch
//     of RequireSession (the AEAD-verify + IAM-3 live-row cross-
//     check). Either nil disables the cookie branch: a request with
//     a session cookie falls through to the no-credentials 401.
//     Production callers wire both; tests that only exercise the
//     bearer branch pass nil/nil.
//   - auditor — nil disables audit-row emission. RequireMFA's
//     auth.mfa_gate_hit row, the session-stolen row, and the
//     bearer-inactive row are all gated on this. Production always
//     passes a non-nil auditor; unit tests pass nil.
//   - log — nil disables the Warn path. The middleware never logs
//     anything at Info or above; Warn is for cross-check failures
//     and detached-touch errors.
func New(authn Authenticator, sessions Sessions, lookups SessionLookup,
	auditor Auditor, log *slog.Logger, limiter *middleware.Limiter, bindingKeyFn bindinghash.KeyFunc) *Middleware {
	if authn == nil {
		panic("auth: nil Authenticator")
	}
	if limiter == nil {
		panic("auth: nil Limiter (RequireLimited cannot share an empty bucket; pass middleware.NewLimiter(cfg))")
	}
	return &Middleware{
		Authn:             authn,
		Sessions:          sessions,
		Lookups:           lookups,
		Audit:             auditor,
		Log:               log,
		Limiter:           limiter,
		BindingKeyFn:      bindingKeyFn,
		SessionCookieName: defaultSessionCookieName,
	}
}

const defaultSessionCookieName = "faas_sid"

// keyTouchWindow is the per-key debounce window for last_used_at
// touches. 30s matches cmd/apid's server.touchDebounce. Hot keys
// (high-RPS cron apps) hit this ceiling; without the debounce a
// sustained 1k RPS would mint 1k UPDATEs/sec on the api_keys row.
const keyTouchWindow = 30 * time.Second

// keyTouchDebounce is the per-key debounce map. sync.Map idiom;
// reads are lock-free, writes go through CompareAndSwap so two
// concurrent first-time callers don't both schedule a touch.
//
// Cleanup: every accepted touch is keyed by a fresh *TouchTicket
// pointer. The firing goroutine calls ticket.AfterFire(window) which
// sleeps for the window then atomically deletes the entry via
// CompareAndDelete IFF no concurrent firer has stamped a newer
// ticket in the meantime (pointer-identity check). Working set
// stays at "active keys in the last 30 s" rather than "all keys
// ever authenticated".
type keyTouchDebounce struct {
	tickets sync.Map // key string → *TouchTicket
}

// TouchTicket carries the map reference + the key id so AfterFire
// can atomically delete the entry without the caller threading
// extra arguments through the detached-goroutine path.
//
// Exported solely for whitebox tests in package middleware_test
// (debounce_whitebox_test.go). Production code MUST NOT construct
// or read *TouchTicket values; the only consumer-facing surface is
// the debouncer's shouldTouch entry point.
type TouchTicket struct {
	m       *sync.Map // pointer to debouncer.tickets; load-bearing for CompareAndDelete
	id      string    // map key for CompareAndDelete
	firedAt atomic.Pointer[time.Time]
}

// AfterFire sleeps for window then atomically deletes the
// debouncer's map entry IFF the stored ticket still matches this
// pointer. A fresher firer stamping its own ticket leaves this
// goroutine's CompareAndDelete as a no-op — pointer-identity is the
// eviction version. Pre-extraction cmd/apid used the same shape
// (server.touchDebounce + the deleted session_middleware.go
// sessionTouchDebounce).
func (t *TouchTicket) AfterFire(window time.Duration) {
	if t == nil {
		return
	}
	now := time.Now()
	t.firedAt.Store(&now)
	time.Sleep(window)
	t.m.CompareAndDelete(t.id, t)
}

// shouldTouch elects exactly one firer per window and returns a
// *TouchTicket; the firing goroutine calls ticket.AfterFire at the
// end (success or failure) so the per-key map entry is removed
// keyTouchWindow later.
func (d *keyTouchDebounce) shouldTouch(keyID string, now time.Time, window time.Duration) (*TouchTicket, bool) {
	if existing, ok := d.tickets.Load(keyID); ok {
		t := existing.(*TouchTicket)
		if last := t.firedAt.Load(); last != nil && now.Sub(*last) < window {
			return nil, false
		}
		// Stale ticket: CAS-replace so the next firer wins
		// without losing the election-on-window-elapsed property.
		// If a concurrent firer already swapped the ticket in
		// between, our CompareAndSwap fails and we read the
		// winner — fall through to the conditional second check
		// so the post-CAS ticket is honored.
		fresh := &TouchTicket{m: &d.tickets, id: keyID}
		fresh.firedAt.Store(&now)
		if d.tickets.CompareAndSwap(keyID, t, fresh) {
			return fresh, true
		}
		// CAS-lose: a concurrent firer already installed a
		// winner. We re-load and re-check the window — if the
		// winner is fresh (within window) we yield; if the
		// winner is itself stale we still want SOMEONE to fire
		// so the touch isn't lost, hence the unconditional
		// Store below. The two Store paths (CAS-win vs fall-
		// through) are NOT duplicates: the CAS path installs
		// the fresh ticket we just built; the fall-through path
		// is the "everyone else gave up, last firer wins" branch.
		if cur, ok := d.tickets.Load(keyID); ok {
			latest := cur.(*TouchTicket)
			if last := latest.firedAt.Load(); last != nil && now.Sub(*last) < window {
				return nil, false
			}
		}
	}
	t := &TouchTicket{m: &d.tickets, id: keyID}
	t.firedAt.Store(&now)
	d.tickets.Store(keyID, t)
	return t, true
}

// --- RequireSession ------------------------------------------------------

// RequireSession authenticates the request via bearer-key or
// session-cookie. On success stamps principal{Acct, Key, Membership=nil} into
// r.Context() via withPrincipal and calls next. On failure writes
// a 401 problem (or 402 for inactive accounts).
//
// Carve-out for ADR-021: while an account is in deleted_pending,
// the customer still needs to reach
//
//	GET    /v1/account          (Whoami)
//	GET    /v1/account/export   (final export during grace)
//	DELETE /v1/account          (idempotent re-DEL)
//	POST   /v1/account/restore  (cancel the deletion)
//
// All other routes still 402 with CodeBillingPastDue during grace.
//
// Mirrors cmd/apid/server.go:1313 (PR #180 / PR #244).
func (m *Middleware) RequireSession(next AccountHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// (1) Bearer-key branch — fastest path, no DB cross-check.
		tok := bearerToken(r)
		if api.ValidAPIKeyFormat(tok) {
			acct, key, err := m.Authn.AuthenticateKey(r.Context(), api.HashAPIKey(tok))
			// IAM-5 (issue #189): translate the IAM-5 sentinels
			// to RFC 7807 + emit the audit row. The store
			// already lazily marked the key status='revoked'
			// for the expired case; we just translate the
			// error and emit the audit event from here so the
			// store stays independent of the audit seam.
			if err != nil {
				if errors.Is(err, state.ErrAPIKeyExpired) {
					m.Audit.Emit(r.Context(), "key.expired", nil, map[string]any{
						"key_id": key.ID,
					})
					api.WriteProblem(w, api.ErrAPIKeyExpired())
					return
				}
				if errors.Is(err, state.ErrAPIKeyRevoked) {
					m.Audit.Emit(r.Context(), "key.auth_rejected_revoked", nil, map[string]any{
						"key_id": key.ID,
					})
					api.WriteProblem(w, api.ErrAPIKeyRevoked())
					return
				}
				// Fall through to the session-cookie branch —
				// the key may have been deleted or the hash is
				// simply unknown; both surface as "invalid
				// bearer" through the legacy 401 path.
			} else {
				if !acct.Active() {
					if acct.Status != state.AccountDeletedPending || !isAccountScopedPath(r.URL.Path) {
						api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue,
							"Account suspended", "resolve billing to continue: https://"+wire.DocsHost+"/billing"))
						return
					}
				}
				*r = *r.WithContext(withPrincipal(r.Context(), principal{Acct: acct, Key: &key, Membership: nil}))
				// TouchKeyLastUsed is observability, not auth —
				// detached context + bounded timeout so a slow PG
				// cannot block the user's request, and a canceled
				// client (tab close, SSE disconnect) still leaves
				// a stamp.
				if t, fire := m.keyDebounce.shouldTouch(key.ID, time.Now(), m.keyTouchWindow()); fire {
					//nolint:contextcheck // detached-context is load-bearing; matches cmd/apid.
					go func(parent context.Context, id string, ticket *TouchTicket) {
						defer ticket.AfterFire(m.keyTouchWindow())
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						if err := m.Authn.TouchKeyLastUsed(ctx, id); err != nil && m.Log != nil {
							m.Log.Warn("api_key last_used_at touch failed", "key_id", id, "error", err.Error())
						}
					}(r.Context(), key.ID, t)
				}
				next(w, r, acct)
				return
			}
		}

		// (1b) OIDC-derived short-lived bearer branch
		// (issue #270 / ADR-101). Disjoint prefix from ValidAPIKeyFormat
		// so the two checks never cross-match; same bearer-token
		// extraction as the fp_live_ branch. The synthetic principal
		// carries Scopes=["deploy:write"] (per ADR-101 customer-locked
		// decision) so requireScope(ScopesDeployWriteSurface) works
		// unchanged downstream.
		//
		// What this branch does NOT do:
		//   - TouchKeyLastUsed — a 5-min TTL row would dominate
		//     write load if every CI request stamped last_used_at.
		//     The audit row (auth.token.exchanged, emitted at mint
		//     time in pkg/oidc/handler.go) is the durable record.
		//   - emit any audit here — mint-time audit (in pkg/oidc) is
		//     the contract. This branch only verifies + stamps.
		//
		// What it DOES do:
		//   - Same Active() short-circuit as the fp_live_ branch
		//     (billing-past-due 402).
		//   - Same pointer-mutation contract (withPrincipal writes
		//     into r.Context() so observeWrap in cmd/apid sees the
		//     principal via principalFrom(r)).
		if api.ValidOIDCKeyFormat(tok) {
			acct, key, err := m.Authn.AuthenticateOIDCBearer(r.Context(), api.HashAPIKey(tok))
			if err == nil {
				if !acct.Active() {
					if acct.Status != state.AccountDeletedPending || !isAccountScopedPath(r.URL.Path) {
						api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue,
							"Account suspended", "resolve billing to continue: https://"+wire.DocsHost+"/billing"))
						return
					}
				}
				*r = *r.WithContext(withPrincipal(r.Context(), principal{Acct: acct, Key: &key, Membership: nil}))
				// No withMFAPending — bearer principals bypass MFA
				// (IAM-2 design decision 3; same posture as the
				// fp_live_ branch above).
				next(w, r, acct)
				return
			}
			// ErrNotFound → fall through to the cookie branch (the
			// same "the bearer may have been deleted or simply
			// unknown" semantics as the fp_live_ branch's
			// non-sentinel errors at line 467). The 5-min TTL is
			// handled inside the store (GetByHash returns
			// ErrNotFound for rows past ExpiresAt), so a stale OIDC
			// bearer naturally falls through and 401s via the
			// no-credentials terminal below.
		}

		// (2) Session-cookie branch — verify AEAD, cross-check
		//     live row, stamp principal.
		if m.Sessions != nil && m.Lookups != nil {
			if c, err := r.Cookie(m.SessionCookieName); err == nil && c.Value != "" {
				env, err := m.Sessions.Verify(c.Value)
				if err == nil {
					sess, handled, cookieErr := m.RequireSessionCookie(w, r, env)
					if cookieErr != nil {
						if m.Log != nil {
							m.Log.Warn("session cross-check error",
								"path", logsanitize.Field(r.URL.Path), "error", cookieErr.Error())
						}
						m.clearSessionCookie(w)
						api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
							api.CodeSessionExpired, "Session expired",
							"session validation failed; sign in again"))
						return
					}
					if handled {
						return
					}
					// Stamp the live session row onto the local r
					// so AccountByID (below) sees it via the same
					// context the cookie branch built. The rebind
					// is intentionally discarded after this block
					// — the *r mutations below write the principal
					// + mfa-pending flag directly into the OUTER
					// request so observeWrap (cmd/apid) sees them
					// via principalFrom(r) and MFAPendingFrom(r).
					// See the package doc on the pointer-mutation
					// contract (issue #278 / PR #332).
					//nolint:contextcheck // pointer-mutation contract: r.Context() must be the inherited ctx; capturing into a local breaks observeWrap.
					r = r.WithContext(withSession(r.Context(), sess))
					//nolint:contextcheck // same pointer-mutation contract: AccountByID reads from r.Context() so the returned principal stamps into the same ctx as withPrincipal below.
					if acct, err := m.Authn.AccountByID(r.Context(), env.AccountID); err == nil {
						if !acct.Active() {
							if acct.Status != state.AccountDeletedPending || !isAccountScopedPath(r.URL.Path) {
								api.WriteProblem(w, api.NewProblem(http.StatusPaymentRequired, api.CodeBillingPastDue,
									"Account suspended", "resolve billing to continue: https://"+wire.DocsHost+"/billing"))
								return
							}
						}
						//nolint:contextcheck // same pointer-mutation contract: withPrincipal derives from r.Context() so the principal stamps into the OUTER r, not a captured local ctx.
						*r = *r.WithContext(withPrincipal(r.Context(), principal{Acct: acct, Key: nil, Membership: nil}))
						//nolint:contextcheck // same pointer-mutation contract: withMFAPending derives from r.Context() so the flag stamps into the OUTER r.
						*r = *r.WithContext(withMFAPending(r.Context(), session.IsMFAPending(env)))
						// IAM-hardening-mega-PR (logical change 6,
						// ADR-077): stamp the step-up timestamp the
						// /v1/account/mfa/verify handler last sealed
						// onto the envelope so RequireStepUp can read
						// it on the next gated request. Zero-value
						// stamps bypass the gate (legacy cookies —
						// rolling migration; see RequireStepUp).
						*r = *r.WithContext(WithStepUp(r.Context(), env.StepUpAt))
						next(w, r, acct)
						return
					}
					// AccountByID err: AEAD-bound env.AccountID
					// should never miss; if it does, treat as
					// session-expired (defensive — never leak
					// existence).
					m.clearSessionCookie(w)
					api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
						api.CodeSessionExpired, "Session expired",
						"account lookup failed; sign in again"))
					return
				}
			}
		}

		// (3) No credentials.
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Unauthorized", "provide a valid API key as a Bearer token or sign in via session cookie"))
	}
}

// RequireSessionCookie is the live-row cross-check (IAM-3).
// Returns (Session{}, handled=true, nil) on any 401 path — the
// caller stops. Returns (sess, handled=false, nil) on success.
// Returns (Session{}, handled=true, err) on a real DB failure.
//
// Exported because cross-package tests (cmd/apid/handlers_sessions_test.go)
// need to exercise the defensive branches (account-mismatch,
// found-revoked, empty-sid) with a forged envelope. Production
// callers go through RequireSession (the cookie branch invokes
// this directly).
func (m *Middleware) RequireSessionCookie(w http.ResponseWriter,
	r *http.Request, env session.Envelope) (state.Session, bool, error) {
	ctx := r.Context()
	// (1) empty sid = pre-IAM-3 rollout cookie. Fail closed.
	if env.Sid == "" {
		m.clearSessionCookie(w)
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeSessionExpired,
			"Session expired", "this dashboard session was issued before the session-revocation rollout; sign in again"))
		return state.Session{}, true, nil
	}
	// (2) row lookup. ErrNotFound = never-valid sid.
	sess, err := m.Lookups.GetSession(ctx, env.Sid)
	if err != nil {
		m.clearSessionCookie(w)
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeSessionExpired,
			"Session expired", "this dashboard session has been revoked; sign in again"))
		if errors.Is(err, state.ErrNotFound) {
			return state.Session{}, true, nil
		}
		return state.Session{}, true, fmt.Errorf("session lookup: %w", err)
	}
	// (3) revoked-row = possibly-stolen cookie. Distinct audit.
	if sess.RevokedAt != nil {
		m.clearSessionCookie(w)
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeSessionExpired,
			"Session expired", "this dashboard session has been revoked; sign in again"))
		if m.Audit != nil {
			m.Audit.Emit(ctx, "auth.session.stolen", &env.AccountID, map[string]any{
				"sid":    env.Sid,
				"method": r.Method,
				"path":   r.URL.Path,
			})
		}
		return state.Session{}, true, nil
	}
	// (3.5) binding-hash mismatch = stolen-cookie auto-revoke
	// (IAM hardening mega-PR, logical change 5, ADR-076).
	// Compare the envelope's binding_hash (sealed by AEAD at mint
	// time) against the sessions row's binding_hash column.
	// Either side empty = "binding not armed" (pre-PR-076 cookie
	// or unix-socket code path); cross-check is skipped in that
	// case. Mismatch ⇒ auto-revoke + audit + 401.
	if m.BindingKeyFn != nil && env.BindingHash != "" && sess.BindingHash != "" &&
		env.BindingHash != sess.BindingHash {
		// Best-effort revoke. Failure is logged but the
		// 401 still returns — the request is rejected
		// regardless of the revoke's success.
		if _, revokeErr := m.Lookups.RevokeSession(ctx, env.Sid, env.AccountID); revokeErr != nil && m.Log != nil {
			m.Log.Warn("session binding-mismatch auto-revoke failed",
				"sid", logsanitize.Field(env.Sid),
				"path", logsanitize.Field(r.URL.Path),
				"error", revokeErr.Error())
		}
		// Audit emit (best-effort — never blocks the 401).
		if m.Audit != nil {
			m.Audit.Emit(ctx, "auth.session.binding_mismatch", &env.AccountID, map[string]any{
				"sid":              env.Sid,
				"method":           r.Method,
				"path":             r.URL.Path,
				"expected_prefix":  prefix8(sess.BindingHash),
				"presented_prefix": prefix8(env.BindingHash),
			})
		}
		m.clearSessionCookie(w)
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeSessionInvalid,
			"Session invalid", "session binding mismatch — possible replay; sign in again"))
		return state.Session{}, true, nil
	}
	// (4) account-mismatch is defensive — the AEAD binds
	// AccountID into the same ciphertext as Sid, so a mismatch
	// implies an AEAD forgery. 401.
	if sess.AccountID != env.AccountID {
		m.clearSessionCookie(w)
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeSessionInvalid,
			"Session invalid", "session and account binding mismatch"))
		if m.Log != nil {
			m.Log.Warn("session account mismatch (AEAD bind broken?)",
				"sid", logsanitize.Field(env.Sid), "path", logsanitize.Field(r.URL.Path))
		}
		return state.Session{}, true, nil
	}
	// (5) stamp + async touch. Detached; bounded.
	if t, fire := m.sessionDebounce.shouldTouch(env.Sid, time.Now(), m.sessionTouchWindow()); fire {
		go func(parentCtx context.Context, sid string, ticket *TouchTicket) {
			defer ticket.AfterFire(m.sessionTouchWindow())
			c, cancel := context.WithTimeout(parentCtx, 2*time.Second)
			defer cancel()
			if err := m.Lookups.TouchSessionLastSeen(c, sid); err != nil && m.Log != nil {
				m.Log.Warn("session last_seen_at touch failed", "sid", logsanitize.Field(sid), "error", err.Error())
			}
		}(ctx, env.Sid, t)
	}
	return sess, false, nil
}

const sessionTouchWindow = 5 * time.Minute

// sessionTouchDebounce mirrors keyTouchDebounce but keyed on the
// session id (sid) rather than the API key id. Same pointer-version
// CompareAndDelete contract (see TouchTicket.AfterFire).
//
// TODO(extract-debouncer): sessionTouchDebounce.shouldTouch is
// byte-identical to keyTouchDebounce.shouldTouch except for the
// key-name parameter (sid vs keyID). A small generic helper
// typed on string (or a mapDebounce[K ~string] type parameter)
// would compress ~50 LOC and ensure the election / eviction
// contract evolves in lockstep. Follow-up refactor — not
// required for PR-1 because the duplication is so thin
// (one parameter name) that the abstraction is arguable.
type sessionTouchDebounce struct {
	tickets sync.Map // sid string → *TouchTicket
}

func (d *sessionTouchDebounce) shouldTouch(sid string, now time.Time, window time.Duration) (*TouchTicket, bool) {
	if existing, ok := d.tickets.Load(sid); ok {
		t := existing.(*TouchTicket)
		if last := t.firedAt.Load(); last != nil && now.Sub(*last) < window {
			return nil, false
		}
		// Stale ticket: CAS-replace so a concurrent firer that
		// already swapped the ticket wins; if our CAS loses,
		// fall through to the second conditional check on the
		// winner. The unconditional Store below is the
		// "everyone else gave up, last firer wins" branch — NOT
		// a duplicate of the CAS path; the CAS path installs
		// the fresh ticket we just built.
		fresh := &TouchTicket{m: &d.tickets, id: sid}
		fresh.firedAt.Store(&now)
		if d.tickets.CompareAndSwap(sid, t, fresh) {
			return fresh, true
		}
		if cur, ok := d.tickets.Load(sid); ok {
			latest := cur.(*TouchTicket)
			if last := latest.firedAt.Load(); last != nil && now.Sub(*last) < window {
				return nil, false
			}
		}
	}
	t := &TouchTicket{m: &d.tickets, id: sid}
	t.firedAt.Store(&now)
	d.tickets.Store(sid, t)
	return t, true
}

// isAccountScopedPath returns true for the paths that must remain
// reachable while an account is in the deletion grace window.
func isAccountScopedPath(p string) bool {
	switch p {
	case "/v1/account", "/v1/account/export", "/v1/account/restore":
		return true
	}
	return false
}

// --- RequireLimited ------------------------------------------------------

// RequireLimited is RequireSession wrapped in
// pkg/middleware.AuthLimit (spec §11: 10 failed auth attempts per
// IP per minute). The /v1/* API-key surface uses this everywhere;
// only /login, /auth/verify, and /dashboard/* use the cookie-based
// dashboardAuthChain.
//
// Counts ONLY 401s — the inner handler is responsible for any 429
// emission. CountStatuses=[401] is the explicit default.
//
// The bucket is m.Limiter — shared across every /v1/* route so
// spec §11 "10/min/IP" is enforced across the whole surface, not
// per route.
func (m *Middleware) RequireLimited(next AccountHandler) http.HandlerFunc {
	h := m.RequireSession(next)
	cfg := middleware.AuthLimitConfig{
		CountStatuses: []int{http.StatusUnauthorized},
		Log:           m.Log,
	}
	return middleware.AuthLimitWithLimiter(cfg, m.Limiter)(h).ServeHTTP
}

// --- RequireMFA ----------------------------------------------------------

// RequireMFA gates the route on MFA-completion. Reads the
// mfa-pending flag stashed by RequireSession's session-cookie
// branch. Bearer-key principals bypass MFA (API keys are not
// MFA-bound per IAM-2 decision 3).
//
// On a blocked request emits a 403 problem and an IAM-4
// auth.mfa_gate_hit audit row (when Auditor is non-nil).
// Allowlists /v1/account/mfa/* paths so the dashboard can render
// the enrollment prompt.
//
// Mirrors cmd/apid/mfa_middleware.go:156 (PR #180 / PR #244).
func (m *Middleware) RequireMFA(next AccountHandler) AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		pending, hasPending := MFAPendingFrom(r)
		if !hasPending || !pending {
			// Bearer-key principal (no mfa-pending stamp) or
			// already-cleared session — no gate.
			next(w, r, acct)
			return
		}
		if isMFAAllowlisted(r.URL.Path) {
			next(w, r, acct)
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden,
			api.CodeMFARequired, "MFA required",
			"complete /v1/account/mfa/enroll or /v1/account/mfa/verify to access this route"))
		if m.Audit != nil {
			m.Audit.Emit(r.Context(), "auth.mfa_gate_hit", &acct.ID, map[string]any{
				"path":   r.URL.Path,
				"method": r.Method,
			})
		}
	}
}

// --- RequireStepUp -------------------------------------------------------

// Step-up audit "reason" values. Three occurrences (RequireStepUp,
// RequireStepUpHandler, RequireStepUpStrict) trigger goconst at
// 3 — keeping them as named constants is also the contract for
// downstream SQL queries that filter on `data->>'reason'`.
const (
	stepUpReasonMissing = "missing"
	stepUpReasonExpired = "expired"
)

// RequireStepUp gates a route on a fresh TOTP step-up. Reads the
// stamp set by RequireSession's session-cookie branch (StepUpFrom)
// and rejects when it's missing or older than ttl. Bearer-key
// principals bypass step-up (an API key is itself a step-up-
// equivalent proof — the request can't be replayed from a stolen
// browser without the key).
//
// On a blocked request emits a 403 problem and an IAM-4
// auth.step_up_required audit row. Distinct from RequireMFA's
// auth.mfa_gate_hit kind so a downstream query can tell "needs
// MFA enrollment" from "needs recent step-up". Auditable per ADR-077.
//
// Default TTL is the user-confirmed 5-minute window (industry
// comparison: GitHub sudo-mode 5m, AWS console 15m, GCP IAM 10m;
// 5m is the shortest that still tolerates a single confirmation
// click latency). The auth.step_up_verified kind fires on the
// /v1/account/mfa/verify success branch so an operator can audit
// how often the gate is succeeding vs. blocking.
//
// Compose ordering for a sensitive-op route:
//
//	requireMFA → requireScope(admin) → requireStepUp(5m) → handler
//
// The new stamp is written by /v1/account/mfa/verify on every
// success — see cmd/apid/handlers_mfa.go reissueSessionCookie.
//
// RequireStepUpStrict is the opt-in for operations where a bearer must
// never be enough proof. Invitation acceptance and provider-admin
// mutations use it; other legacy sensitive routes retain the documented
// lax key-as-proof posture until they are migrated.
func (m *Middleware) RequireStepUp(ttl time.Duration) func(AccountHandler) AccountHandler {
	return m.requireStepUp(ttl, false)
}

// RequireStepUpStrict is the same gate as RequireStepUp but the
// bearer-key branch is REJECTED with 403 step_up_required, not
// bypassed. Use this on routes where the threat model explicitly
// excludes the bearer-key equivalent-proof posture — i.e. where a
// leaked token alone is sufficient to perform the action and the
// step-up chain exists to require a fresh TOTP even from the
// bearer principal.
//
// PR-9 introduced Strict for invitation acceptance. Provider-admin
// mutations use the same strict gate so bearer/API-key principals are
// rejected consistently across the control plane.
//
// The cookie path is unchanged — the absence of a step-up stamp
// on a session-cookie principal still fails-fast (the v1 cookie
// branch already did, and the Envelope.StepUpAt omission is the
// same condition).
func (m *Middleware) RequireStepUpStrict(ttl time.Duration) func(AccountHandler) AccountHandler {
	return m.requireStepUp(ttl, true)
}

// requireStepUp is the shared implementation of RequireStepUp
// (lax: bearer-key bypass) and RequireStepUpStrict (no bypass).
// strict:true in the audit row is the post-PR-9 marker operators
// filter for in audit queries.
func (m *Middleware) requireStepUp(ttl time.Duration, strict bool) func(AccountHandler) AccountHandler {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return func(next AccountHandler) AccountHandler {
		return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
			ts, has := StepUpFrom(r)
			// Lax mode: bearer-key principal (no step-up stamp) bypasses.
			// Pre-PR-077 cookie without step_up_at is also bypass-
			// aware — the middleware is opt-in and the absence of a
			// stamp carries the same risk profile as the bearer-
			// bypass path. The Envelope.StepUpAt field is omitempty
			// so a cookie issued before PR-077 reads StepUpAt zero
			// and the bypass fails open: this is the documented
			// "rolling out, the gate hasn't tripped anyone yet"
			// behaviour. Once every active session carries a stamp
			// (one TOTP rotation cycle later), the bypass becomes
			// the legacy-cookie anti-pattern that future audit
			// queries can filter for (subject = sid, no StepUpAt).
			if !strict && !has {
				next(w, r, acct)
				return
			}
			if has && !ts.IsZero() && time.Since(ts) <= ttl {
				next(w, r, acct)
				return
			}
			reason := stepUpReasonMissing
			if has && !ts.IsZero() {
				reason = stepUpReasonExpired
			}
			api.WriteProblem(w, api.ErrStepUpRequired())
			if m.Audit != nil {
				data := map[string]any{
					"path":    r.URL.Path,
					"method":  r.Method,
					"reason":  reason,
					"ttl_sec": int(ttl.Seconds()),
				}
				if strict {
					data["strict"] = true
				}
				m.Audit.Emit(r.Context(), "auth.step_up_required", &acct.ID, data)
			}
		}
	}
}

// RequireStepUpHandler is the http.Handler-shaped twin of
// RequireStepUp (IAM-hardening-mega-PR, ADR-077) for dashboard
// routes that don't fit the AccountHandler signature
// (RequireSession is http.Handler-shaped via sessionAuth in
// cmd/apid/auth_facade.go). Same TTL semantics, same audit kind,
// same bypass rules. The stamp is read off r.Context() the same
// way RequireStepUp reads it via StepUpFrom.
func (m *Middleware) RequireStepUpHandler(ttl time.Duration) func(http.Handler) http.Handler {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ts, has := StepUpFrom(r)
			if !has {
				next.ServeHTTP(w, r)
				return
			}
			if !ts.IsZero() && time.Since(ts) <= ttl {
				next.ServeHTTP(w, r)
				return
			}
			reason := stepUpReasonMissing
			if !ts.IsZero() {
				reason = stepUpReasonExpired
			}
			api.WriteProblem(w, api.ErrStepUpRequired())
			if m.Audit != nil {
				// Dashboard routes don't carry a state.Account on
				// the request context the way RequireSession's
				// AccountHandler branch does; use the session's
				// AccountID via principalFrom so the audit row
				// still names the principal.
				acctID := ""
				if p, ok := principalFrom(r); ok && p.Acct.ID != "" {
					acctID = p.Acct.ID
				}
				m.Audit.Emit(r.Context(), "auth.step_up_required", &acctID, map[string]any{
					"path":    r.URL.Path,
					"method":  r.Method,
					"reason":  reason,
					"ttl_sec": int(ttl.Seconds()),
				})
			}
		})
	}
}

// --- RequireScope --------------------------------------------------------

// RequireScope enforces the api.Scope* vocabulary. The principal
// must carry at least one of the allowed scopes; session-cookie
// principals are implicitly admin (Key == nil). Empty allowed set
// is a no-op (the caller didn't ask for any check).
//
// On a missing principal (RequireSession wasn't wired) returns 500
// CodeCapacity — same fail-closed behaviour as
// cmd/apid/server.go:1481-1485. A real wiring bug surfaces as a
// loud 500, not a silent bypass.
//
// Mirrors cmd/apid/server.go:1476 (PR #244).
func (m *Middleware) RequireScope(allowed ...string) func(AccountHandler) AccountHandler {
	return func(next AccountHandler) AccountHandler {
		return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
			// Empty allowed = no check (the caller didn't ask,
			// e.g. internal routes). Short-circuit BEFORE the
			// principal lookup so a route that wires RequireScope
			// without RequireSession (e.g. /internal/health) is
			// not fail-closed. Matches cmd/apid exactly.
			if len(allowed) == 0 {
				next(w, r, acct)
				return
			}
			p, ok := principalFrom(r)
			if !ok {
				// RequireScope must run AFTER RequireSession.
				api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeCapacity,
					"auth-context missing", "RequireScope must be wrapped inside RequireSession / RequireLimited"))
				return
			}
			if !principalHasScope(p, allowed) {
				api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
					"Insufficient scope", "this endpoint requires one of: "+strings.Join(allowed, ",")))
				return
			}
			next(w, r, acct)
		}
	}
}

// --- LoadApp -------------------------------------------------------------

// LoadApp resolves a slug to an account-scoped App, collapsing
// cross-account lookups to 404 per the handler convention. Returns
// the resolved app or writes the error and returns false.
//
// IDOR safety: a slug belongs to exactly one account; the ownership
// predicate (app.AccountID == acct.ID) is the load-bearing check.
// Cross-tenant reads surface as 404, NOT 403, so the handler
// convention never leaks "this slug exists, just not for you".
func (m *Middleware) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	app, err := m.Authn.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Not found", "no such app"))
		return state.App{}, false
	}
	return app, true
}

// --- scope helpers -------------------------------------------------------

// principalHasScope reports whether the principal carries at least
// one of the allowed scopes. Session-cookie principals (Key == nil)
// are implicitly admin. An empty allowed set is a no-op (the caller
// didn't ask for any scope check, e.g. internal routes).
//
// INVARIANT: this helper is called only from RequireScope, which
// short-circuits on `len(allowed) == 0` before reaching here. The
// Key==nil branch relies on RequireScope being run AFTER
// RequireSession; a direct call without going through the auth
// middleware would let unauthenticated requests reach the handler.
//
// Mirrors cmd/apid/server.go:1277.
func principalHasScope(p principal, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	if p.Key == nil {
		// Session-cookie auth = human at the dashboard = full access.
		return true
	}
	for _, want := range allowed {
		for _, have := range p.Key.Scopes {
			if have == want {
				return true
			}
		}
	}
	return false
}

// --- MFA allowlist -------------------------------------------------------

// mfaAllowlist is the set of paths that stay reachable while the
// cookie is mfa_pending. The intent is: the dashboard can still
// render the "MFA required" prompt, and the customer can complete
// enrollment / step-up / recovery / disable without first
// satisfying MFA on a different route.
//
// Mirrors cmd/apid/mfa_middleware.go:90 exactly.
var mfaAllowlist = []string{
	"/v1/account",
	"/v1/account/mfa/enroll",
	"/v1/account/mfa/confirm",
	"/v1/account/mfa/verify",
	"/v1/account/mfa/recover",
	"/v1/account/mfa/disable",
	// IAM-3 (ADR-039) — a customer whose session is mfa_pending
	// must still be able to list / revoke their active sessions.
	// The /v1/auth/sessions/{id} route is matched by the prefix
	// check in isMFAAllowlisted below, not by a literal entry.
	"/v1/auth/logout",
	"/v1/auth/csrf",
	"/v1/auth/sessions",
	"/v1/auth/sessions/revoke_all",
}

// isMFAAllowlisted is the predicate RequireMFA calls on
// r.URL.Path.
func isMFAAllowlisted(path string) bool {
	for _, p := range mfaAllowlist {
		if p == path {
			return true
		}
	}
	// Wildcard DELETE /v1/auth/sessions/{id} matched by prefix.
	if strings.HasPrefix(path, "/v1/auth/sessions/") && path != "/v1/auth/sessions/revoke_all" {
		return true
	}
	return false
}

// --- cookie helpers ------------------------------------------------------

// bearerToken extracts the bearer token from the Authorization
// header. The scheme is matched case-insensitively per RFC 6750
// §2.1 ("Bearer" is the registered scheme name; clients in the
// wild occasionally lowercase it). A scheme we don't recognise
// falls through to the session-cookie branch.
//
// Mirrors cmd/apid/server.go:1578 (which was case-sensitive; this
// lift tightens the contract).
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const scheme = "bearer "
	if len(h) >= len(scheme) && strings.EqualFold(h[:len(scheme)], scheme) {
		return strings.TrimSpace(h[len(scheme):])
	}
	return ""
}

// clearSessionCookie evicts the session cookie on the client. Path
// "/" + MaxAge=-1 matches the cookie Name + Secure + SameSite set
// by the issuer. The CSRF cookie is deliberately NOT touched here —
// it is bound to the double-submit envelope and survives session-
// revoke.
func (m *Middleware) clearSessionCookie(w http.ResponseWriter) {
	m.ClearSessionCookie(w)
}

// ClearSessionCookie evicts the session cookie on the client. Exported
// so cmd/apid's session-bearing handlers (handlers_sessions.go +
// handlers_mfa.go) can keep their pre-extraction `clearSessionCookie`
// bridge without re-implementing the cookie attribute set. The
// attribute set must match the issuer in handlers_auth.go (Path "/" +
// Secure + SameSite=Lax + HttpOnly) so a Set-Cookie issued here
// overwrites one set on the way in. The CSRF cookie is deliberately
// NOT touched (it is bound to the double-submit envelope and survives
// session-revoke).
func (m *Middleware) ClearSessionCookie(w http.ResponseWriter) {
	name := m.SessionCookieName
	if name == "" {
		name = defaultSessionCookieName
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Domain:   m.CookieDomain,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// prefix8 returns the first 8 characters of s, or s itself when
// shorter. The IAM-3-Evolved binding-mismatch audit row carries
// the 8-char prefix so an operator can disambiguate the kind of
// drift (e.g. "presented `7a2f…` but stored `b81c…`") without
// leaking the HMAC key. 8 hex chars = 32 bits, plenty for a
// human-readable fingerprint.
func prefix8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
