package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"
)

// API keys (spec §4.2, §11). The plaintext key is shown to the user exactly once
// (at creation); only its SHA-256 is stored. Keys are prefixed so they are
// greppable in incident response and detectable in leaked-secret scanners.
const (
	// APIKeyPrefix marks live keys. A test/sandbox prefix can be added later.
	APIKeyPrefix = "fp_live_"
	// APIKeyOIDCKeyPrefix marks short-lived OIDC-derived bearer tokens
	// (issue #270 / ADR-101). Same entropy as APIKeyPrefix (24 bytes)
	// but prefix-disjoint so the two validators don't cross-match.
	// The middleware dispatches on prefix in pkg/auth/middleware.
	APIKeyOIDCKeyPrefix = "fp_oidc_"
	// apiKeyRandomBytes is the entropy behind each key.
	apiKeyRandomBytes = 24
)

// Consumer keys (ADR-120 / issue #975 item #5). End-customer-of-an-app
// credentials; distinct from api_keys because they are scoped to a
// single (account_id, app_id) pair (D1) and exposed to the public
// internet (every customer of the app sees one — hence 2× the
// entropy). Prefix-disjoint from APIKeyPrefix / APIKeyOIDCKeyPrefix
// so the three validators never cross-match.
//
// Wire format: `ck_<8-hex-prefix>_<64-hex-secret>`. The prefix is the
// human-shareable portion (16 bits of randomness per key, ~65k
// possible prefixes; sufficient to namespace keys inside one app).
// The trailing 64 hex chars are 32 bytes of crypto/rand — see
// ADR-120 §D2.
const (
	// ConsumerKeyPrefix marks consumer keys. Greppable in incident
	// response without leaking the secret.
	ConsumerKeyPrefix = "ck_"
	// consumerKeyPrefixBytes is the entropy behind the prefix
	// segment of the plaintext (32 bits = 4 bytes = 8 hex chars).
	// 32 bits chosen because the (app_id, prefix) UNIQUE index in
	// migration 00329 guarantees no two keys share a prefix inside
	// one app — see ADR-120 §D2 for the collision-probability math.
	consumerKeyPrefixBytes = 4
	// consumerKeySecretBytes is the entropy behind the secret
	// segment of the plaintext (256 bits = 32 bytes = 64 hex chars).
	// 1.33× apiKeyRandomBytes (24 bytes) because consumer keys are
	// public-internet-exposed while api_keys only circulate inside
	// the account operator's CI fleet.
	consumerKeySecretBytes = 32
)

// GenerateAPIKey mints a new key, returning the plaintext (to show the user once)
// and its SHA-256 hash (to store). The plaintext is never persisted.
func GenerateAPIKey() (plaintext string, hash []byte, err error) {
	buf := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("api: generate key: %w", err)
	}
	plaintext = APIKeyPrefix + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, sum[:], nil
}

// GenerateOIDCKey mints a short-lived OIDC-derived bearer (ADR-101).
// Same entropy policy as GenerateAPIKey (apiKeyRandomBytes) but
// prefixed APIKeyOIDCKeyPrefix so the wire format is greppable
// separately from long-lived api_keys. Returned plaintext is shown
// to the caller once; the hash is what the store persists on
// oidc_exchanged_tokens.token_hash.
func GenerateOIDCKey() (plaintext string, hash []byte, err error) {
	buf := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("api: generate OIDC key: %w", err)
	}
	plaintext = APIKeyOIDCKeyPrefix + hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, sum[:], nil
}

