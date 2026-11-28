package api

import (
	"fmt"
	"regexp"
)

// Secret DTOs (spec §11/G2). Plaintext VALUES only appear in PutAppSecretRequest
// and never leave apid except transiently during the seal call
// (pkg/secretbox.Seal). All response shapes omit the value entirely.
//
// Naming mirrors the existing app/cron/domain resource shapes so the CLI
// (cmd/faas) and the dashboard can use the same JSON tags verbatim.

type PutAppSecretRequest struct {
	// Value is the plaintext. Sealed server-side with the host X25519
	// recipient and never persisted in plaintext. Maximum length is
	// enforced against Limits.SecretValueMaxBytes BEFORE the seal so
	// over-cap payloads never reach the seal path.
	Value string `json:"value"`
}

// Validate enforces the byte cap against maxBytes. Used by apid's PUT
// handler so the cap is checked before pkg/secretbox ever sees the value
// (defense in depth — secretbox.SealOne also checks).
//
// Returns *Problem directly (not error) so the call site can pass it
// straight to api.WriteProblem without an AsProblem unwrap.
func (r PutAppSecretRequest) Validate(maxBytes int) *Problem {
	if maxBytes > 0 && len(r.Value) > maxBytes {
		return ErrSecretValueTooLarge(Limits{SecretValueMaxBytes: maxBytes}, len(r.Value))
	}
	return nil
}

// RotateAppSecretRequest is the body for POST
// /v1/apps/{slug}/secrets/{key}/rotate. Same wire shape as
// PutAppSecretRequest; the rotate verb is distinct so the server
// can emit the `secret.rotated` audit kind (vs `secret.set` on
// PUT). Byte cap is the per-plan `SecretValueMaxBytes`.
//
// ADR-092 PR-B: scope is selected via ?scope= on the URL path,
// NOT in the body — the body stays byte-equivalent to pre-PR-B
// callers. Mirrors Python sdk/python/faas_sdk/models/rotate_app_secret_request.py
// and Node sdk/node/src/generated/models/RotateAppSecretRequest.ts.
type RotateAppSecretRequest struct {
	Value string `json:"value"`
}

// RotateAppSecretResponse is the 200 body from
// POST /v1/apps/{slug}/secrets/{key}/rotate. The kid is the age-1...
// recipient string of the host identity that sealed the new envelope
// (ADR-089 D4); rotated_at is RFC3339Nano so two rotates in the same
// second produce distinct timestamps. Empty kid means the row was
// rotated but the kid was not stampable (rare — happens only if apid
// started without host.age.pub, which the handler 503s for instead).
//
// Mirrors Python sdk/python/faas_sdk/models/rotate_app_secret_response.py
// and Node sdk/node/src/generated/models/RotateAppSecretResponse.ts.
type RotateAppSecretResponse struct {
	Key       string `json:"key"`
	RotatedAt string `json:"rotated_at"`
	Kid       string `json:"kid,omitempty"`
}

// AppSecretResponse is the GET / list shape. The value NEVER appears here —
// only metadata about the secret.
//
// ADR-092 PR-B: Scope is the env-scope the row belongs to. Always
// populated — pre-PR-B callers see Scope="default" on every row
// (the column DEFAULT backfill posture). Mirrors the env route's
// AppEnvResponse.Scope echo (ADR-090 PR-B).
//
// Kid is the age-1... recipient string of the host identity that
// sealed this row (ADR-089 PR-B). Pre-PR-A rows have Kid="" (the
// kid column was added in 00166). Empty JSON value for backward
// compat — older SDKs pre-00166 decode without the field.
type AppSecretResponse struct {
	Key       string `json:"key"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Kid       string `json:"kid,omitempty"`
}

// ScopedAppSecretResponse is the per-row shape for the nested
// secrets_by_scope map (ADR-092 PR-B, mirror of ScopedAppEnvResponse).
// Same posture as AppSecretResponse but with an explicit `scope`
// field that is always populated (the flat AppSecretResponse's Scope
// is also always populated, but the nested-map rows are returned when
// the GET carries ?scope=__all__ and the SDK caller switches on the
// presence of SecretsByScope to decode the discriminated union).
type ScopedAppSecretResponse struct {
	Scope     string `json:"scope"`
	Key       string `json:"key"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Kid       string `json:"kid,omitempty"`
}

// SecretByScope is the nested-map shape returned under
// secrets_by_scope when the GET carries ?scope=__all__.
// Type alias (not struct) so the AST parity test in
// cmd/apid/spec_compliance_test.go can filter it out — the test
// only observes struct types, so a named SecretByScope schema
// entry would surface as "schema in spec but no DTO in code".
// Env's EnvByScope uses the same type-alias posture.
type SecretByScope = map[string][]ScopedAppSecretResponse

// AppSecretListResponse is the wrapped GET response: the secrets slice plus
// quota metadata so the CLI can render "3/25 secrets" without a second
// request. Matches the anonymous struct apid emits.
//
// ADR-092 PR-B: discriminated union. When the GET carries
// ?scope=__all__, the server populates SecretsByScope (the
// nested-map arm) and treats Secrets as an empty array. When the
// GET omits ?scope=, the server populates Secrets (the flat arm)
// and SecretsByScope stays nil. SDK callers branch on the
// presence of SecretsByScope to decode the correct arm.
type AppSecretListResponse struct {
	Secrets        []AppSecretResponse `json:"secrets"`
	SecretsByScope SecretByScope       `json:"secrets_by_scope,omitempty"`
	Quota          int                 `json:"quota_max"`
	Count          int                 `json:"count"`
}

// ValidateSecretKey returns nil when key matches ^[A-Za-z][A-Za-z0-9_]*$ and is
// within MaxSecretKeyLen bytes; otherwise it returns the api.Problem-shaped
// CodeSecretInvalidKey. Returns *Problem directly (not error) so call sites
// can pass it straight to api.WriteProblem without an AsProblem unwrap — the
// key validation branch is hot enough that we want to skip the type assert.
//
// Mirror of the SQL CHECK constraint so upstream validation rejects bad keys
// before they reach the DB.
func ValidateSecretKey(key string) *Problem {
	if key == "" {
		return ErrSecretInvalidKey("key is required")
	}
	if len(key) > MaxSecretKeyLen {
		return ErrSecretInvalidKey(fmt.Sprintf("key length %d exceeds max %d", len(key), MaxSecretKeyLen))
	}
	re := regexp.MustCompile(SecretKeyPattern)
	if !re.MatchString(key) {
		return ErrSecretInvalidKey("must start with a letter and contain only letters, digits and underscore")
	}
	return nil
}
