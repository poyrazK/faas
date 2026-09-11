// Per-request authenticated-identity carrier (issue #881 Phase 3,
// ADR-104). The authn branches (enforceRequireAuthn for API keys,
// applyEdgeRuleJWT for JWKS-verified tokens) populate this struct
// and stamp it on the request context so downstream appliers —
// today only applyEdgeRuleThrottle — can read the resolved
// operator or end-customer identity without re-running the auth chain.
//
// Phase 1+2 (PR #887, ADR-091 D20.5 amendment 3) shipped
// kind=throttle keyed only on `appID+"\x00"+ruleID`. The authn chain
// already resolved the consumer identity for audit emission (see
// the `key_id` field at handler.go:1022 and the `sub` field at
// :1698) but the values were dropped at the audit boundary, never
// reaching the rate limiter. Phase 3 plumbs them through.
//
// APIKeyID, ConsumerID, and ConsumerKeyID are zero-valued unless
// their respective authn
// branch ran on this request. Anonymous traffic has APIKeyID ==
// "" and JWTSubject == ""; applyEdgeRuleThrottle treats that as a
// single anonymous bucket per rule (the same scope today already
// had — back-compat preserved).
package gateway

import "context"

// authenticatedKey is the unexported context key used to thread the
// per-request Authenticated struct. Uniqueness is guaranteed by the
// empty-struct type — only this file declares it, so collisions with
// other context keys in the codebase are structurally impossible.
type authenticatedKey struct{}

// Authenticated is the read-only slice of resolved request identity
// that downstream appliers need to key a per-consumer rate-limit
// bucket. Populated lazily by the authn branches:
//
//   - enforceRequireAuthn (handler.go:962) stamps APIKeyID on the
//     success path. Today the value is dropped after the audit emit
//     at line 1022; Phase 3 keeps it via withAuthenticated.
//
//   - applyEdgeRuleJWT (handler.go:1658) stamps JWTSubject and
//     JWTClaims after JWKS verification succeeds. Custom claims
//     are sourced from pkg/edgejwks.Claims.Custom — a string→string
//     subset; non-string claim values are dropped at the verifier
//     and never reach this struct.
//
// All fields are zero-valued until their respective branch ran.
// Anonymous traffic (no authn chain OR RequireAuthn=false on the
// app) reads as a zero Authenticated — callers must handle that
// explicitly (applyEdgeRuleThrottle collapses anonymous traffic on a
// per-consumer rule to a single anonymous bucket, NOT to per-IP
// sub-keying — the latter is explicitly out of scope per ADR-091
// D20.5 amendment 3, and ADR-104 §Rejected alternatives preserves
// the deferral).
//
// Thread-safety: this struct is per-request. Concurrent reads from
// a single handler goroutine are safe; the context carrier API
// (withAuthenticated / authenticatedFrom) is not safe to call from
// multiple goroutines on the same request context.
type Authenticated struct {
	// APIKeyID is the resolved API key identifier (state.APIKey.ID)
	// for requests that satisfied enforceRequireAuthn. Empty for
	// anonymous traffic OR for traffic on apps with
	// RequireAuthn=false. Used by KeyBy == "api_key" or
	// "consumer_id" to construct the per-consumer bucket key.
	APIKeyID string

	// ConsumerID is the stable end-customer identity resolved from a
	// consumer key. It survives key rotation and is the identifier future
	// quota, metering, and billing paths must use. Empty means that no
	// consumer key was accepted for the request.
	ConsumerID string

	// ConsumerKeyID is the specific credential used for this request. It is
	// useful for revocation/last-used diagnostics, while ConsumerID remains
	// the stable attribution dimension.
	ConsumerKeyID string

	// JWTSubject is the `sub` claim from a JWKS-verified token for
	// requests that satisfied applyEdgeRuleJWT. Empty for traffic
	// that did not hit a JWT edge rule (or whose JWT verification
	// failed — Phase 3 stamps on the success path only). Used by
	// KeyBy == "jwt_subject" to construct the per-consumer bucket
	// key.
	JWTSubject string

	// JWTClaims is the string→string subset of custom claims the
	// rule required (pkg/edgejwks.Claims.Custom). Empty when no
	// JWT was verified OR when the rule did not require any custom
	// claims. Used by KeyBy == "jwt_claim" to look up the named
	// claim (rule.JWTClaimName) for the per-consumer bucket key.
	// Non-string claim values are intentionally absent — Phase 3
	// does not attempt coercion (see ADR-104 §Out of scope).
	JWTClaims map[string]string
}

// withAuthenticated stamps a on ctx and returns the new context.
// Subsequent calls REPLACE the struct (the latest authn branch
// wins) — only one branch is expected to populate any given
// request in production (an API-key-gated app with a JWT rule
// would reject the request at the authn step before the JWT
// branch runs; both branches running on the same request is a
// wiring bug, not a normal case).
func withAuthenticated(ctx context.Context, a Authenticated) context.Context {
	return context.WithValue(ctx, authenticatedKey{}, a)
}

// authenticatedFrom returns the Authenticated struct stamped on
// ctx, or the zero value when none was stamped. Callers MUST treat
// the zero value as "anonymous traffic" and route it to the
// back-compat bucket key (see ADR-104 §Implementation seams).
func authenticatedFrom(ctx context.Context) Authenticated {
	if v, ok := ctx.Value(authenticatedKey{}).(Authenticated); ok {
		return v
	}
	return Authenticated{}
}

// resolveConsumerKey picks the per-consumer bucket key suffix
// (ADR-104 §Implementation seams) from the Authenticated struct
// stamped on the request. Returns (consumerID, true) when a
// non-empty identity is available for the requested dimension;
// (consumerID == "", false) when the request is anonymous on the
// requested dimension — caller (applyEdgeRuleThrottle) treats
// that as "the per-rule bucket already throttled, let anonymous
// traffic through the per-consumer layer" (the documented
// back-compat posture; a future hardening may 401 anonymous
// traffic against a per-consumer rule explicitly).
//
// The consumerID must NOT equal ConsumerKeySentinel — if a
// customer manages to inject "__other__" via keyBy="api_key" +
// a forged API key, that collision would let them share the
// pinned collapse bucket with the attacker's traffic. The
// limiter constructor also rejects the sentinel, but defending
// at the key-construction site keeps the rule readable and the
// rejection easy to trace.
//
// KeyBy / claimName / authed come from the cmd-side compiled
// resolved rule + the per-request Authenticated struct; the
// helper is pure (no I/O, no globals).
func resolveConsumerKey(keyBy, claimName string, authed Authenticated) (string, bool) {
	switch keyBy {
	case "api_key", "apiKey":
		if authed.APIKeyID == "" {
			return "", false
		}
		return authed.APIKeyID, true
	case "consumer_id", "consumerID":
		if authed.ConsumerID == "" {
			return "", false
		}
		return authed.ConsumerID, true
	case "jwt_subject", "jwtSubject":
		if authed.JWTSubject == "" {
			return "", false
		}
		return authed.JWTSubject, true
	case "jwt_claim", "jwtClaim":
		if claimName == "" {
			// Rule with key_by="jwt_claim" must carry a
			// JWTClaimName — compileThrottleRules rejects this
			// at cmd-side, but defending here keeps the helper
			// safe to call from any future caller.
			return "", false
		}
		v, ok := authed.JWTClaims[claimName]
		if !ok || v == "" {
			return "", false
		}
		return v, true
	default:
		return "", false
	}
}
