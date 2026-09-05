// Facade: cmd/apid's existing s.auth + s.authLimited + s.requireMFA +
// s.requireScope + s.loadApp method names delegate to pkg/auth.Middleware
// so the route table stays unchanged (cmd/apid/server.go mounts every
// /v1/* route through these methods). The middleware lives in pkg/auth
// because Move 4 (issue #254) needs cmd/gatewayd-internal to compose the same
// chain without duplicating cmd/apid's auth surface.
//
// ADR-046 records the extraction. This file is the apid-side seam:
// it owns the bridge between cmd/apid's unexported `accountHandler`
// (cmd/apid/server.go) and pkg/auth's exported AccountHandler, plus the
// two unexported helpers (`authAccountHandler`, `toAcctHandler`) that
// cross the boundary.
//
// The bridge is intentionally zero-cost: pkg/middleware.AccountHandler and
// accountHandler are structurally identical (both are
// func(http.ResponseWriter, *http.Request, state.Account)) so the
// conversion is a function-pointer cast, not an allocation.
//
// What stays in cmd/apid (NOT delegated to pkg/auth):
//   - s.adminAllows — per-daemon operator-email allowlist (compute_nodes.go).
//     PR-2 (gatewayd-internal AppLogsHandler) doesn't need it; keep the seam
//     per-daemon.
//   - mfaSessionPending — login-handler-only predicate that decides
//     whether to stamp MfaPending=true on a freshly issued cookie.
//     pkg/auth reads the flag, cmd/apid writes it.
package main

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// auth delegates to pkg/auth.Middleware.RequireSession. The in-memory
// accountHandler ↔ pkg/middleware.AccountHandler bridge is the only
// conversion; behaviour matches cmd/apid/server.go:1341-1455 exactly
// because pkg/auth lifts that body verbatim.
func (s *server) auth(next accountHandler) http.HandlerFunc {
	pkgNext := middleware.AccountHandler(next)
	return s.authMw.RequireSession(pkgNext)
}

// authLimited wraps s.auth in pkg/middleware.AuthLimit with the
// shared per-IP bucket (s.apiAuthLimiter). The bucket is constructed
// in newServerWithDeps and threaded into pkg/auth at the same point
// (cmd/apid/auth_adapters.go for the bridge types); pkg/auth owns
// the wrapping because the spec §11 "10/min/IP" rule is a
// middleware-level concern, not a per-daemon configuration detail.
func (s *server) authLimited(next accountHandler) http.HandlerFunc {
	pkgNext := middleware.AccountHandler(next)
	return s.authMw.RequireLimited(pkgNext)
}

// requireMFA delegates to pkg/auth.Middleware.RequireMFA. Behaviour
// matches cmd/apid/mfa_middleware.go:493-528 exactly because pkg/auth
// lifts that body verbatim.
func (s *server) requireMFA(next accountHandler) accountHandler {
	pkgNext := middleware.AccountHandler(next)
	pkgHandler := s.authMw.RequireMFA(pkgNext)
	return authAccountHandler(pkgHandler)
}

// requireVerifiedEmail gates customer actions that publish code or touch
// money. Authentication and read-only account access remain available so an
// unverified customer can sign in and follow the dashboard guidance.
func (s *server) requireVerifiedEmail(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if !acct.EmailVerified() {
			api.WriteProblem(w, api.ErrEmailVerificationRequired())
			return
		}
		next(w, r, acct)
	}
}

// requireVerifiedEmailHandler is the http.Handler counterpart for dashboard
// routes that have already passed through sessionAuth.
func (s *server) requireVerifiedEmailHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acct, ok := AccountFrom(r.Context())
		if !ok {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
				"Unauthorized", "sign in is required."))
			return
		}
		if !acct.EmailVerified() {
			api.WriteProblem(w, api.ErrEmailVerificationRequired())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireStepUp delegates to pkg/auth.Middleware.RequireStepUp
// (IAM-hardening-mega-PR logical change 6, ADR-077). Compose order
// for a sensitive-op route is:
//
//	requireMFA → requireScope(admin) → requireStepUp(5m) → handler
//
// The default TTL is the user-confirmed 5-minute window. The
// reissue seam at /v1/account/mfa/verify refreshes the stamp on
// every successful TOTP verify (see reissueSessionCookie in
// handlers_mfa.go).
func (s *server) requireStepUp(ttl time.Duration) func(accountHandler) accountHandler {
	return s.requireStepUpVariant(ttl, s.authMw.RequireStepUp)
}

// requireStepUpStrict is the PR-9 §4 twin of requireStepUp: same
// 5m TTL but the bearer-key branch is rejected with 403 instead
// of bypassed. It is used by invitation acceptance and the provider
// admin mutation policy, where a leaked bearer must never be enough
// to perform a privileged operation.
func (s *server) requireStepUpStrict(ttl time.Duration) func(accountHandler) accountHandler {
	return s.requireStepUpVariant(ttl, s.authMw.RequireStepUpStrict)
}

// requireAdminMutation is the single authentication policy for provider
// control-plane writes. Unlike the legacy RequireMFA/RequireStepUp pair, the
// strict step-up gate rejects bearer/API-key principals instead of treating a
// key as an equivalent proof. Provider mutations must be performed from a
// verified operator session with a recent TOTP step-up.
//
// Keep this helper next to the auth facade so adding a new /v1/admin mutation
// requires choosing this policy explicitly at the route table rather than
// repeating a subtly different middleware chain at every call site.
func (s *server) requireAdminMutation(next accountHandler) accountHandler {
	return s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(s.requireStepUpStrict(5 * time.Minute)(s.requireSameOrigin(s.requireIdempotency(next)))))
}

