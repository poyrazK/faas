// Package internalsvc mints and verifies the JWT that Gregale
// daemons attach to outbound requests when calling a customer
// app whose public_auth_mode='internal_only' (ADR-119).
//
// Token shape:
//
//	header:   { "alg": "EdDSA", "kid": <KidFromPub(pub)> }
//	payload:  { "iss": "gregale",
//	            "sub": <svcName>,             // e.g. "schedd"
//	            "aud": "gregale.internal",
//	            "exp": now+ttl,               // ≤30s in v1.0
//	            "iat": now,
//	            "nbf": now,
//	            "jti": <uuidv4>,
//	            "kid": "<kid param>",
//	            "app_id": <optional uuid> }   // for cross-check at the gate
//
// The verifier holds a per-service public-key allowlist keyed
// by canonical service name. A token whose `sub` is not in the
// map fails verification. This is the per-service allowlist
// promised in ADR-118's "Out of scope" note (line 239-243):
//
//	"a separate aud='gregale.internal' mint path, and a
//	 per-service allowlist."
//
// Library: github.com/go-jose/go-jose/v4 (already in go.mod,
// used by pkg/edgejwks for external customer JWTs). EdDSA via
// crypto/ed25519 (already in go.mod via golang.org/x/crypto).
// No new dependencies added.
//
// Audit redaction: callers MUST NOT pass the token string to
// any audit logger. The verifier returns a typed error with a
// stable reason code (aud_mismatch, expired, sig_invalid,
// unknown_svc) so the gateway can log the reason without the
// token itself.
package internalsvc

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

// Audience is the JWT audience claim that the gateway's
// internal_only gate verifies. Pinned at package level so a
// future contributor cannot accidentally split the constant
// across the mint and verify surfaces (the drift-guard test
// for pkg/api + pkg/state + pkg/gateway catches the
// public_auth_mode enum, but the audience string is its own
// contract).
const Audience = "gregale.internal"

// Issuer is the JWT issuer claim. Pinned at "gregale" for
// v1.0; future multi-tenant Gregale deployments would extend
// this to "gregale.<host>" but the cross-PR precheck for the
// public_auth surface does not require it.
const Issuer = "gregale"

// VerifyLeeway is the clock-skew tolerance for exp / nbf
// claims. go-jose's DefaultLeeway is 1 minute; we use the
// same default here for parity with pkg/edgejwks (whose
// leeway comment references the same value).
const VerifyLeeway = 1 * time.Minute

// ErrAudienceMismatch is returned by Verify when the token's
// aud claim is not Audience. Stable sentinel so callers can
// errors.Is against it without parsing the error message.
var ErrAudienceMismatch = errors.New("internalsvc: aud claim does not match gregale.internal")

// ErrExpired is returned by Verify when the token's exp
// claim is in the past (beyond VerifyLeeway).
var ErrExpired = errors.New("internalsvc: token expired")

// ErrNotYetValid is returned by Verify when the token's nbf
// claim is in the future (beyond VerifyLeeway).
var ErrNotYetValid = errors.New("internalsvc: token not yet valid")

// ErrUnknownService is returned by Verify when the token's
// sub claim is not in the per-service allowlist. This is the
// per-service allowlist contract from ADR-119.
var ErrUnknownService = errors.New("internalsvc: svcName not in per-service allowlist")

// ErrSignatureInvalid is returned by Verify when the
// signature does not match the public key for the claimed
// svcName (either wrong key, tampered signature, or wrong
// algorithm).
var ErrSignatureInvalid = errors.New("internalsvc: signature invalid")

// ErrMalformed is returned by Verify when the token cannot be
// parsed (base64, JSON, or header shape is wrong).
var ErrMalformed = errors.New("internalsvc: token malformed")

// ErrEmptyAllowlist is returned by Verify when the per-service
// allowlist is empty. Defensive — the allowlist is the trust
// boundary, an empty map is a misconfiguration.
var ErrEmptyAllowlist = errors.New("internalsvc: per-service allowlist must not be empty")

