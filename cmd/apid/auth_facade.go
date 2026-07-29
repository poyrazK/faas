// Facade: cmd/apid's existing s.auth + s.authLimited + s.requireMFA +
// s.requireScope + s.loadApp method names delegate to pkg/auth.Middleware
// so the route table stays unchanged (cmd/apid/server.go mounts every
// /v1/* route through these methods). The middleware lives in pkg/auth
// because Move 4 (issue #254) needs cmd/gatewayd to compose the same
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
//     PR-2 (gatewayd AppLogsHandler) doesn't need it; keep the seam
//     per-daemon.
//   - mfaEnrollRequired — login-handler-only predicate that decides
//     whether to stamp MfaPending=true on a freshly issued cookie.
//     pkg/auth reads the flag, cmd/apid writes it.
package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/auth/middleware"
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
