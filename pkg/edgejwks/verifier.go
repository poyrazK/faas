// Package edgejwks — verifier. Verify parses a raw JWT (the
// "Bearer <token>" suffix is already stripped by the caller — handler.go
// does strings.TrimPrefix), fetches the keyset from the per-URL JWKS
// cache, picks the right key by kid, verifies the signature, and
// returns the parsed claims. The closed-set algorithm vocabulary
// (RS256/RS384/RS512/ES256/ES384/ES512) is enforced by
// pkg/api/dto.go::EdgeRuleJWTAction.Validate and repeated at parse time.
// Each selected JWK must bind its `alg` metadata to the JWS algorithm;
// an optional JWK `use` must be `sig` (RFC 8725 §3.1, RFC 7517).
//
// Clock-skew is 60s by default (matches Auth0/Okta/Cognito defaults).
// If a customer later needs a different skew, add an Option.
package edgejwks

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Sentinel errors. handler.go maps each to a distinct audit + metric
// outcome (jwt_missing / jwt_failed). Keep the messages stable — they
// end up in slog and customer-facing 401 bodies.
var (
	ErrJWKSNotRegistered = errors.New("edgejwks: jwks_url not registered")
	ErrJWTMissingToken   = errors.New("edgejwks: missing bearer token")
	ErrJWTBadSignature   = errors.New("edgejwks: bad signature")
	ErrJWTExpired        = errors.New("edgejwks: token expired")
	ErrJWTNotYetValid    = errors.New("edgejwks: token not yet valid")
	ErrJWTWrongIssuer    = errors.New("edgejwks: issuer mismatch")
	ErrJWTWrongAudience  = errors.New("edgejwks: audience mismatch")
	ErrJWTMissingClaim   = errors.New("edgejwks: required claim missing or wrong")
	ErrJWTWrongAlgorithm = errors.New("edgejwks: algorithm not in rule vocabulary")
	ErrJWTKeyMetadata    = errors.New("edgejwks: jwk metadata mismatch")
	ErrJWTNoMatchingKey  = errors.New("edgejwks: no matching kid in jwks")
)

// Claims is the parsed subset pkg/gateway surfaces. Subject/Issuer/Aud
// are copied out of the standard jwt.Claims struct. Custom is a bounded
// string→string subset of safe top-level scalar custom claims. Strings,
// numbers, and booleans are retained; arrays and objects are omitted.
type Claims struct {
	Subject string
	Issuer  string
	Aud     []string
	Exp     time.Time
	Custom  map[string]string
}

const (
	maxCustomClaims     = 64
	maxCustomClaimValue = 256
)

var customClaimNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

var registeredJWTClaims = map[string]struct{}{
	"iss": {}, "sub": {}, "aud": {}, "exp": {}, "nbf": {}, "iat": {}, "jti": {},
}

// Verifier is the narrow interface pkg/gateway sees. cmd-side wires
// the *joseVerifier with a Cache + skew.
type Verifier interface {
	Verify(ctx context.Context, rawToken string, rule VerifierRule) (*Claims, error)
}

// VerifierRule is the minimal projection of *gateway.EdgeRuleJWTResolved
// the verifier needs. Defining it here (instead of importing pkg/gateway)
// keeps the dep direction one-way: pkg/edgejwks → pkg/gateway would be
// wrong (it's a leaf package, same posture as pkg/edgejwks → pkg/state).
//
// RequiredClaims does strict equality (got.(string) == want); good for
// stable, fully-spelled-out claims like `aud=foo`. RequiredClaimPatterns
// does regex matching against string claim values; used for `sub` glob
// shapes like `repo:OWNER/NAME:ref:refs/heads/.*` where the branch varies
// (issue #270 / ADR-101). The two maps coexist — a claim key can appear
// in both, in which case BOTH checks must pass. A nil/empty map on either
// skips that check entirely (backwards-compatible with pre-PR-101 callers).
type VerifierRule struct {
	JWKSURL               string
	Issuer                string
	Audience              []string
	Algorithms            []string
	RequiredClaims        map[string]string
	RequiredClaimPatterns map[string]string
	// ExtractClaims gives downstream policy a verified claim handoff that is
	// independent of RequiredClaims. Named claims are retained before the
	// generic custom-claim cap is filled.
	ExtractClaims []string
}

