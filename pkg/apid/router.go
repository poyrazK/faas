// Package apid — Router.
//
// This file holds the apid path-matching predicate (`IsApidPath`) that
// decides whether an inbound HTTP request should be forwarded from
// `cmd/gatewayd-internal/proxy.go` into the apid loopback. The matcher
// is the single source of truth across the codebase: the proxy uses it
// to dispatch, the standby write-gate (cmd/gatewayd-internal/write_gate.go)
// receives it as an injected predicate so it can decide whether a
// mutating request is bound for apid or for the wake/proxy fallthrough.
//
// History:
//   - Lifted from cmd/gatewayd-internal/proxy.go (where it was an
//     unexported helper gated on a local `apidRoot*` constants block)
//     during PR-B / Tier A9 / ADR-084. The proxy and the write-gate
//     both need to consult the same set of rooted prefixes; duplicating
//     the matcher (the original writegate.go:410-429 `apidPathMatch`)
//     was a drift hazard that the PR-A review flagged.
//   - The anchored-root discipline (`hasApidPrefix`) is preserved
//     verbatim: each prefix matches exact + "/" subtree, never bare
//     HasPrefix. Review finding #6 (PR #180 dashboard era): a bare
//     HasPrefix("/v1") would silently shadow "/v1.zip" and steal
//     customer-app traffic.
//   - The 12 anchored roots are exported as `ApidRoot*` constants so
//     external callers (writegate package, ops tooling) can reference
//     them by name rather than hard-coding strings.
package apid

import "strings"

// Anchored root paths used by IsApidPath. Each entry is matched as
// exact + "/" subtree (see hasApidPrefix). Order is not significant;
// the matcher is a flat loop.
//
// The set is exhaustive for the apid public surface (issue #85) —
// anything outside falls through to the wake/proxy path (which 404s
// for legitimate apid traffic, so missing entries are loud bugs
// we'll catch immediately in tests).
//
// Customer apps cannot expose routes starting with any of these
// roots (the proxy treats them as apid-bound). `/healthz` is deliberately
// absent: the public edge scopes the platform probe by Host, while an app's
// health path must reach that app like any other request. This is a deliberate
// constraint; the docs for customer apps should call this out
// (issue #85 follow-up).
const (
	ApidRootV1          = "/v1"
	ApidRootDashboard   = "/dashboard"
	ApidRootOAuthPrefix = "/oauth/"
	ApidRootLogin       = "/login"
	ApidRootSignup      = "/signup"
	ApidRootLoginForgot = "/login/forgot"
	ApidRootAuthVerify  = "/auth/verify"
	ApidRootAuthReset   = "/auth/reset"
	ApidRootLogout      = "/logout"
	ApidRootStatus      = "/status"
	ApidRootHealthz     = "/healthz"
	ApidRootCliAuth     = "/cli-auth"
	ApidRootDocs        = "/docs"
)

// IsApidPath returns true when the inbound request path matches one
// of the anchored roots the proxy forwards into the apid loopback.
//
// Anchor discipline (hasApidPrefix): each anchored entry matches
// exact + the trailing-slash subtree. Bare HasPrefix(prefix) would
// also match prefix + arbitrary junk (e.g. "/v1.zip" or "/loginfoo"),
// which would silently steal customer-app paths.
//
// /oauth/* is the only subtree form (no exact /oauth match); apid
// has no bare /oauth route today (only /oauth/callback is mounted),
// so a bare /oauth request would 404 on apid's mux either way. The
// pinned regression {"/oauth", false} (see router_test.go) defends
// against an accidental future expansion that would steal what
// should be a 404 path.
//
// Callers MUST treat this function as pure: no I/O, no globals. The
// standby write-gate (cmd/gatewayd-internal/write_gate.go) receives
// it as an injected predicate so it can avoid duplicating the
// predicate.
func IsApidPath(p string) bool {
	// Anchored roots: each matched as exact + "/" subtree.
	for _, root := range []string{
		ApidRootV1,
		ApidRootDashboard,
		ApidRootLogin,
		ApidRootSignup,
		ApidRootLoginForgot,
		ApidRootAuthVerify,
		ApidRootAuthReset,
		ApidRootLogout,
		ApidRootStatus,
		ApidRootCliAuth,
		ApidRootDocs,
	} {
		if hasApidPrefix(p, root) {
			return true
		}
	}
	// /oauth/* — only the subtree form. See godoc above.
	return strings.HasPrefix(p, ApidRootOAuthPrefix)
}

// hasApidPrefix returns true iff `p` is `prefix` exactly, or `prefix`
// followed by a single `/` and any continuation. This prevents
// accidental shadowing like "/v1.zip" matching "/v1" — review
// finding #6 from the dashboard era.
func hasApidPrefix(p, prefix string) bool {
	if p == prefix || p == prefix+"/" {
		return true
	}
	return strings.HasPrefix(p, prefix+"/")
}
