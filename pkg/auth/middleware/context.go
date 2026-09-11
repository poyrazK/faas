// Context plumbing for the authenticated request. The three values
// RequireSession stamps into r.Context() (via withPrincipal,
// withSession, withMFAPending) are read back via AccountFromContext,
// SessionFromContext, MFAPendingFrom.
//
// The principal type is unexported because pkg/auth wants to
// retain the freedom to migrate the ctx key across a guard later.
// Callers that need to know "is this principal admin?" compose with
// RequireScope (which honours the session-cookie implicit-admin
// rule) instead of reaching into the principal directly.
package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ConsumerIdentity is the stable end-customer identity resolved by the
// gateway's consumer-key gate. It deliberately contains identifiers and the
// key's scopes only; the plaintext credential is never carried in context.
//
// The identity is shared through this package so gatewayd, request telemetry,
// and future authorization/metering consumers use one canonical context
// carrier rather than inventing incompatible request-local keys.
type ConsumerIdentity struct {
	ID     string
	AppID  string
	KeyID  string
	Scopes []string
}

type consumerCtxKey struct{}

// WithConsumer stamps a resolved end-customer identity onto ctx. The scopes
// slice is copied so callers cannot mutate the request identity after it has
// been installed.
func WithConsumer(ctx context.Context, identity ConsumerIdentity) context.Context {
	identity.Scopes = append([]string(nil), identity.Scopes...)
	return context.WithValue(ctx, consumerCtxKey{}, identity)
}

// ConsumerFromContext returns the end-customer identity stamped by the
// gateway consumer-auth gate. ok=false means the request is anonymous or did
// not pass through the gate. The returned scopes are copied to preserve the
// read-only context-carrier contract.
func ConsumerFromContext(r *http.Request) (ConsumerIdentity, bool) {
	if r == nil {
		return ConsumerIdentity{}, false
	}
	identity, ok := r.Context().Value(consumerCtxKey{}).(ConsumerIdentity)
	if !ok || identity.ID == "" {
		return ConsumerIdentity{}, false
	}
	identity.Scopes = append([]string(nil), identity.Scopes...)
	return identity, true
}

// principal is the authenticated caller. Key is nil when the caller
// authenticated via the dashboard session cookie (in which case
// RequireScope treats the caller as implicitly admin). Membership is
// the caller's resolved OrgMembership against the active org for the
// current request, stamped by pkg/authz.LoadOrgWithResolver (issue
// #190 / IAM-6 / ADR-061, PR 4). Membership is nil for requests that
// did not pass through LoadOrg (the common case for every pre-PR-5
// route) or that came in without an X-Active-Org / ?org= hint. Mirrors
// cmd/apid/server.go:1239.
type principal struct {
	Acct       state.Account
	Key        *state.APIKey
	Membership *state.OrgMembership
}

type ctxKey int

const principalCtxKey ctxKey = iota

// principalFrom returns the principal stashed in r.Context() by
// RequireSession. Returns ok=false if RequireSession did not run
// (e.g. tests that wire a handler directly to httptest).
func principalFrom(r *http.Request) (principal, bool) {
	v := r.Context().Value(principalCtxKey)
	if v == nil {
		return principal{}, false
	}
	p, ok := v.(principal)
	return p, ok
}

// withPrincipal returns a context carrying the principal.
// Unexported — only RequireSession stamps; only principalFrom +
// AccountFromContext read.
func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalCtxKey, p)
}

// AccountFromContext returns the (Account, APIKey) pair stashed by
// RequireSession. Returns ok=false if the request never went
// through RequireSession (the middleware wasn't wired) — callers
// should treat this as a wiring bug and fail closed (RequireScope
// emits 500 CodeCapacity; cmd/apid routes have the same fail-closed
// behaviour today).
//
// Key is nil for session-cookie auth; the implicit-admin rule lives
// inside RequireScope, NOT here, so AccountFromContext surfaces
// the raw authenticated shape. Callers that need the
// "session = admin" truth must compose with RequireScope or
// duplicate its rule.
//
// Membership is intentionally NOT surfaced here — see PrincipalFrom
// for the org-aware accessor. AccountFromContext stays stable for
// the 38 existing accountHandler bodies (ADR-046 seam).
func AccountFromContext(r *http.Request) (state.Account, *state.APIKey, bool) {
	p, ok := principalFrom(r)
	if !ok {
		return state.Account{}, nil, false
	}
	return p.Acct, p.Key, true
}

// PrincipalFrom returns the full authenticated shape — Account,
// APIKey, and the active-org Membership — stashed by RequireSession
// + pkg/authz.LoadOrgWithResolver (issue #190 / IAM-6 / ADR-061).
//
// Membership is nil when the request did not pass through LoadOrg
// (the pre-PR-5 default) or when the caller did not supply an
// X-Active-Org / ?org= hint. Authoritative for the 9 org actions
// defined in pkg/authz — call AuthorizeOrgAction(r, action) instead
// of branching on the membership role directly.
//
// Returns ok=false if the request never went through RequireSession
// (the middleware wasn't wired) — same fail-closed contract as
// AccountFromContext.
func PrincipalFrom(r *http.Request) (state.Account, *state.APIKey, *state.OrgMembership, bool) {
	p, ok := principalFrom(r)
	if !ok {
		return state.Account{}, nil, nil, false
	}
	return p.Acct, p.Key, p.Membership, true
}

