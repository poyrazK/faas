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

// AppSecretResponse is the GET / list shape. The value NEVER appears here —
// only metadata about the secret.
type AppSecretResponse struct {
	Key       string `json:"key"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// AppSecretListResponse is the wrapped GET response: the secrets slice plus
// quota metadata so the CLI can render "3/25 secrets" without a second
// request. Matches the anonymous struct apid emits.
type AppSecretListResponse struct {
	Secrets []AppSecretResponse `json:"secrets"`
	Quota   int                 `json:"quota_max"`
	Count   int                 `json:"count"`
}

// ValidateSecretKey returns nil when key matches ^[A-Z][A-Z0-9_]*$ and is
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
		return ErrSecretInvalidKey("must start with a letter and contain only A-Z, 0-9, underscore")
	}
	return nil
}