// Mint signs a JWT with aud=Audience, sub=svcName, ttl=ttl,
// and merges claims into the payload. Returns the compact
// serialization (header.payload.signature) suitable for
// `Authorization: Bearer <token>`.
//
// svcName is the canonical Gregale daemon name
// (e.g. "schedd", "meterd", "imaged", "builderd"). It must be
// a key in the verifier's allowlist at the call site, or the
// token will be rejected on the round-trip.
//
// claims is merged into the payload after the standard
// claims. Used to attach app_id for cross-check at the gate.
//
// priv is the Ed25519 private key. Caller is responsible for
// loading it from /etc/faas/secrets/internal-svc/<svcName>.ed25519
// or sealed via host.age in the GREGALE_INTERNAL_SVC namespace.
//
// kid is the key identifier embedded in the JWS header. By
// convention it is KidFromPub(pub) so the verifier can
// identify the key without trusting the svcName alone — but
// the verifier here uses svcName directly (the allowlist IS
// the trust boundary). kid is kept in the payload for
// diagnostic / rotation-tracking purposes.
func Mint(svcName string, ttl time.Duration, claims map[string]any,
	priv ed25519.PrivateKey, kid string) (string, error) {
	return MintWithAudience(svcName, ttl, claims, priv, kid, Audience)
}

// MintWithAudience is the test-only mint that takes an explicit
// audience. Production callers (cmd/schedd/internal_svc_minter.go)
// MUST use Mint (audience hardcoded to gregale.internal);
// MintWithAudience exists so tests can mint tokens with a wrong
// audience and exercise the gate's reason-mapping end-to-end.
//
// Round-2 peer-review #3 surfaced the need: a test that mints
// with aud='foo' is the only way to pin the gate's
// 'audience_mismatch' reason code against a live Verify call —
// the bridge in cmd/gatewayd-internal/internal_svc_verifier.go
// preserves the typed error from internalsvc.Verify verbatim,
// so the substring-match table in pkg/gateway/internal_svc_auth.go
// is what classifies it. To keep production callers from
// accidentally varying the audience (and breaking the §3
// ADR-119 trust contract), the function name carries the
// "MintWith" prefix — a future reader scanning the call sites
// will see "Mint" in cmd/schedd and "MintWithAudience" in
// *test.go files only. There is no compile-time guard; the
// convention is comment-enforced.
func MintWithAudience(svcName string, ttl time.Duration, claims map[string]any,
	priv ed25519.PrivateKey, kid, audience string) (string, error) {
	if svcName == "" {
		return "", errors.New("internalsvc.Mint: svcName must not be empty")
	}
	// ttl <= 0 is allowed (used by tests that mint
	// expired tokens). Operators SHOULD pass a positive
	// value — anything <=0 produces an already-expired
	// token. We log a warning at the call site (caller's
	// responsibility) but do not fail Mint, since
	// generating an expired token is a legitimate test
	// operation.
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("internalsvc.Mint: priv key has wrong size %d, want %d", len(priv), ed25519.PrivateKeySize)
	}

	now := time.Now().UTC()
	pub := priv.Public().(ed25519.PublicKey)

	// Auto-derive kid if not provided. Round-3 peer-review
	// #7 (kid format divergence): prior to KidFromPub, this
	// branch used hex-of-[:8] while cmd/schedd/internal_svc_
	// minter.go::kidFromPub used base64-of-[:16] — different
	// shapes for the same key. Now both call KidFromPub.
	// Used for JWS header kid claim AND the payload kid
	// field (informational only; verifier keys off svcName).
	if kid == "" {
		kid = KidFromPub(pub)
	}

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: priv},
		(&jose.SignerOptions{}).
			WithType("JWT").
			WithHeader(jose.HeaderKey("kid"), kid),
	)
	if err != nil {
		return "", fmt.Errorf("internalsvc.Mint: build signer: %w", err)
	}

	// Build the payload as a map directly — go-jose v4's
	// jwt.Claims does not expose ToMap / FromMap; the
	// library expects callers to construct the payload
	// themselves when they need custom claim merging.
	payload := map[string]any{
		"iss": Issuer,
		"sub": svcName,
		"aud": audience,
		"exp": jwt.NewNumericDate(now.Add(ttl)),
		"iat": jwt.NewNumericDate(now),
		"nbf": jwt.NewNumericDate(now),
		"jti": uuid.NewString(),
		"kid": kid,
	}
	for k, v := range claims {
		// Defensive: do not allow callers to override the
		// standard claims via the merge map. The standard
		// claims are the trust boundary; allowing override
		// would let a caller mint a token whose `aud` is
		// not Audience.
		switch k {
		case "iss", "sub", "aud", "exp", "iat", "nbf", "jti", "kid":
			continue
		}
		payload[k] = v
	}

	jws, err := signer.Sign(mustJSONMarshal(payload))
	if err != nil {
		return "", fmt.Errorf("internalsvc.Mint: sign: %w", err)
	}
	return jws.CompactSerialize()
}