var allowedSignatureAlgorithms = map[string]struct{}{
	string(jose.RS256): {}, string(jose.RS384): {}, string(jose.RS512): {},
	string(jose.ES256): {}, string(jose.ES384): {}, string(jose.ES512): {},
}

// joseVerifier is the production impl.
type joseVerifier struct {
	cache Cache
	skew  time.Duration
}

// NewVerifier constructs a verifier over the given cache. skew=0
// means "use go-jose default" (60s — matches DefaultLeeway in v4).
func NewVerifier(cache Cache, skew time.Duration) Verifier {
	if skew <= 0 {
		skew = time.Duration(jwt.DefaultLeeway)
	}
	return &joseVerifier{cache: cache, skew: skew}
}

// DefaultSkew matches the Auth0/Okta/Cognito default clock skew
// window. Tunable per verifier via NewVerifier; we keep one global
// default so cmd-side wiring is simple. Kept as an alias to
// jwt.DefaultLeeway (= 1 minute) — preserved here so downstream code
// reads naturally.
const DefaultSkew = time.Duration(jwt.DefaultLeeway)

// Verify validates rawToken against rule. The rawToken is the
// stripped bearer suffix (no "Bearer " prefix).
//
// Algorithm whitelist: rule.Algorithms is passed straight to
// jose.ParseSigned; if the token's `alg` header is not in the list,
// go-jose rejects with ErrUnexpectedSignatureAlgorithm which we map
// to ErrJWTWrongAlgorithm.
//
// Issuer: rule.Issuer is required (apid-Validate enforces
// non-empty); an empty rule.Issuer here is a programming error and
// returns ErrJWTWrongIssuer to fail closed.
//
// Audience: if rule.Audience is non-empty, the token's `aud` must
// intersect it (delegated to jwt.Claims.ValidateWithLeeway's
// AnyAudience intersection). Empty rule.Audience skips the aud check
// entirely (matches apid-Validate semantics — audience is optional
// in the DTO).
//
// RequiredClaims: every k=v in rule.RequiredClaims must equal the
// token's claim[k]. Missing or wrong value → ErrJWTMissingClaim.
// Only string-typed claims are compared (per DTO constraint that
// the rule's required_claims is map[string]string).
func (v *joseVerifier) Verify(ctx context.Context, rawToken string, rule VerifierRule) (*Claims, error) {
	if rule.JWKSURL == "" {
		return nil, fmt.Errorf("%w: rule.JWKSURL empty", ErrJWTWrongAlgorithm)
	}
	if strings.TrimSpace(rule.Issuer) == "" {
		return nil, ErrJWTWrongIssuer
	}
	if rawToken == "" {
		return nil, ErrJWTMissingToken
	}
	if len(rule.Algorithms) == 0 {
		return nil, fmt.Errorf("%w: rule.Algorithms empty", ErrJWTWrongAlgorithm)
	}
	for _, algorithm := range rule.Algorithms {
		if _, ok := allowedSignatureAlgorithms[algorithm]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrJWTWrongAlgorithm, algorithm)
		}
	}
	// Parse the JWS envelope first to extract the kid header. We pass
	// the algorithm whitelist so a token with alg=HS256 (or any
	// alg not in the rule vocabulary) is rejected here at parse
	// time, before we burn a JWKS fetch.
	sigAlgs := make([]jose.SignatureAlgorithm, 0, len(rule.Algorithms))
	for _, a := range rule.Algorithms {
		sigAlgs = append(sigAlgs, jose.SignatureAlgorithm(a))
	}
	jws, err := jose.ParseSigned(rawToken, sigAlgs)
	if err != nil {
		return nil, mapParseError(err)
	}
	hdr := jws.Signatures[0].Header
	kid := hdr.KeyID
	alg := string(hdr.Algorithm)

	// Fetch the keyset (Register must have happened in MatchJWT, but
	// Get is safe even if not — returns ok=false).
	set, registered, err := v.cache.Get(ctx, rule.JWKSURL, kid)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch %s: %w", ErrJWKSNotRegistered, rule.JWKSURL, err)
	}
	if !registered || set == nil {
		return nil, ErrJWKSNotRegistered
	}

	// Pick the key by kid. If kid is empty (some IdPs omit it for
	// single-key sets), try the only key in the set; otherwise
	// reject (no kid + multi-key set is unsafe).
	candidates := set.Key(kid)
	if len(candidates) == 0 {
		if kid == "" && len(set.Keys) == 1 {
			candidates = set.Keys
		} else {
			return nil, fmt.Errorf("%w: kid=%q", ErrJWTNoMatchingKey, kid)
		}
	}
	// Try each candidate — JWKS rotation may publish a stale kid
	// that the IdP no longer signs with, in which case the verify
	// fails; we fall through to the next.
	var lastErr error
	metadataRejected := false
	for _, k := range candidates {
		// RFC 8725 §3.1 requires the key's permitted algorithm to be
		// bound to the JWS header. An omitted `alg` is not a binding;
		// reject it rather than allowing a multi-algorithm rule to
		// select a key for an unintended operation. RFC 7517's `use`
		// is optional, but when present this verifier only accepts
		// signing keys.
		if k.Algorithm != alg || (k.Use != "" && k.Use != "sig") {
			metadataRejected = true
			continue
		}
		// Use the public key for verify. JSONWebKey.Public() drops
		// private material so we never accidentally hand a private
		// key to Verify even if the IdP publishes one.
		pub := k.Public()
		if !pub.Valid() {
			continue
		}
		payload, err := jws.Verify(pub)
		if err != nil {
			lastErr = err
			continue
		}
		var std jwt.Claims
		if err := decodeJSON(payload, &std); err != nil {
			return nil, fmt.Errorf("%w: decode: %w", ErrJWTBadSignature, err)
		}
		// Time-based validation (exp/nbf/iat) with skew.
		exp := jwt.Expected{}
		if rule.Issuer != "" {
			exp.Issuer = rule.Issuer
		}
		if len(rule.Audience) > 0 {
			exp.AnyAudience = jwt.Audience(rule.Audience)
		}
		exp.Time = time.Now()
		if err := std.ValidateWithLeeway(exp, v.skew); err != nil {
			return nil, mapParseError(err)
		}
		// Decode the verified payload once into a generic map. Besides
		// validating required claims, the bounded scalar subset is returned to
		// the gateway for key_by=jwt_claim throttles. This must happen even when
		// the JWT rule has no required_claims: the throttle rule, not the JWT
		// rule, selects which verified claim becomes the bucket dimension.
		generic := map[string]any{}
		if err := decodeJSON(payload, &generic); err != nil {
			return nil, fmt.Errorf("%w: decode claims: %w", ErrJWTMissingClaim, err)
		}
		// RequiredClaims — read raw claim map.
		if len(rule.RequiredClaims) > 0 || len(rule.RequiredClaimPatterns) > 0 {
			for k, want := range rule.RequiredClaims {
				got, present := generic[k]
				if !present {
					return nil, fmt.Errorf("%w: %s", ErrJWTMissingClaim, k)
				}
				s, ok := got.(string)
				if !ok || s != want {
					return nil, fmt.Errorf("%w: %s", ErrJWTMissingClaim, k)
				}
			}
			// RequiredClaimPatterns — regex match against string claim
			// values. Compiled once per Verify call (rare path; the
			// JIT cost is dwarfed by the JWKS fetch); a future ADR
			// can promote this to a per-rule cache if the pattern
			// count grows past ~16. Same missing/wrong-value sentinels
			// as RequiredClaims so the gateway audit row reason is
			// stable across both checks.
			if len(rule.RequiredClaimPatterns) > 0 {
				for k, pat := range rule.RequiredClaimPatterns {
					got, present := generic[k]
					if !present {
						return nil, fmt.Errorf("%w: %s", ErrJWTMissingClaim, k)
					}
					s, ok := got.(string)
					if !ok {
						return nil, fmt.Errorf("%w: %s", ErrJWTMissingClaim, k)
					}
					re, err := regexp.Compile(pat)
					if err != nil {
						// A bad pattern is a server-side bug; fail closed.
						return nil, fmt.Errorf("%w: %s pattern invalid: %w", ErrJWTMissingClaim, k, err)
					}
					if !re.MatchString(s) {
						return nil, fmt.Errorf("%w: %s", ErrJWTMissingClaim, k)
					}
				}
			}
		}
		expTime := time.Time{}
		if std.Expiry != nil {
			expTime = std.Expiry.Time()
		}
		var aud []string
		if len(std.Audience) > 0 {
			aud = append(aud, std.Audience...)
		}
		return &Claims{
			Subject: std.Subject,
			Issuer:  std.Issuer,
			Aud:     aud,
			Exp:     expTime,
			Custom:  boundedCustomClaims(generic, rule.ExtractClaims),
		}, nil
	}
	if lastErr != nil {
		return nil, mapParseError(lastErr)
	}
	if metadataRejected {
		return nil, fmt.Errorf("%w: kid=%q alg=%q", ErrJWTKeyMetadata, kid, alg)
	}
	return nil, fmt.Errorf("%w: no usable key", ErrJWTNoMatchingKey)
}

