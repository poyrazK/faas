package api

import (
	"fmt"
	"regexp"
	"strings"
)

// Customer env-var DTOs (issue #395 / ADR-045). The wire shape mirrors
// pkg/api/secrets.go EXCEPT there is no sealed-vs-plaintext boundary —
// env values are stored and re-emitted as-is by design (they're
// non-sensitive runtime config, distinct from the secret quota).
//
// Putting this file alongside secrets.go keeps the parallel surface
// obvious to reviewers and to the SDK generator (which walks
// pkg/api/*.go looking for response types).

// PutAppEnvRequest is the PUT /v1/apps/{slug}/env/{key} body. Value is
// stored verbatim in the app_envs table — no seal step, no ciphertext
// column. The byte cap is enforced against Limits.EnvValueMaxBytes by
// Validate() before the row reaches PG (defense in depth — see
// ValidateSecretKey's mirror comment).
type PutAppEnvRequest struct {
	// Value is the plaintext env-var value. Persisted as-is; treated as
	// non-sensitive by contract (issue #395 plaintext rationale +
	// ADR-045 §Decision). Customers must NOT use this surface for
	// credentials — sealed secrets remain the credential store.
	Value string `json:"value"`
}

// Validate enforces the byte cap against maxBytes. Used by apid's PUT
// handler so the cap is checked before the row ever hits the store.
// Returns *Problem directly (not error) so the call site can pass it
// straight to api.WriteProblem without an AsProblem unwrap.
func (r PutAppEnvRequest) Validate(maxBytes int) *Problem {
	if maxBytes > 0 && len(r.Value) > maxBytes {
		return ErrEnvVarValueTooLarge(Limits{EnvValueMaxBytes: maxBytes}, len(r.Value))
	}
	return nil
}

