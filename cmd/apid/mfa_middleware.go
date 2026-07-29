// MFA gate (IAM-2, issue #186).
//
// requireMFA is the session-cookie-side companion to requireScope.
// It runs after s.auth has stamped the verified principal, and
// reads the mfa-pending flag off the session envelope via a separate
// context value (withMFAPending). The flag is true when the cookie
// was issued by a login path on an account that is
// mfa_required && !mfa_enrolled; the cookie-stamp path in s.auth
// (cmd/apid/server.go, the cookie branch) is the only writer.
//
// Two non-trivial decisions:
//
//   1. The mfa-pending flag is a *distinct* context key from
//      principalCtxKey. principalCtxKey is an iota-typed const; a
//      future PR inserting a value in the middle would silently
//      shift the int. The MFA flag lives on its own string-typed
//      key to make the wire-shape intent explicit and to make the
//      `mfaPendingFrom(r) → (false, false)` fallback detectable
//      from a miswire.
//
//   2. The MFA allowlist is path-prefixed against r.URL.Path so
//      the dashboard's /v1/account whoami can render the "MFA
//      required" prompt without itself being gated. Every other
//      session-cookie route 403s CodeMFARequired. API keys bypass
//      the gate (mfaPendingFrom returns false/ok=false → key path
//      bypass) per the IAM-2 design decision in the plan.

package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// mfaPendingCtxKey is the unexported type-guard around the
// withMFAPending context value. Distinct from principalCtxKey
// (which is iota-typed in server.go) so a future insert in that
// const block doesn't silently collide.
type mfaPendingCtxKey struct{}

// withMFAPending decorates the request context for the requireMFA
// wrapper. Stamped by the cookie branch of s.auth (server.go: the
// `c, err := r.Cookie(sessionCookie)` block); not stamped by the
// bearer branch because API keys bypass MFA per the IAM-2 design
// decision 3. A missing value (`mfaPendingFrom` returns
// (false, false)) is the deliberate signal for "bearer key —
// bypass".
func withMFAPending(ctx context.Context, pending bool) context.Context {
	return context.WithValue(ctx, mfaPendingCtxKey{}, pending)
}

// mfaPendingFrom returns the mfa-pending flag stamped by s.auth's
// cookie branch. (false, false) means the principal was a bearer
// key (no MFA on the table) OR the routes were wired without
// s.auth (a test miswire). Both cases bypass the gate.
func mfaPendingFrom(r *http.Request) (bool, bool) {
	v := r.Context().Value(mfaPendingCtxKey{})
	if v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// mfaAllowlist is the set of paths that stay reachable while the
// cookie is mfa_pending. The intent is: the dashboard can still
// render the "MFA required" prompt, and the customer can complete
// enrollment / step-up / recovery / disable without first
// satisfying MFA on a different route.
//
// MUST match pkg/auth/middleware.mfaAllowlist — a future
// post-extraction refactor that lifts the cookie branch fully
// into pkg/auth would consume the pkg/auth copy and delete this
// one. Until then, the two lists must stay byte-identical; a
// silent drift here would let an MFA-pending session reach a
// route pkg/auth refuses, or vice versa.
//
//   - /v1/account                — whoami (lets the dashboard know
//     which account it's prompting).
//   - /v1/account/mfa/enroll     — start enrollment.
//   - /v1/account/mfa/confirm    — finish enrollment.
//   - /v1/account/mfa/verify     — step up an mfa_pending session.
//   - /v1/account/mfa/recover    — burn a recovery code.
//   - /v1/account/mfa/disable    — opt out (if mfa_required is
//     false).
//
// Exact-path match: the route table uses Go 1.22+ `method
// /path` form so r.URL.Path is the full pattern, no trailing
// wildcards. Adding a new MFA sub-route means adding it here.
// The /v1/auth/* cookie-issue paths are NOT in the allowlist —
// they are not gated by s.auth at all (they live on the
// dashboardAuthChain), so they never reach requireMFA.
var mfaAllowlist = []string{
	"/v1/account",
	"/v1/account/mfa/enroll",
	"/v1/account/mfa/confirm",
	"/v1/account/mfa/verify",
	"/v1/account/mfa/recover",
	"/v1/account/mfa/disable",
	// IAM-3 (ADR-039) — a customer whose session is mfa_pending
	// must still be able to list / revoke their active sessions.
	// The /v1/auth/sessions/{id} route (any uuid) is matched by
	// the prefix check in isMFAAllowlisted below, not by a literal
	// entry. Same Go 1.22 mux shape as the wildcard-route pattern:
	// the route table registers "DELETE /v1/auth/sessions/{id}"
	// and the request lands on r.URL.Path = the literal UUID,
	// never the route pattern.
	"/v1/auth/logout",
	"/v1/auth/sessions",
	"/v1/auth/sessions/revoke_all",
}

// isMFAAllowlisted is the predicate requireMFA calls on
// r.URL.Path. Returns true for any path in mfaAllowlist OR for
// the wildcard /v1/auth/sessions/{id} (matched by prefix — see
// mfaAllowlist comment). False on every other session-cookie
// route, which 403s CodeMFARequired.
func isMFAAllowlisted(path string) bool {
	for _, p := range mfaAllowlist {
		if p == path {
			return true
		}
	}
	// Wildcard DELETE /v1/auth/sessions/{id}. The Go 1.22 mux
	// delivers r.URL.Path as the actual requested UUID, not the
	// pattern, so a literal-equality check against the entry
	// would never match — the prefix match accepts ANY uuid
	// under the prefix.
	if strings.HasPrefix(path, "/v1/auth/sessions/") && path != "/v1/auth/sessions/revoke_all" {
		return true
	}
	return false
}

// requireMFA is the cmd/apid-side facade. The body lives in
// pkg/auth (cmd/apid/auth_facade.go::requireMFA is the bridge).
// This file keeps the unexported MFA helpers — mfaPendingFrom,
// withMFAPending, isMFAAllowlisted, mfaEnrollRequired — because
// the cookie-branch of s.auth still stamps the mfa-pending flag
// directly (the follow-up slice will route that through
// pkg/auth's WithMFAPending once RequireSession lands). ADR-044.

// mfaEnrollRequired is the predicate the login handlers check to
// decide whether to stamp MfaPending=true on the new session
// cookie. Returns true iff the account has the policy flag set
// AND has not yet enrolled. The inverse is "the customer has
// either cleared MFA or never been required to".
//
// Used by handlers_auth_*.go and the OAuth callbacks. Kept as a
// free function (not a method on Account) so the auth handlers
// can call it inline without exposing the predicate outside
// cmd/apid.
func mfaEnrollRequired(acct state.Account) bool {
	return acct.MFARequired && !acct.MFAEnrolled()
}