// boundedCustomClaims returns only safe, top-level scalar custom claims. The
// limits bound both the request-context footprint and the eventual throttle
// bucket key. Explicitly requested names are visited first so a configured
// throttle claim cannot be crowded out by unrelated token claims. Arrays and
// objects are intentionally excluded because their canonicalization and
// cardinality semantics require a separate contract.
func boundedCustomClaims(raw map[string]any, priority []string) map[string]string {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		if _, registered := registeredJWTClaims[key]; registered || !customClaimNamePattern.MatchString(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, min(len(keys), maxCustomClaims))
	seen := make(map[string]struct{}, len(priority))
	ordered := make([]string, 0, len(priority)+len(keys))
	for _, key := range priority {
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, key)
	}
	for _, key := range keys {
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		ordered = append(ordered, key)
	}
	for _, key := range ordered {
		if _, registered := registeredJWTClaims[key]; registered || !customClaimNamePattern.MatchString(key) {
			continue
		}
		var value string
		switch v := raw[key].(type) {
		case string:
			value = v
		case bool:
			value = strconv.FormatBool(v)
		case float64:
			value = strconv.FormatFloat(v, 'g', -1, 64)
		default:
			continue
		}
		if value == "" || len(value) > maxCustomClaimValue {
			continue
		}
		out[key] = value
		if len(out) == maxCustomClaims {
			break
		}
	}
	return out
}

