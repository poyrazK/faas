// Package servicecaller mints and verifies the assertion the node-local
// service proxy attaches to an internal service call, stating which workload
// it verified as the caller (ADR-206).
//
// Token shape:
//
//	header:   { "alg": "EdDSA", "kid": <node key id> }
//	payload:  { "iss": "gregale.svc",
//	            "sub": <caller app id>,      // who called
//	            "aud": <target app id>,      // who may accept it
//	            "exp": now+ttl,              // ≤30s
//	            "iat": now, "nbf": now,
//	            "jti": <uuidv4>,
//	            "account_id":         <uuid>,
//	            "caller_instance_id": <instance id>,
//	            "caller_env":         "preview" (omitted for production) }
//
// This is deliberately NOT pkg/internalsvc. That package asserts a *platform
// service* ("schedd") to a customer app against a per-service public-key
// allowlist, with the constant audience "gregale.internal". A caller assertion
// has an app-id subject and a per-target audience, so the two contracts would
// only get vaguer by sharing a type.
//
// It is also not pkg/workloadidentity, which mints guest-facing tokens whose
// audience the *application* chooses for cloud federation. Here the platform
// is the asserting party, not the subject.
//
// The audience is the single most load-bearing claim: it binds the assertion
// to one target app, so a service that receives a call cannot replay the
// assertion against a different service in the same account.
package servicecaller

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
)

const (
	// Issuer identifies assertions minted by the service mesh. It is distinct
	// from pkg/internalsvc's "gregale" so a verifier cannot be pointed at the
	// wrong token family by accident.
	Issuer = "gregale.svc"

	// MaxTTL bounds how long an assertion stays valid. It matches ADR-119's
	// internal-token bound: short enough that replay needs a live position on
	// the node bridge, long enough to survive a wake hold without re-minting
	// mid-request.
	MaxTTL = 30 * time.Second

	// EnvPreview is the caller_env value for a call originating in a PR
	// preview app. Production callers omit the claim entirely, so a verifier
	// that ignores it is unaffected.
	EnvPreview = "preview"
)

var (
	// ErrMalformed is a token that is not a parseable EdDSA JWS.
	ErrMalformed = errors.New("servicecaller: malformed assertion")
	// ErrUnknownKey is a token whose kid names no trusted node key. The
	// signature is deliberately NOT checked in this case — there is no key to
	// check it against, and the trusted-key set is the trust boundary.
	ErrUnknownKey = errors.New("servicecaller: unknown signing key")
	// ErrBadSignature is a token whose signature does not verify.
	ErrBadSignature = errors.New("servicecaller: signature verification failed")
	// ErrExpired is a token outside its validity window.
	ErrExpired = errors.New("servicecaller: assertion expired")
	// ErrWrongAudience is a token minted for a different target app. This is
	// the replay guard: without it, any service could present an assertion it
	// received to a sibling service in the same account.
	ErrWrongAudience = errors.New("servicecaller: assertion audience mismatch")
	// ErrWrongIssuer is a token from another family (e.g. an internalsvc
	// platform token) presented where a caller assertion is expected.
	ErrWrongIssuer = errors.New("servicecaller: assertion issuer mismatch")
)

// Assertion is the verified statement about a caller.
type Assertion struct {
	// CallerAppID is the app the proxy observed making the call.
	CallerAppID string
	// TargetAppID is the app the assertion was minted for.
	TargetAppID string
	AccountID   string
	// CallerInstanceID names the specific VM, so a target can correlate a
	// call with a wake timeline or an instance-scoped log.
	CallerInstanceID string
	// CallerEnv is EnvPreview when the caller is a PR preview app, empty for
	// production. A preview has no sibling copy of its dependencies, so this
	// is how a service learns the call is exercising it from a PR.
	CallerEnv string
	// ID is the jti, for de-duplication if a verifier chooses to track it.
	ID string
}

// Claims is the wire payload. Exported so a verifier outside this package can
// decode an assertion it has already validated by other means.
type Claims struct {
	Issuer           string   `json:"iss"`
	Subject          string   `json:"sub"`
	Audience         []string `json:"aud"`
	ExpiresAt        int64    `json:"exp"`
	IssuedAt         int64    `json:"iat"`
	NotBefore        int64    `json:"nbf"`
	ID               string   `json:"jti"`
	AccountID        string   `json:"account_id"`
	CallerInstanceID string   `json:"caller_instance_id,omitempty"`
	CallerEnv        string   `json:"caller_env,omitempty"`
}

// MintInput is what the proxy knows about a call it has already authorized.
type MintInput struct {
	CallerAppID      string
	TargetAppID      string
	AccountID        string
	CallerInstanceID string
	CallerEnv        string
}

