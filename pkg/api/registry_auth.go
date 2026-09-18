package api

// registry_auth.go holds the wire shapes for per-app private-registry
// Basic Auth (issue #461 / ADR-062). Mirrors secrets.go posture:
// plaintext PASSWORD only appears in PutAppRegistryCredentialRequest
// and never leaves apid except transiently during secretbox.Seal.
// All response shapes omit the password entirely.
//
// The Registry field is the normalized host (lowercased, no scheme,
// no path, port preserved). apid's handler normalizes at the boundary
// (cmd/apid/handlers_registry_auth.go::normalizeRegistryHost) and the
// store stays opaque — the SELECT predicate matches the exact stored
// bytes.

import (
	"fmt"
	"regexp"
)

// MaxRegistryHostLen mirrors the SQL CHECK constraint
// (length(registry) <= 253, RFC 1035 hostname cap).
const MaxRegistryHostLen = 253

// MaxRegistryUsernameLen mirrors the SQL CHECK constraint
// (length(username) <= 256). Username is metadata, not sealed.
const MaxRegistryUsernameLen = 256

// MaxRegistryPasswordBytes is the per-request plaintext cap. The
// Basic Auth password for a private registry is typically 16–64
// bytes; 4 KiB mirrors SecretValueMaxBytes for the cheapest plan
// (Free) so a misconfigured PUT fails closed at the gate.
const MaxRegistryPasswordBytes = 4 * 1024

// registryHostRe is the normalized-host shape accepted by
// normalizeRegistryHost: a lowercased DNS name (letters/digits/hyphens
// separated by dots) optionally followed by :port. Anchored.
//
// Per-label cap is 63 (RFC 1035 §2.3.4); the {1,61} repetition
// bracket on the inner chars + a single anchor char yields exactly
// 63 — same shape the standard library uses. Port range is
// 1..65535 (1..5 digits, value-bound by the second branch).
//
// IPv6 literals are NOT accepted — the OCI client uses DNS names and
// the user-facing UX is "registry hostname". A customer who genuinely
// needs an IPv6 literal can pin via a hostname alias. This is the
// conservative gate; loosening requires an ADR.
var registryHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*(:([1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5]))?$`)

// RegistryHostRe exposes the compiled regex so the CLI (cmd/gregale)
// can mirror the same gate locally before round-tripping. Returns the
// same compiled *regexp.Regexp the package-internal Validate() uses;
// zero allocation, same underlying object. Mirrors the api.Plans
// export pattern from pkg/api/limits.go:34.
func RegistryHostRe() *regexp.Regexp {
	return registryHostRe
}

// PutAppRegistryCredentialRequest is the PUT body. Registry MUST be
// already normalized by the handler (lowercase, no scheme, no path,
// no trailing slash); the handler's normalizeRegistryHost is the
// single source of truth so apid, imaged, and the e2e harness agree.
type PutAppRegistryCredentialRequest struct {
	Registry string `json:"registry"`
	Username string `json:"username"`
	// Password is plaintext at the wire boundary only. apid seals it
	// via secretbox.SealBytes before persisting; the plaintext is
	// never logged, audited, or echoed in error responses. The
	// Validate() method enforces MaxRegistryPasswordBytes.
	Password string `json:"password"`
}

// Validate enforces the per-field byte caps. Returns *Problem directly
// so call sites can pass it straight to api.WriteProblem.
func (r PutAppRegistryCredentialRequest) Validate() *Problem {
	if r.Registry == "" {
		return ErrInvalidRegistryHost(fmt.Errorf("registry is required"))
	}
	if len(r.Registry) > MaxRegistryHostLen {
		return ErrInvalidRegistryHost(fmt.Errorf("registry length %d exceeds %d", len(r.Registry), MaxRegistryHostLen))
	}
	if !registryHostRe.MatchString(r.Registry) {
		return ErrInvalidRegistryHost(fmt.Errorf("registry %q does not match normalized host pattern (lowercase DNS[:port])", r.Registry))
	}
	if r.Username == "" {
		return ErrInvalidRegistryHost(fmt.Errorf("username is required"))
	}
	if len(r.Username) > MaxRegistryUsernameLen {
		return ErrInvalidRegistryHost(fmt.Errorf("username length %d exceeds %d", len(r.Username), MaxRegistryUsernameLen))
	}
	if r.Password == "" {
		return ErrInvalidRegistryHost(fmt.Errorf("password is required"))
	}
	if len(r.Password) > MaxRegistryPasswordBytes {
		return ErrInvalidRegistryHost(fmt.Errorf("password length %d exceeds %d", len(r.Password), MaxRegistryPasswordBytes))
	}
	return nil
}

// AppRegistryCredentialResponse is the GET / PUT response shape.
// The password NEVER appears here — only the metadata needed to
// identify the credential. LastUsedAt is RFC 3339, omitempty for the
// fresh-row case (the field exists to mirror the row, but never
// carrying a password means it carries no sensitive state).
type AppRegistryCredentialResponse struct {
	Registry   string `json:"registry"`
	Username   string `json:"username"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// AppRegistryCredentialListResponse is the wrapped GET response:
// rows + quota metadata so the dashboard can render "2/5 hosts"
// without a second request.
type AppRegistryCredentialListResponse struct {
	Credentials []AppRegistryCredentialResponse `json:"credentials"`
	QuotaMax    int                             `json:"quota_max"`
	Count       int                             `json:"count"`
}

// PutJobRegistryCredentialRequest is the job-scoped equivalent of
// PutAppRegistryCredentialRequest. It intentionally has a distinct schema so
// generated SDKs expose the job resource without coupling it to app routes.
type PutJobRegistryCredentialRequest struct {
	Registry string `json:"registry"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r PutJobRegistryCredentialRequest) Validate() *Problem {
	return PutAppRegistryCredentialRequest(r).Validate()
}

// JobRegistryCredentialResponse never contains the sealed or plaintext
// password; it is metadata only.
type JobRegistryCredentialResponse struct {
	Registry   string `json:"registry"`
	Username   string `json:"username"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

type JobRegistryCredentialListResponse struct {
	Credentials []JobRegistryCredentialResponse `json:"credentials"`
	QuotaMax    int                             `json:"quota_max"`
	Count       int                             `json:"count"`
}