// mapParseError turns the go-jose/jwt error zoo into our sentinels
// so the gateway can distinguish jwt_missing / jwt_failed / jwt_expired
// for audit + metric outcome routing. go-jose v4 returns typed
// sentinels (jwt.ErrExpired, etc.); we map them via errors.Is first
// and fall back to string matching for the untyped errors.
func mapParseError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, jwt.ErrExpired):
		return fmt.Errorf("%w: %w", ErrJWTExpired, err)
	case errors.Is(err, jwt.ErrNotValidYet):
		return fmt.Errorf("%w: %w", ErrJWTNotYetValid, err)
	case errors.Is(err, jwt.ErrInvalidIssuer):
		return fmt.Errorf("%w: %w", ErrJWTWrongIssuer, err)
	case errors.Is(err, jwt.ErrInvalidAudience):
		return fmt.Errorf("%w: %w", ErrJWTWrongAudience, err)
	case errors.Is(err, jwt.ErrIssuedInTheFuture):
		return fmt.Errorf("%w: %w", ErrJWTNotYetValid, err)
	}
	// go-jose v4 wraps ErrUnexpectedSignatureAlgorithm in an
	// exported error type whose String() includes "unexpected
	// signature algorithm"; the substring match below picks it up.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "algorithm"):
		return fmt.Errorf("%w: %w", ErrJWTWrongAlgorithm, err)
	case strings.Contains(msg, "signature"):
		return fmt.Errorf("%w: %w", ErrJWTBadSignature, err)
	default:
		return fmt.Errorf("%w: %w", ErrJWTBadSignature, err)
	}
}

// decodeJSON is a tiny indirection so tests can swap in a
// json.Unmarshal hook without reaching into the import. Production
// callers see the same behavior as encoding/json.Unmarshal.
var decodeJSON = func(data []byte, v any) error {
	return jsonUnmarshal(data, v)
}