// AppEnvResponse is the GET / list shape. Mirrors AppSecretResponse
// exactly — the value NEVER appears here, only metadata about the env
// var. The customer gets the actual value at process start inside the
// guest (guest-init reads /etc/faas/env.json); the management API
// returns only the key set + timestamps so the dashboard can render
// "env: FOO, BAR" without echoing potentially sensitive defaults.
type AppEnvResponse struct {
	Key       string `json:"key"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ScopedAppEnvResponse is the per-row shape for the nested
// `env_by_scope` response (ADR-090 D3). The flat AppEnvResponse
// shape is unchanged — the only new field is `scope`, which carries
// the scope name on the wire so a CLI / dashboard can render
// "scope: staging" without a second lookup. Value is NEVER echoed
// (same posture as AppEnvResponse).
//
// Reserved for the GET ?scope=__all__ path. The flat
// AppEnvListResponse with a single `env` array is the default
// (and is what every existing SDK caller decodes today). D3
// justifies the discriminated-union shape over a single flattened
// response because a customer on the default scope doesn't need
// scope metadata in every row — a one-off nested response is
// cheaper to render than a per-row scope string.
type ScopedAppEnvResponse struct {
	Scope     string `json:"scope"`
	Key       string `json:"key"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// EnvByScope is the nested map shape returned under `env_by_scope`
// when the GET carries `?scope=__all__`. Keys are scope names
// (e.g. "default", "staging"); values are the rows for that scope,
// ordered by key ASC to match the flat response. The map is nil
// (omitted via omitempty) for the default-scope GET so SDK callers
// that only care about the flat `env` array don't see a new field
// they have to special-case.
type EnvByScope map[string][]ScopedAppEnvResponse

// AppEnvListResponse is the wrapped GET response: the env slice plus
// quota metadata so the CLI can render "3/8 env vars" without a second
// request. Shape mirrors AppSecretListResponse field-for-field —
// SDK callers reuse the same parsing branch.
//
// EnvByScope is the discriminated-union arm for `?scope=__all__`
// (ADR-090 D3). When present, the SDK decodes env_by_scope and
// treats `env` as an empty array; when absent, the SDK decodes `env`
// as the flat per-scope result. Both arms are valid wire shapes
// for a GET /v1/apps/{slug}/envs; the `?scope=` query discriminates.
type AppEnvListResponse struct {
	Env        []AppEnvResponse `json:"env"`
	EnvByScope EnvByScope       `json:"env_by_scope,omitempty"`
	Quota      int              `json:"quota_max"`
	Count      int              `json:"count"`
}

// EnvKeyPattern is the env var name grammar: the POSIX portable name
// (letters, digits and underscore, starting with a letter), case
// sensitive. Runtimes read mixed-case names (.NET's DOTNET_gcServer and
// DOTNET_EnableDiagnostics, Java's -D style tooling), so an uppercase-only
// rule refused real configuration (microservices-demo, 2026-11-16). Secret
// names keep SecretKeyPattern. The app_envs and app_config_revisions CHECKs
// mirror this pattern (migration 20261116100000000).
const EnvKeyPattern = `^[A-Za-z][A-Za-z0-9_]*$`

var envKeyRe = regexp.MustCompile(EnvKeyPattern)

// ValidateEnvKey returns nil when key matches EnvKeyPattern, is within
// MaxSecretKeyLen bytes, and is not another spelling of a platform-provided
// key; otherwise it returns the api.Problem-shaped CodeEnvVarInvalidKey.
// Returns *Problem directly (not error) so call sites can pass it straight
// to api.WriteProblem.
func ValidateEnvKey(key string) *Problem {
	if key == "" {
		return ErrEnvVarInvalidKey("key is required")
	}
	if len(key) > MaxSecretKeyLen {
		return ErrEnvVarInvalidKey(fmt.Sprintf("key length %d exceeds max %d", len(key), MaxSecretKeyLen))
	}
	if !envKeyRe.MatchString(key) {
		return ErrEnvVarInvalidKey("must start with a letter and contain only letters, digits and underscore")
	}
	// Reserved-name checks across the platform compare the uppercase form
	// (PORT, FAAS_*, GREGALE_*, identity keys). A mixed-case spelling would
	// slip past them and confuse anything that folds case, so it is refused.
	if upper := strings.ToUpper(key); upper != key && PlatformProvidedEnvKey(upper) {
		return ErrEnvVarInvalidKey(fmt.Sprintf("%s is reserved for the platform in any letter case", upper))
	}
	return nil
}

// DataUpstreamSource (ADR-098 §D1.a / §D4) is the discriminator on
// how a data_upstreams row was created. Lives in env.go (not a new
// upstreams.go) for the C1 commit shape — the DTOs and validation
// land in C3 (pkg/api/upstreams.go) where the response/request types
// pick up the enum. Two values:
//
//   - DataUpstreamSourceInferred: the apid env-classifier recorded
//     the row from a customer's env (DATABASE_URL,
//     REDIS_URL, ...). The source is the DSN — never stored, only
//     the host + port + scope + kind. Dashboard renders a
//     "we inferred this" badge.
//   - DataUpstreamSourceExplicit: the customer POSTed to
//     /v1/apps/{slug}/upstreams (PR-B / C4). Dashboard renders a
//     "you pinned this" badge.
//
// The SQL CHECK constraint at migration 00226
// (`data_upstreams_source_check`) is the wire-bypass backstop; the
// IsValid helper here is the apid-side closed-vocab check that
// gates before the row reaches the store.
type DataUpstreamSource string

const (
	DataUpstreamSourceInferred DataUpstreamSource = "inferred"
	DataUpstreamSourceExplicit DataUpstreamSource = "explicit"
)

// DataUpstreamSourceIsValid returns true for the two closed
// vocabulary values above. Used by apid's createUpstream handler
// (C4) to reject an unknown source with 400 upstream_invalid_kind
// BEFORE the store is touched. Mirrors the Closed-vocabulary enum
// pattern in EdgeRuleKindIsValid at pkg/api/edge_rules.go.
func DataUpstreamSourceIsValid(s DataUpstreamSource) bool {
	switch s {
	case DataUpstreamSourceInferred, DataUpstreamSourceExplicit:
		return true
	}
	return false
}
