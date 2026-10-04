package api

import (
	"regexp"
)

// EnvScopePattern accepts every catalog environment slug and preserves the
// existing 40-character limit for legacy deployment scopes (ADR-521). Scope
// names contain lowercase letters, digits and internal hyphens. The catalog
// has its own 33-character limit and reserves DefaultEnvScope. Scope writes
// continue to reject the read-only EnvScopeAllSentinel.
const EnvScopePattern = `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`

// MaxEnvScopeLen bounds the scope name. Mirrors MaxSecretKeyLen /
// MaxOrgSlugLen so the wire limits and DB CHECK share one source.
// 40 chars is the upper bound of the paired database CHECK constraints.
const MaxEnvScopeLen = 40

// EnvScopeAllSentinel is the magic string a client passes in
// `?scope=__all__` to request a nested `env_by_scope` response shape
// (ADR-090 D3). It MUST continue to fail EnvScopePattern so the
// sentinel cannot collide with a real scope name. Rejecting it via
// pattern alone would be brittle (a future relaxation that admits
// underscores would accidentally start accepting "__all__"); instead
// ValidateScope does a two-stage check: dedicated sentinel branch
// first (the load-bearing one), then the regex. See
// ErrEnvScopeReserved for the 400 error code.
const EnvScopeAllSentinel = "__all__"

// DefaultEnvScope is the scope name assigned to (1) every
// pre-PR-B app_envs row via PG11+ fast-default at migration
// 00203 (ADR-090 PR-A) and (2) every pre-PR-D deployments row via
// migration 00213 (ADR-091 / PR-D). The wire shape `?scope=` on
// env routes collapses an empty string to DefaultEnvScope at the
// handler seam (see scopeFromQuery); the schedd loadAPIEnv thread
// does the same defensive collapse so a caller that forgets to
// pass scope doesn't accidentally read a scope='other' row's
// env. The schema and ValidateScope accept it without a special case.
const DefaultEnvScope = "default"

// envScopeRe is the compiled form of EnvScopePattern. Compiled once
// at init so each ValidateScope call is a MatchString and not a
// MustCompile. The pattern is small + constant — no need for sync.Once.
var envScopeRe = regexp.MustCompile(EnvScopePattern)

// ValidateScope returns nil when s is a well-formed scope name; otherwise
// it returns one of:
//
//   - ErrEnvScopeReserved (400) when s == EnvScopeAllSentinel
//     (`__all__`) — the sentinel is reserved for the read-path nested
//     `env_by_scope` response and MUST NOT be set as a scope on
//     write. Distinct from ErrEnvScopeInvalid so a CLI author can
//     tell "you accidentally used the all-scopes sentinel" apart from
//     "your scope name has the wrong shape".
//   - ErrEnvScopeInvalid (400) for any other rejection: empty,
//     exceeds MaxEnvScopeLen, or fails EnvScopePattern.
//
// Returns *Problem directly (not error) so call sites can pass it
// straight to api.WriteProblem without an AsProblem unwrap. This
// matches the contract of ValidateEnvKey / ValidateSecretKey on the
// sibling surfaces in pkg/api/{env,secrets}.go.
//
// Callers: apid's PUT/DELETE /v1/apps/{slug}/envs/{key}?scope=...
// (rejects invalid scope names before they reach the store) and the
// gregale CLI's `env set --scope=...` (same 400 problem code so the
// dashboard renders one consistent error card).
func ValidateScope(s string) *Problem {
	if s == EnvScopeAllSentinel {
		return ErrEnvScopeReserved(EnvScopeAllSentinel)
	}
	if s == "" {
		return ErrEnvScopeInvalid("scope is required")
	}
	if len(s) > MaxEnvScopeLen {
		return ErrEnvScopeInvalid(
			"scope length exceeds max",
		)
	}
	if !envScopeRe.MatchString(s) {
		return ErrEnvScopeInvalid(
			"scope must match " + EnvScopePattern,
		)
	}
	return nil
}
