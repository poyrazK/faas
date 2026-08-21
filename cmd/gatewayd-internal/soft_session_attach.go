// soft_session_attach.go — ADR-123 (members_only public-auth mode)
// cmd-side adapter that wires authmw.Middleware.AttachSessionIfPresent
// into the gatewayd-internal public-front-door chain.
//
// Why this exists
//
// pkg/gateway/handler.go::applyIngressMembersOnly is the per-app
// ingress gate for apps.public_auth_mode='members_only'. It
// resolves the cookie envelope via the cmd-side
// authPrincipalAdapter (cmd/gatewayd-internal/auth_principal_adapter.go),
// which delegates to middleware.PrincipalFrom(r) — a read-side
// helper that pulls the principal stamped into r.Context() by
// pkg/auth/middleware.RequireSession. The hard-attach
// RequireSession 401s on missing/invalid cookie, which would
// break every other public_auth_mode (open / bearer / basic /
// ip_allowlist / internal_only) that needs to pass through
// anonymous traffic.
//
// AttachSessionIfPresent is the soft-attach companion: it runs
// the cookie branch without 401ing on miss/invalid, stamping
// the principal into r.Context() only on success. The downstream
// per-app gate then reads the principal via PrincipalFrom; a
// gate that doesn't care about cookies (open / bearer / etc.)
// ignores the stamp, and the members_only gate sees ok=false
// on a no-cookie request and 401s with the no_cookie reason.
//
// Wiring point
//
// The middleware wraps publicListenerHandler at the
// publicListenerHandler = http.Handler(publicHandler) site in
// cmd/gatewayd-internal/run.go (line ~2206). The wrap order is:
// outer-most → otelhttp → budget → recorder → softSessionAttach
// → httpsec → publicHandler. Soft-session-attach sits INSIDE
// the security middleware (so the response headers don't
// observe the attached principal) but OUTSIDE publicHandler
// (so the per-app gate reads the stamped principal on every
// request that flows through the chain). Audit + tracing
// keep their independent cross-cutting positions.
//
// Why a dedicated adapter file
//
// Mirrors the authPrincipalAdapter / requireAuthnAdapter /
// appErrorsRecorder pattern (each bridge type lives in its own
// file so reviewers can grep for the bridge shape). pkg/gateway
// stays free of pkg/auth/middleware imports (L300-303 boundary)
// — the adapter IS the seam.
//
// nil-safety: deps.authMw is non-nil in production (the daemon
// panics if it isn't constructed at run() line ~1259) but
// unit tests + dev boxes can hit the soft-attach with a nil
// adapter; the wrapper returns a pass-through middleware in
// that case (no stamp, no panic, no 401 — exactly the
// pre-ADR-123 behaviour).
package main

import (
	"net/http"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// softSessionAttach wraps h with a middleware that calls
// authmw.Middleware.AttachSessionIfPresent on every inbound
// request and forwards the (mutated) request to h. The stamp
// is best-effort: no cookie → pass through silently; invalid
// cookie → pass through silently; valid cookie → principal
// stamped into r.Context().
//
// Returns a pass-through http.Handler (no stamp, no-op) when
// mw is nil. This matches the nil-safety contract shared with
// requireAuthnAdapter (cmd/gatewayd-internal/require_authn_adapter.go:54)
// — the wrapper is constructed at run() time after deps.authMw
// is built, but unit tests construct their own deps and benefit
// from the same nil-tolerance.
func softSessionAttach(mw *authmw.Middleware, h http.Handler) http.Handler {
	if mw == nil {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Best-effort stamp. The bool return is
		// intentionally discarded — both ok=true and
		// ok=false paths forward to h. The principal
		// (if stamped) lives in r.Context() and is
		// visible to the downstream publicHandler chain
		// via middleware.PrincipalFrom(r).
		_ = mw.AttachSessionIfPresent(r)
		h.ServeHTTP(w, r)
	})
}
