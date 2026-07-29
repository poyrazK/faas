// MFA gate (IAM-2, issue #186).
//
// requireMFA is the session-cookie-side companion to requireScope.
// It runs after s.auth has stamped the verified principal, and
// reads the mfa-pending flag off the session envelope via a
// separate context value (pkg/auth/middleware.WithMFAPending). The
// flag is true when the cookie was issued by a login path on an
// account that is mfa_required && !mfa_enrolled; the cookie-stamp
// path in pkg/auth.RequireSession (the cookie branch) is the only
// writer. ADR-046.
//
// The MFA allowlist is path-prefixed against r.URL.Path in
// pkg/auth/middleware so the dashboard's /v1/account whoami can
// render the "MFA required" prompt without itself being gated.
// Every other session-cookie route 403s CodeMFARequired. API keys
// bypass the gate (MFAPendingFrom returns false/ok=false → key
// path bypass) per the IAM-2 design decision in the plan.
//
// Pre-PR-1 this file held the cookie-stamp helpers
// (withMFAPending, mfaPendingFrom, mfaAllowlist, isMFAAllowlisted)
// and the s.requireMFA method body. After pkg/auth lifted the
// cookie branch + RequireMFA into the new package, both moved
// with it — keeping a copy here would have been an unused
// duplicate golangci-lint flags as dead code. The s.requireMFA
// facade now lives in cmd/apid/auth_facade.go. This file keeps
// only mfaEnrollRequired, which the login handlers use to decide
// whether to stamp MfaPending=true on a freshly issued cookie.

package main

import (
	"github.com/onebox-faas/faas/pkg/state"
)

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