// Mint produces an assertion for one call. ttl is clamped to MaxTTL; a
// non-positive ttl uses MaxTTL rather than minting something already expired,
// because a misconfigured operator should get a working short token and not a
// mesh that fails every call.
func Mint(in MintInput, priv ed25519.PrivateKey, kid string, ttl time.Duration, now time.Time) (string, error) {
	switch {
	case strings.TrimSpace(in.CallerAppID) == "":
		return "", errors.New("servicecaller: caller app id is required")
	case strings.TrimSpace(in.TargetAppID) == "":
		return "", errors.New("servicecaller: target app id is required")
	case strings.TrimSpace(kid) == "":
		return "", errors.New("servicecaller: signing key id is required")
	case len(priv) != ed25519.PrivateKeySize:
		return "", fmt.Errorf("servicecaller: private key has wrong size %d, want %d", len(priv), ed25519.PrivateKeySize)
	}
	if ttl <= 0 || ttl > MaxTTL {
		ttl = MaxTTL
	}
	now = now.UTC()

	claims := Claims{
		Issuer:           Issuer,
		Subject:          in.CallerAppID,
		Audience:         []string{in.TargetAppID},
		ExpiresAt:        now.Add(ttl).Unix(),
		IssuedAt:         now.Unix(),
		NotBefore:        now.Unix(),
		ID:               uuid.NewString(),
		AccountID:        in.AccountID,
		CallerInstanceID: in.CallerInstanceID,
		CallerEnv:        in.CallerEnv,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("servicecaller: marshal claims: %w", err)
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), kid),
	)
	if err != nil {
		return "", fmt.Errorf("servicecaller: new signer: %w", err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		return "", fmt.Errorf("servicecaller: sign: %w", err)
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("servicecaller: serialize: %w", err)
	}
	return compact, nil
}

// TrustedKeys maps a node key id to its public key. The caller owns refresh;
// pkg/sched's NodeKeyRegistry is the production source.
type TrustedKeys map[string]ed25519.PublicKey

// Verify checks an assertion and returns what it states. expectedAudience is
// the verifying app's own id — passing an empty value is a wiring error and
// fails closed rather than accepting any audience.
//
// Verification order matters: the key is resolved from the kid BEFORE the
// signature is checked, because without a trusted key there is nothing to
// check against. An unknown kid is therefore ErrUnknownKey and never reaches
// the signature step. This mirrors pkg/internalsvc's two-pass shape.
func Verify(token string, keys TrustedKeys, expectedAudience string, now time.Time) (Assertion, error) {
	if strings.TrimSpace(token) == "" {
		return Assertion{}, ErrMalformed
	}
	if strings.TrimSpace(expectedAudience) == "" {
		return Assertion{}, fmt.Errorf("%w: no expected audience configured", ErrWrongAudience)
	}
	if len(keys) == 0 {
		return Assertion{}, ErrUnknownKey
	}
	// EdDSA-only: an explicit algorithm allowlist defeats substitution
	// attacks where a token claims alg=none, or alg=HS256 so the public key
	// is used as an HMAC secret.
	jws, err := jose.ParseSigned(token, []jose.SignatureAlgorithm{jose.EdDSA})
	if err != nil {
		return Assertion{}, fmt.Errorf("%w: parse: %w", ErrMalformed, err)
	}
	if len(jws.Signatures) != 1 {
		return Assertion{}, fmt.Errorf("%w: want exactly one signature", ErrMalformed)
	}
	kid := jws.Signatures[0].Header.KeyID
	pub, ok := keys[kid]
	if !ok || len(pub) != ed25519.PublicKeySize {
		return Assertion{}, fmt.Errorf("%w: kid %q", ErrUnknownKey, kid)
	}
	payload, err := jws.Verify(pub)
	if err != nil {
		return Assertion{}, fmt.Errorf("%w: %w", ErrBadSignature, err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Assertion{}, fmt.Errorf("%w: decode claims: %w", ErrMalformed, err)
	}
	if claims.Issuer != Issuer {
		return Assertion{}, fmt.Errorf("%w: got %q", ErrWrongIssuer, claims.Issuer)
	}
	if !audienceMatches(claims.Audience, expectedAudience) {
		return Assertion{}, fmt.Errorf("%w: got %v", ErrWrongAudience, claims.Audience)
	}
	if err := checkWindow(claims, now.UTC()); err != nil {
		return Assertion{}, err
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return Assertion{}, fmt.Errorf("%w: empty subject", ErrMalformed)
	}
	return Assertion{
		CallerAppID:      claims.Subject,
		TargetAppID:      expectedAudience,
		AccountID:        claims.AccountID,
		CallerInstanceID: claims.CallerInstanceID,
		CallerEnv:        claims.CallerEnv,
		ID:               claims.ID,
	}, nil
}

// checkWindow rejects an assertion outside [nbf, exp]. No leeway is granted:
// both ends of the hop are platform components on hosts whose clocks the
// operator controls, and a 30 s window is already generous. Silently accepting
// skew here would widen the replay window for every call in the fleet.
func checkWindow(claims Claims, now time.Time) error {
	if claims.ExpiresAt == 0 || now.Unix() >= claims.ExpiresAt {
		return fmt.Errorf("%w: exp %d, now %d", ErrExpired, claims.ExpiresAt, now.Unix())
	}
	if claims.NotBefore != 0 && now.Unix() < claims.NotBefore {
		return fmt.Errorf("%w: nbf %d, now %d", ErrExpired, claims.NotBefore, now.Unix())
	}
	return nil
}

func audienceMatches(audience []string, want string) bool {
	for _, entry := range audience {
		if entry == want {
			return true
		}
	}
	return false
}