// WithPrincipal stamps a principal onto ctx. Exported so tests
// (and any future reverse-proxy shim that pre-authenticates
// before the request reaches the daemon) can build a ctx that
// RequireScope will read via AccountFromContext.
//
// Production code MUST use RequireSession — this setter exists
// for the auth-skip path (load tests, integration tests with
// pre-minted identities) and is the only correct way to bypass
// the bearer/session dance without breaking RequireScope's
// fail-closed contract.
//
// The membership parameter is the active-org membership resolved
// by pkg/authz.LoadOrgWithResolver (issue #190 / IAM-6 / ADR-061).
// Callers that don't compose LoadOrg pass nil; pre-PR-5 routes stay
// account-scoped.
func WithPrincipal(ctx context.Context, acct state.Account, key *state.APIKey, mem *state.OrgMembership) context.Context {
	return withPrincipal(ctx, principal{Acct: acct, Key: key, Membership: mem})
}

// --- session-cookie plumbing --------------------------------------------

// sessionCtxKey holds the state.Session row RequireSession stamps
// after the live-row cross-check passes. Distinct from
// principalCtxKey (which is iota-typed) so a future insert in that
// const block doesn't silently collide.
type sessionCtxKey struct{}

// withSession stamps the live state.Session onto the request
// context. /v1/auth/sessions/{id} handlers read it back via
// SessionFromContext to know which sid to revoke without
// re-querying.
func withSession(ctx context.Context, sess state.Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sess)
}

// SessionFromContext returns the live state.Session stamped by
// RequireSession's cookie branch. ok=false means the request
// didn't authenticate via cookie (bearer-key paths).
func SessionFromContext(r *http.Request) (*state.Session, bool) {
	v := r.Context().Value(sessionCtxKey{})
	if v == nil {
		return nil, false
	}
	s, ok := v.(state.Session)
	if !ok {
		return nil, false
	}
	return &s, true
}

// --- mfa-pending plumbing -----------------------------------------------

// mfaPendingCtxKey is the typed ctx guard. Distinct from
// principalCtxKey (iota-typed) so a future insert doesn't collide.
// Distinct from sessionCtxKey (typed struct{} already, but a
// different one) so the MFA flag is a wire-shape intent signal, not
// a "session row carries this somewhere" side-channel.
type mfaPendingCtxKey struct{}

// withMFAPending decorates the request context with the
// mfa-pending flag. The session-cookie branch of RequireSession
// stamps this; the bearer branch deliberately does NOT (API keys
// bypass MFA per IAM-2 design decision 3).
func withMFAPending(ctx context.Context, pending bool) context.Context {
	return context.WithValue(ctx, mfaPendingCtxKey{}, pending)
}

// MFAPendingFrom returns the flag stamped by WithMFAPending. The
// bool is false when the stamp is absent (bearer auth path). The
// ok return distinguishes "not stamped" from "stamped false" so
// RequireMFA can short-circuit correctly on each branch.
//
// Exported so tests + future callers (PR-2 gatewayd-internal AppLogsHandler)
// can inspect without going through WithMFAPending.
func MFAPendingFrom(r *http.Request) (bool, bool) {
	v := r.Context().Value(mfaPendingCtxKey{})
	if v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

// WithMFAPending stamps the mfa-pending flag onto ctx. Exported
// for the same test-seam reason as WithPrincipal: tests need to
// build a context that RequireMFA will read via MFAPendingFrom.
//
// Production code MUST stamp this via RequireSession's
// session-cookie branch (the only writer). A direct call here
// without going through RequireSession would let a request
// bypass the bearer → 402-on-past-due check that RequireSession
// also performs.
func WithMFAPending(ctx context.Context, pending bool) context.Context {
	return withMFAPending(ctx, pending)
}

// stepUpCtxKey is the typed ctx guard for the IAM-hardening-mega-PR
// (logical change 6, ADR-077) step-up timestamp. Carry the
// ISO-8601 stamp the customer's last successful TOTP verify
// landed on so RequireStepUp can compare against its TTL.
type stepUpCtxKey struct{}

// WithStepUp decorates the request context with the step-up
// timestamp read from the cookie envelope. The session-cookie
// branch of RequireSession stamps this; bearer auth doesn't
// carry one (an API key is itself a step-up-equivalent proof —
// the request can't be replayed from a stolen browser without
// the key).
func WithStepUp(ctx context.Context, ts time.Time) context.Context {
	return context.WithValue(ctx, stepUpCtxKey{}, ts)
}

// StepUpFrom returns the stamp added by WithStepUp. The bool is
// false when the stamp is absent (bearer auth path, or pre-
// PR-077 cookie). Exported so tests + future dashboard code can
// inspect without going through WithStepUp.
func StepUpFrom(r *http.Request) (time.Time, bool) {
	v := r.Context().Value(stepUpCtxKey{})
	if v == nil {
		return time.Time{}, false
	}
	ts, ok := v.(time.Time)
	return ts, ok
}