// HashAPIKey returns the SHA-256 of a plaintext key for lookup/comparison.
func HashAPIKey(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// GenerateConsumerKey mints a new consumer key (ADR-120 / issue #975
// item #5). Wire format `ck_<8-hex-prefix>_<64-hex-secret>`. The
// plaintext is shown to the operator exactly once (PR #5-B's
// handlers_consumer_keys POST response); the SHA-256 hash is what
// the store persists on consumer_keys.hashed_secret.
//
// Returns the plaintext, the hex-encoded prefix segment (for the
// store's `prefix` column — used by the (app_id, prefix) hot-path
// index), and the SHA-256 of the FULL plaintext (NOT of the secret
// alone, so a secret reused across two prefixes produces two
// different hashes — see ADR-120 §Security notes).
func GenerateConsumerKey() (plaintext string, prefix string, hash []byte, err error) {
	prefixBuf := make([]byte, consumerKeyPrefixBytes)
	if _, err := rand.Read(prefixBuf); err != nil {
		return "", "", nil, fmt.Errorf("api: generate consumer key prefix: %w", err)
	}
	secretBuf := make([]byte, consumerKeySecretBytes)
	if _, err := rand.Read(secretBuf); err != nil {
		return "", "", nil, fmt.Errorf("api: generate consumer key secret: %w", err)
	}
	prefixHex := hex.EncodeToString(prefixBuf)
	plaintext = ConsumerKeyPrefix + prefixHex + "_" + hex.EncodeToString(secretBuf)
	sum := sha256.Sum256([]byte(plaintext))
	return plaintext, prefixHex, sum[:], nil
}

// HashConsumerKey returns the SHA-256 of the FULL plaintext
// `ck_<prefix>_<secret>` (ADR-120 §D3). The prefix is part of the
// input, so a secret reused across two prefixes produces two
// different hashes. Lookup path: pgstore narrows to one row via the
// (app_id, prefix) composite index, then compares this hash.
func HashConsumerKey(plaintext string) []byte {
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// ValidConsumerKeyFormat reports whether s looks like a consumer
// key (ADR-120 / issue #975 item #5). Cheap pre-check before
// hitting the database. Prefix-disjoint from ValidAPIKeyFormat and
// ValidOIDCKeyFormat so the three checks never cross-match.
//
// Expected shape: `ck_` + 8 hex chars + `_` + 64 hex chars.
func ValidConsumerKeyFormat(s string) bool {
	if !strings.HasPrefix(s, ConsumerKeyPrefix) {
		return false
	}
	body := strings.TrimPrefix(s, ConsumerKeyPrefix)
	// body must be `<prefix-hex>_<secret-hex>` — find the underscore.
	idx := strings.IndexByte(body, '_')
	if idx != consumerKeyPrefixBytes*2 {
		return false
	}
	prefix := body[:idx]
	secret := body[idx+1:]
	if len(secret) != consumerKeySecretBytes*2 {
		return false
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		return false
	}
	if _, err := hex.DecodeString(secret); err != nil {
		return false
	}
	return true
}

// HashToken returns the SHA-256 of arbitrary raw bytes. Login tokens
// (M7.5 magic link) are random 32-byte values — no API-key prefix —
// so the storage key is the SHA-256 of the raw token.
func HashToken(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

// ValidAPIKeyFormat reports whether s looks like one of our long-lived
// keys (cheap pre-check before hitting the database).
func ValidAPIKeyFormat(s string) bool {
	if !strings.HasPrefix(s, APIKeyPrefix) {
		return false
	}
	body := strings.TrimPrefix(s, APIKeyPrefix)
	if len(body) != apiKeyRandomBytes*2 {
		return false
	}
	_, err := hex.DecodeString(body)
	return err == nil
}

// ValidOIDCKeyFormat reports whether s looks like an OIDC-derived
// short-lived bearer (ADR-101). Prefix-disjoint from ValidAPIKeyFormat
// so the two checks never cross-match. The middleware dispatches on
// this branch after ValidAPIKeyFormat returns false.
func ValidOIDCKeyFormat(s string) bool {
	if !strings.HasPrefix(s, APIKeyOIDCKeyPrefix) {
		return false
	}
	body := strings.TrimPrefix(s, APIKeyOIDCKeyPrefix)
	if len(body) != apiKeyRandomBytes*2 {
		return false
	}
	_, err := hex.DecodeString(body)
	return err == nil
}

// ConstantTimeEqualHash compares two key hashes without leaking timing.
func ConstantTimeEqualHash(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// API-key scopes (IAM-1, ADR-034 rev2). The first merge of the closed
// vocab (admin | read | write) was too coarse: granting `read` to a key
// gave the key every GET across the account surface (apps, usage,
// secrets audit, deployments), and `write` blocked the legitimate
// "deploy-only" CI key from reading the post-deploy logs it needed to
// gate a release. The fine-grained set below lets a customer mint a
// key that can only deploy (deploy:write), only read usage
// (usage:read), only read the app surface (apps:read), or only
// manage secrets (secrets:write) — without granting the other
// surfaces.
//
//	admin        — every action including billing, account deletion,
//	               key management.
//	apps:read    — GET /v1/apps, /v1/apps/{slug}, /v1/deployments,
//	               /v1/deployments/{id}, /v1/deployments/{id}/logs,
//	               /v1/apps/{slug}/instances, /v1/apps/{slug}/logs,
//	               /v1/apps/{slug}/secrets (list only), /v1/keys,
//	               /v1/audit-events, /v1/audit-events/{id},
//	               /v1/invocations, /v1/invocations/{id},
//	               /v1/delayed-tasks/{id}, /v1/account,
//	               /v1/account/export, /v1/crons, /v1/domains.
//	deploy:write — POST/PATCH/DELETE on /v1/apps, /v1/apps/{slug},
//	               /v1/domains, /v1/crons, /v1/invocations/queues/*,
//	               /v1/delayed-tasks, /v1/account/restore,
//	               /v1/apps/{slug}/invoke, /v1/apps/{slug}/invoke/async,
//	               /v1/apps/{slug}/deployments, /v1/apps/{slug}/wake,
//	               /v1/apps/{slug}/park, /v1/apps/{slug}/rollback,
//	               /v1/apps/{slug}/rename.
//	secrets:write— PUT/DELETE /v1/apps/{slug}/secrets/{key}.
//	usage:read   — GET /v1/usage, /v1/usage/summary.
//	storage:manage — bucket lifecycle and per-bucket grant management.
//	storage:read   — object listing/GET signing with a bucket grant.
//	storage:write  — object deletion/PUT signing with a bucket grant.
//
// `admin` implicitly satisfies every other scope check — the
// principalHasScope helper grants any-of. Session-cookie auth (Key ==
// nil) is implicitly admin: humans at the dashboard always have full
// access.
//
// The closed vocabulary is mirrored at the DB layer by migration
// 00046's api_keys_scopes_vocab_chk CHECK constraint (and widened by
// migration 00063 to admit env:read/env:write for issue #395). The Go
// side is the first line; the constraint is the floor a typo cannot
// cross.
const (
	ScopeAdmin        = "admin"
	ScopeAppsRead     = "apps:read"
	ScopeDeployWrite  = "deploy:write"
	ScopeSecretsRead  = "secrets:read"
	ScopeSecretsWrite = "secrets:write"
	ScopeUsageRead    = "usage:read"
	// Issue #395 / ADR-045: env:read scopes the GET endpoint;
	// env:write scopes PUT/DELETE. Distinct codes from secrets:* so
	// the secret-quota bypass argument is closed — a customer can't
	// grant "secrets:write" through an env-var surface.
	ScopeEnvRead  = "env:read"
	ScopeEnvWrite = "env:write"
	// Issue #461 / ADR-062: registry_credentials:read scopes GET;
	// registry_credentials:write scopes PUT/DELETE. Distinct from
	// secrets:* / env:* because the resource is per-app Basic Auth
	// sealed at rest, not per-app env config — a customer who can
	// write env vars cannot write registry credentials without the
	// new scope, closing the quota-bypass path.
	ScopeRegistryCredentialsRead  = "registry_credentials:read"
	ScopeRegistryCredentialsWrite = "registry_credentials:write"
	// ADR-098 §D4: upstreams:write scopes POST/DELETE on
	// /v1/apps/{slug}/upstreams/{id}. Distinct from env:write
	// because the resource is a (kind, host, port) tuple — adding
	// or removing it doesn't touch the env surface. The Free-plan
	// gate (CodePlanDataUpstreamsNotAllowed) is independent of this
	// scope check (a Hobby customer without upstreams:write still
	// can't POST, and a Scale customer with only env:write still
	// can't POST upstreams).
	ScopeUpstreamsWrite = "upstreams:write"
	// Object storage separates control-plane management from data-plane
	// access. Data-plane keys also need an explicit grant for the target
	// bucket; these scopes alone never expose a bucket.
	ScopeStorageManage = "storage:manage"
	ScopeStorageRead   = "storage:read"
	ScopeStorageWrite  = "storage:write"
)

// validScopes is the closed set of scope strings the API accepts. The
// order is not significant — callers can pass scopes in any order.
var validScopes = map[string]struct{}{
	ScopeAdmin:                    {},
	ScopeAppsRead:                 {},
	ScopeDeployWrite:              {},
	ScopeSecretsRead:              {},
	ScopeSecretsWrite:             {},
	ScopeUsageRead:                {},
	ScopeEnvRead:                  {},
	ScopeEnvWrite:                 {},
	ScopeRegistryCredentialsRead:  {},
	ScopeRegistryCredentialsWrite: {},
	ScopeUpstreamsWrite:           {},
	ScopeStorageManage:            {},
	ScopeStorageRead:              {},
	ScopeStorageWrite:             {},
}

// IsValidScope reports whether s is in the allowed scope vocabulary.
func IsValidScope(s string) bool {
	_, ok := validScopes[s]
	return ok
}

// NormalizeCreateKeyScopes validates + defaults + dedupes the requested
// scopes for POST /v1/keys and the CLI exchange path.
//
//	empty   → [admin] (legacy default — preserve current behavior
//	          for SDK callers that have not yet learned about scopes).
//	unknown → error (wrap with %w so handlers can map the error to
//	          a 400 invalid_scope).
//	duplicates → collapsed; order preserved as-given.
//
// Single source of truth: every caller that mints an api_key row
// (handlers_ext.go::createKey, handlers_cli_auth.go::exchangeCliAuthCode)
// funnels through this helper so the DB CHECK constraint added in
// migration 00044 is the only remaining validation surface.
func NormalizeCreateKeyScopes(requested []string) ([]string, error) {
	if len(requested) == 0 {
		return []string{ScopeAdmin}, nil
	}
	seen := make(map[string]struct{}, len(requested))
	out := make([]string, 0, len(requested))
	for _, s := range requested {
		if !IsValidScope(s) {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out, nil
}

// Pre-baked per-route scope sets for the four common patterns in
// cmd/apid/server.go. Adding a new route should pick one of these
// named shapes; the literal scope-list form is reserved for routes
// that need an unusual combination (none today).
//
// Admin is always in every set because principalHasScope uses any-of
// semantics: an admin key always satisfies the route. A non-admin
// key must carry one of the other scopes in the set to be allowed.
var (
	// ScopesAdminOnly: route is destructive/privileged — only admin
	// keys (and session cookies, which are implicitly admin) pass.
	ScopesAdminOnly = []string{ScopeAdmin}

	// ScopesReadSurface: any read across the account's apps,
	// deployments, usage, audit, secrets-list, and config surface.
	// Granted by admin or apps:read.
	ScopesReadSurface = []string{ScopeAdmin, ScopeAppsRead}

	// ScopesDeploymentReadSurface: read one deployment by opaque ID.
	// deploy:write is admitted so a least-privilege CI token can poll the
	// deployment it just created without receiving account-wide apps:read.
	ScopesDeploymentReadSurface = []string{ScopeAdmin, ScopeAppsRead, ScopeDeployWrite}

	// ScopesUsageReadSurface: the two narrow usage endpoints.
	// Granted by admin or usage:read.
	ScopesUsageReadSurface = []string{ScopeAdmin, ScopeUsageRead}

	// ScopesSecretsWriteSurface: PUT/DELETE on
	// /v1/apps/{slug}/secrets/{key}. Granted by admin or
	// secrets:write.
	ScopesSecretsWriteSurface = []string{ScopeAdmin, ScopeSecretsWrite}

	// ScopesEnvWriteSurface: PUT/DELETE on /v1/apps/{slug}/env/{key}
	// (issue #395 / ADR-045). Granted by admin or env:write.
	// NOT MFA-gated because env vars are explicitly non-sensitive
	// runtime config (see handlers_env.go file header for the
	// trust-model rationale + ADR-045 §Decision).
	ScopesEnvWriteSurface = []string{ScopeAdmin, ScopeEnvWrite}

	// ScopesUpstreamWriteSurface: PUT/DELETE on
	// /v1/apps/{slug}/upstreams/{id} (ADR-098 §D4). Granted by
	// admin or upstreams:write. NOT MFA-gated because the explicit
	// POST only adds a hint — it does NOT alter what data leaves
	// the cluster (the env-classifier's inferred path is the
	// authoritative source). The Free-plan gate (402
	// CodePlanDataUpstreamsNotAllowed) is independent of this
	// scope check.
	ScopesUpstreamWriteSurface = []string{ScopeAdmin, ScopeUpstreamsWrite}

	// ScopesRegistryCredentialsReadSurface: GET on
	// /v1/apps/{slug}/registry-credentials (issue #461 / ADR-062).
	// Granted by admin or registry_credentials:read. The password is
	// never returned (AppRegistryCredentialResponse has no Password
	// field), so reading is non-sensitive.
	ScopesRegistryCredentialsReadSurface = []string{ScopeAdmin, ScopeRegistryCredentialsRead}

	// ScopesRegistryCredentialsWriteSurface: PUT/DELETE on
	// /v1/apps/{slug}/registry-credentials (issue #461 / ADR-062).
	// Granted by admin or registry_credentials:write. The handler
	// chain is authLimited → requireMFA → requireScope → handler — the
	// MFA gate is mandatory because PUT replaces a credential that
	// gates a customer's ability to deploy private images.
	ScopesRegistryCredentialsWriteSurface = []string{ScopeAdmin, ScopeRegistryCredentialsWrite}

	// ScopesDeployWriteSurface: every deploy/mutate action except
	// secrets and key/admin operations. Granted by admin or
	// deploy:write.
	ScopesDeployWriteSurface = []string{ScopeAdmin, ScopeDeployWrite}

	// Object-storage route surfaces. Admin remains the universal escape
	// hatch; session-cookie principals are implicitly admin in requireScope.
	ScopesStorageManageSurface = []string{ScopeAdmin, ScopeStorageManage}
	ScopesStorageReadSurface   = []string{ScopeAdmin, ScopeStorageRead}
	ScopesStorageWriteSurface  = []string{ScopeAdmin, ScopeStorageWrite}
	ScopesStorageListSurface   = []string{ScopeAdmin, ScopeStorageManage, ScopeStorageRead, ScopeStorageWrite}
)