// Verify parses a compact JWT, verifies the signature against
// the per-service public-key allowlist, checks aud / exp /
// nbf, and returns the verified svcName (== token's sub).
//
// allowedSvc is the per-service public-key allowlist keyed by
// canonical service name. A token whose `sub` is not in the
// map fails verification with ErrUnknownService — this is
// the per-service allowlist contract.
//
// Returns the typed errors listed at package level
// (ErrAudienceMismatch, ErrExpired, ErrNotYetValid,
// ErrUnknownService, ErrSignatureInvalid, ErrMalformed,
// ErrEmptyAllowlist) so callers can match via errors.Is and
// avoid string-matching on the error message.
func Verify(token string, allowedSvc map[string]ed25519.PublicKey) (string, error) {
	if token == "" {
		return "", ErrMalformed
	}
	if len(allowedSvc) == 0 {
		return "", ErrEmptyAllowlist
	}

	// Parse with EdDSA-only algorithm whitelist. go-jose v4
	// will reject any token whose `alg` header is not in the
	// list (defends against algorithm-substitution attacks
	// where a token claims alg=none or alg=HS256 and tries
	// to use the public key as the HMAC secret).
	jws, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return "", fmt.Errorf("%w: parse: %w", ErrMalformed, err)
	}

	// Two-pass verification: parse the unverified claims to
	// find the svcName, look up the corresponding public key
	// in the allowlist, then verify the signature against
	// that key. A token whose svcName is not in the
	// allowlist fails at the lookup step with
	// ErrUnknownService — the signature is NOT verified in
	// that case (we don't have a key to verify against).
	// This is intentional: the per-service allowlist is the
	// trust boundary, not the signature alone.
	unverified := jws.UnsafePayloadWithoutVerification()
	if len(unverified) == 0 {
		return "", fmt.Errorf("%w: empty payload", ErrMalformed)
	}
	parsed := jwt.Claims{}
	if err := json.Unmarshal(unverified, &parsed); err != nil {
		return "", fmt.Errorf("%w: claims parse: %w", ErrMalformed, err)
	}
	if parsed.Subject == "" {
		return "", fmt.Errorf("%w: empty sub", ErrUnknownService)
	}
	pub, ok := allowedSvc[parsed.Subject]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownService, parsed.Subject)
	}

	// Second pass: verify the signature against the
	// looked-up public key. If this fails, the token was
	// tampered or signed by an unknown key — reject with
	// ErrSignatureInvalid regardless of any other claim
	// validity (signature is the root of trust).
	payload, err := jws.Verify(pub)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrSignatureInvalid, err)
	}
	finalClaims := jwt.Claims{}
	if err := json.Unmarshal(payload, &finalClaims); err != nil {
		return "", fmt.Errorf("%w: final claims parse: %w", ErrMalformed, err)
	}

	// Audience check (the gate's primary contract: the token
	// must be issued for gregale.internal, not for some
	// other service). go-jose's Claims.ValidateWithLeeway
	// also checks aud, but we do an explicit Contains check
	// first so we can return the typed ErrAudienceMismatch
	// without ambiguity.
	if !finalClaims.Audience.Contains(Audience) {
		return "", ErrAudienceMismatch
	}

	// Time-bound claims (exp / nbf) with leeway. Issuer
	// claim is checked implicitly (the allowlist lookup
	// keyed by sub already implies a known issuer). We pass
	// AnyAudience here for symmetry with our explicit
	// Audience.Contains check above — the explicit check
	// already rejected a non-matching audience with the
	// typed error, so by the time ValidateWithLeeway runs
	// any audience is acceptable.
	expErr := finalClaims.ValidateWithLeeway(jwt.Expected{AnyAudience: jwt.Audience{Audience}}, VerifyLeeway)
	if expErr != nil {
		switch {
		case errors.Is(expErr, jwt.ErrExpired):
			return "", ErrExpired
		case errors.Is(expErr, jwt.ErrNotValidYet), errors.Is(expErr, jwt.ErrIssuedInTheFuture):
			return "", ErrNotYetValid
		default:
			return "", fmt.Errorf("internalsvc.Verify: validate: %w", expErr)
		}
	}

	return finalClaims.Subject, nil
}

