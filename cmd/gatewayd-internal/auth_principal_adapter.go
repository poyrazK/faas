package main

// auth_principal_adapter.go — ADR-123 (members_only
// public-auth mode) cmd-side adapter. The pkg/gateway
// CookiePrincipalExtractor interface (declared at
// pkg/gateway/public_auth_members_only.go:85) is the
// bridge pkg/gateway consumes to keep its
// "no import on pkg/auth" posture (L300-303 boundary).
// This file is the production wiring — it imports
// pkg/auth/middleware and composes with PrincipalFrom
// (the same shape the existing pkg/auth.Authenticator
// adapter at cmd/gatewayd-internal/require_authn_adapter.go
// uses). pkg/gateway NEVER imports pkg/auth/middleware;
// this file is the seam.
//
// The interface:
//
//   type CookiePrincipalExtractor interface {
//       FromRequest(r *http.Request) (accountID string, ok bool)
//   }
//
// The adapter returns the cookie-stamped account ID
// (a UUID, not the cookie value) on a valid session.
// bool=false when the request did not authenticate via
// cookie — the upstream pkg/auth/middleware.RequireSession
// 401s on those paths so the gate never sees a stolen /
// revoked / binding-mismatch cookie in practice. The
// gate's no-cookie branch is a defence-in-depth surface
// for the case where RequireSession is bypassed (a
// pre-PR-5 routes or a buggy future hot-path edit).
//
// Audit redaction: this adapter returns ONLY the
// account ID. It never returns the cookie value, the
// session ID, or any envelope that could be replayed.
// The pkg/gateway/public_auth_members_only_test.go
// TestApplyIngressMembersOnly_AuditDoesNotEchoCookie
// pins the redaction invariant by substring-checking
// the audit row's `actor_account_id` field never
// contains the raw cookie value.

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/auth/middleware"
)

// authPrincipalAdapter is the production
// CookiePrincipalExtractor. It delegates to
// pkg/auth/middleware.PrincipalFrom to resolve the
// cookie-stamped principal and returns the account
// ID. zero-value safe — if PrincipalFrom returns
// ok=false, the adapter returns ("", false) and the
// gate's no-cookie branch fires (401).
//
// Construction is via newAuthPrincipalAdapter() so the
// adapter is always consistent (no state to
// misconfigure). Future enhancements (e.g. a per-account
// short-circuit cache, ADR-076) would add fields here.
type authPrincipalAdapter struct{}

// newAuthPrincipalAdapter returns the production
// CookiePrincipalExtractor. The adapter is stateless;
// the only reason it's not a package-level var is to
// keep the constructor visible in deps initialisation
// for grep-ability.
func newAuthPrincipalAdapter() *authPrincipalAdapter {
	return &authPrincipalAdapter{}
}

// FromRequest implements CookiePrincipalExtractor.
// Returns the cookie-stamped account ID on a valid
// session; returns ("", false) on a missing / revoked /
// binding-mismatch cookie. The bool=false path is
// expected to be rare at runtime — RequireSession
// 401s upstream — but the gate's no-cookie branch is
// the safety net for the bypass scenarios.
func (a *authPrincipalAdapter) FromRequest(r *http.Request) (string, bool) {
	acct, _, _, ok := middleware.PrincipalFrom(r)
	if !ok {
		return "", false
	}
	if acct.ID == "" {
		// Defensive: a PrincipalFrom with ok=true and
		// an empty account ID is an invariant violation
		// (the upstream RequireSession sets both), but
		// the gate's no-cookie branch is the safe
		// surface for the impossible case.
		return "", false
	}
	return acct.ID, true
}