// requireSameOrigin is a defense-in-depth browser boundary for provider
// mutations. API clients and CLI callers generally omit Origin, so an absent
// header is allowed; when a browser supplies Origin or Sec-Fetch-Site, the
// request must come from the API host or the dedicated operations origin.
// This prevents a customer-controlled page from driving an operator's
// cookie-authenticated control-plane session.
func (s *server) requireSameOrigin(next accountHandler) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site == "cross-site" {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
				"cross-origin request rejected", "provider-admin mutations require a trusted control-plane origin"))
			return
		}
		if raw := strings.TrimSpace(r.Header.Get("Origin")); raw != "" {
			origin, err := url.Parse(raw)
			if err != nil || origin.Host == "" || !s.isTrustedAdminOrigin(origin, r) {
				api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
					"cross-origin request rejected", "provider-admin mutations require a trusted control-plane origin"))
				return
			}
		}
		next(w, r, acct)
	}
}

func (s *server) isTrustedAdminOrigin(origin *url.URL, r *http.Request) bool {
	originHost := strings.ToLower(origin.Hostname())
	requestHost := strings.ToLower(r.Host)
	if parsed, err := url.Parse("//" + r.Host); err == nil && parsed.Hostname() != "" {
		requestHost = strings.ToLower(parsed.Hostname())
	}
	if originHost == requestHost {
		return true
	}
	base := strings.ToLower(strings.TrimSpace(s.domain))
	return base != "" && originHost == "operations."+base
}

// requireStepUpVariant is the shared body of requireStepUp /
// requireStepUpStrict — the only difference is which middleware
// function is invoked. The chain parameter matches the
// `func(time.Duration) func(middleware.AccountHandler) middleware.AccountHandler`
// signature of both RequireStepUp and RequireStepUpStrict.
func (s *server) requireStepUpVariant(
	ttl time.Duration,
	chain func(time.Duration) func(middleware.AccountHandler) middleware.AccountHandler,
) func(accountHandler) accountHandler {
	return func(next accountHandler) accountHandler {
		return authAccountHandler(chain(ttl)(middleware.AccountHandler(next)))
	}
}

// requireStepUpHandler is the http.Handler-shaped twin for
// dashboard routes (cmd/apid/server.go wires /dashboard/account/*
// through sessionAuth → http.Handler, not through AccountHandler).
// Same TTL semantics as requireStepUp.
func (s *server) requireStepUpHandler(ttl time.Duration) func(http.Handler) http.Handler {
	return s.authMw.RequireStepUpHandler(ttl)
}

// requireScope delegates to pkg/auth.Middleware.RequireScope. Same
// behaviour as cmd/apid/server.go:531-568.
func (s *server) requireScope(allowed ...string) func(accountHandler) accountHandler {
	pkgAllowed := allowed // already a string slice — no copy needed
	return func(next accountHandler) accountHandler {
		pkgNext := middleware.AccountHandler(next)
		pkgHandler := s.authMw.RequireScope(pkgAllowed...)(pkgNext)
		return authAccountHandler(pkgHandler)
	}
}

// loadApp delegates to pkg/auth.Middleware.LoadApp (IDOR-safe
// slug→App with the ownership predicate app.AccountID == acct.ID).
// Behaviour matches cmd/apid/server.go:1611-1617.
func (s *server) loadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return s.authMw.LoadApp(w, r, acct, slug)
}

// loadOrg wraps an accountHandler with pkg/authz.LoadOrg — the
// middleware that resolves the X-Active-Org / ?org= hint to an
// OrgMembership and stamps it onto the principal (issue #190 /
// IAM-6 / ADR-061, PR 4). Unlike loadApp (which is a value-returning
// helper), loadOrg is a middleware: it returns an accountHandler
// that the route table mounts inside s.auth.
//
// Returns a pass-through accountHandler when s.orgResolver is nil
// (unit tests that don't exercise LoadOrg) so the route table never
// dereferences a nil pointer.
//
// The audit emitter is wired from s.audit when available so denials
// surface in pkg/audit as authz.denied rows; nil is tolerated and
// means no audit rows are emitted (PR 4 default; PR 5+ may add a
// dedicated metric counter via pkg/wire/metrics.go).
func (s *server) loadOrg(next accountHandler) accountHandler {
	if s.orgResolver == nil {
		// No resolver wired → pass-through. Tests that don't
		// exercise LoadOrg land here.
		return next
	}
	auditEmitter := loadOrgAuditFrom(s.audit)
	mw := authz.LoadOrgWithResolver(authz.LoadOrgConfig{
		Log:        s.log,
		Audit:      auditEmitter,
		HeaderName: "X-Active-Org",
		QueryName:  "org",
	}, s.orgResolver)

	// AccountHandler-style middlewares require an http.Handler
	// adapter, so we wrap `next` in an http.HandlerFunc. The
	// closure captures `acct` so the inner dispatch sees the
	// same account the route table mounted us with — without
	// this capture, audit rows would lose the principal when
	// LoadOrgWithResolver's chain re-enters our wrapped handler.
	// Do NOT simplify to `mw(http.HandlerFunc(next))` — the
	// accountHandler→http.HandlerFunc conversion would drop the
	// third argument used by the rest of the cmd/apid route table.
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		wrapped := mw(http.HandlerFunc(func(w2 http.ResponseWriter, r2 *http.Request) {
			next(w2, r2, acct)
		}))
		wrapped.ServeHTTP(w, r)
	}
}