// StringClaim returns a string claim from token's payload without checking
// the signature, or "" when the token or claim is malformed. Call it only on
// a token Verify accepted: it reads which app a verified schedd token was
// minted for.
func StringClaim(token, name string) string {
	jws, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(jws.UnsafePayloadWithoutVerification(), &claims) != nil {
		return ""
	}
	v, _ := claims[name].(string)
	return v
}

// GenerateKeypair returns a fresh Ed25519 keypair. Convenience
// for local dev and tests; production code paths load the
// keypair from disk at boot (cmd/schedd/main.go etc.).
func GenerateKeypair() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("internalsvc.GenerateKeypair: %w", err)
	}
	return priv, pub, nil
}

// KidFromPub is the canonical kid derivation. Returns
// base64url(sha256(pubkey)[:16]) — same shape Mint uses when
// kid=="" and the schedd minter derives at boot. Round-3
// peer-review #7 (kid format divergence): before this helper
// existed, schedd's cmd/schedd/internal_svc_minter.go::kidFromPub
// and pkg/internalsvc/internalsvc.go's auto-derive produced
// different strings (base64-of-16 vs hex-of-8), so a token
// minted with kid="" by some other daemon would not collide
// with a schedd token — confusing for diagnostic logs that key
// off kid to identify the minter. Single source of truth here:
//
//	22 chars (16 bytes × 8/6 base64url), URL-safe,
//	truncated sha256, collision risk negligible at the
//	≤10-service fleet size.
//
// Both surfaces call this; no other code path is allowed to
// derive a kid from a pubkey (drift guard: a future contributor
// who adds another derivation will produce a token whose kid
// doesn't match what the rest of the system expects).
func KidFromPub(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// mustJSONMarshal marshals the JWT payload to bytes for the
// go-jose Signer. Marshalling the payload ourselves (rather
// than letting go-jose marshal a struct) is required because
// the standard claim types (jwt.NumericDate, jwt.Audience,
// uuid.UUID) implement json.Marshaler and the verifier path
// uses json.Unmarshal on the same shape — keeping the two
// surfaces symmetric avoids an asymmetric-encoding bug where
// Mint produces a token that Verify cannot parse.
func mustJSONMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Mint builds a deterministic payload from
		// stdlib types — json.Marshal should never fail.
		// Panic surfaces a programming error at the
		// call site rather than letting an unsigned
		// token escape.
		panic(fmt.Sprintf("internalsvc.mustJSONMarshal: %v", err))
	}
	return b
}