// loadOrgAuditFrom bridges cmd/apid's *auditor to pkg/authz.AuditEmitter.
// Returns nil when the audit pipeline isn't wired — the same
// nil-tolerant default that lets PR 4 ship without forcing every
// test to construct an auditor.
func loadOrgAuditFrom(a *auditor) authz.AuditEmitter {
	if a == nil {
		return nil
	}
	return auditorAsAuthzAuditor(a)
}

// authAccountHandler converts a pkg/middleware.AccountHandler back to
// cmd/apid's unexported accountHandler. Structurally identical so
// the conversion is a free cast (no allocation, no closure capture)
// — the round-trip through interface{} is the only thing that lets
// Go assign across two distinct named function types. A future
// signature divergence between AccountHandler and accountHandler
// fails the cast at compile time; the var assertion below pins the
// union the other direction so cmd/apid's handlers compose with the
// pkg/auth chain by typing, not by conversion.
func authAccountHandler(h middleware.AccountHandler) accountHandler {
	return accountHandler(h)
}

// Compile-time assertion: both function types must be
// structurally identical. If pkg/auth changes AccountHandler
// (e.g. adds an argument), the round-trip below fails and the
// bridge stops compiling — the surface area is the contract.
var _ = func() accountHandler {
	var pkg middleware.AccountHandler
	return accountHandler(pkg)
}

// sessionFrom is the cmd/apid-side bridge to pkg/middleware.SessionFromContext.
// cmd/apid's cookie-bearing handlers (handlers_sessions.go +
// handlers_mfa.go::reissueSessionCookie) historically read the
// state.Session row off the request context. After pkg/auth owns
// the cookie branch, the stamp lives in pkg/auth's sessionCtxKey;
// sessionFrom reads it back via the pkg/auth accessor so handlers
// stay byte-identical.
func sessionFrom(r *http.Request) (state.Session, bool) {
	sess, ok := middleware.SessionFromContext(r)
	if !ok {
		return state.Session{}, false
	}
	return *sess, true
}

// clearSessionCookie evicts the faas_sid cookie on the client.
// Bridges to pkg/auth's Middleware.ClearSessionCookie so the cookie
// attribute set matches the issuer in handlers_auth.go (Path "/" +
// Secure + SameSite=Lax). The cookie name reads from
// pkg/auth.Middleware.SessionCookieName — default "faas_sid"; cmd/apid
// never overrides the name so the default is what callers see.
func (s *server) clearSessionCookie(w http.ResponseWriter, _ *http.Request) {
	s.authMw.ClearSessionCookie(w)
}

// withDeprecation stamps the RFC 8594 + RFC 8288 deprecation headers
// on the wrapped route so clients (and the operator UI's lint) can
// detect a sunsetting endpoint. Three headers are set before the
// handler runs:
//
//   - Deprecation: true                         (RFC 8594 §2)
//   - Sunset: Wed, 01 Oct 2026 00:00:00 GMT     (RFC 8594 §3)
//   - Link: </v1/admin/obs/nodes/events>;       (RFC 8288 —
//     rel="successor-version"                     successor-version)
//
// The headers are written OUTSIDE the handler so they carry even on
// auth-rejected paths (403 from the email allowlist, 401 from the
// session middleware, etc.). The chain is
// s.withDeprecation → s.authLimited → s.requireMFA → s.requireScope
// → handler; mounting withDeprecation outermost means the headers
// always land.
//
// PR #3 (ADR-091 §3.7.9) introduces this pattern on
// /v1/compute-nodes/events. The new path /v1/admin/obs/nodes/events
// does NOT carry the headers — it is the successor. 410 Gone on
// the old path is a follow-up cleanup PR after one release.
//
// Sunset is the documented 2026-10-01 date; bump on every release
// that ships a successor feature. Easy to bump per-PR because the
// constant lives in one place.
func (s *server) withDeprecation(next accountHandler) accountHandler {
	const (
		sunset = "Wed, 01 Oct 2026 00:00:00 GMT"
		link   = `</v1/admin/obs/nodes/events>; rel="successor-version"`
	)
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		h := w.Header()
		h.Set("Deprecation", "true")
		h.Set("Sunset", sunset)
		h.Set("Link", link)
		next(w, r, acct)
	}
}
