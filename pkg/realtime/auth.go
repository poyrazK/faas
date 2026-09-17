package realtime

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// AuthMode selects how a managed endpoint authenticates WebSocket clients.
// The empty mode is accepted on the wire for compatibility and is normalized
// to static_bearer when AuthToken is present, otherwise none.
type AuthMode string

const (
	AuthModeNone         AuthMode = "none"
	AuthModeStaticBearer AuthMode = "static_bearer"
	AuthModeOIDCJWT      AuthMode = "oidc_jwt"
)

// AuthPolicy is the non-secret client-authentication configuration replicated
// from apid to realtimed. Static bearer credentials remain in AuthToken and
// are never part of this policy.
type AuthPolicy struct {
	Mode           AuthMode
	Issuer         string
	JWKSURL        string
	Audience       []string
	Algorithms     []string
	RequiredClaims map[string]string
}

// JWTAuthorizer verifies one bearer token and returns its stable principal.
// realtimed supplies the production JWKS-backed implementation; tests and
// embedders can inject a deterministic authorizer.
type JWTAuthorizer interface {
	Authorize(context.Context, string, AuthPolicy) (string, error)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

// normalizeAuthPolicy applies the compatibility default and rejects unsafe
// or incomplete endpoint auth configuration before it reaches the registry.
func normalizeAuthPolicy(policy AuthPolicy, hasStaticToken bool) (AuthPolicy, error) {
	policy.Mode = AuthMode(strings.TrimSpace(string(policy.Mode)))
	if policy.Mode == "" {
		if hasStaticToken {
			policy.Mode = AuthModeStaticBearer
		} else {
			policy.Mode = AuthModeNone
		}
	}
	switch policy.Mode {
	case AuthModeNone:
		if hasStaticToken {
			return AuthPolicy{}, fmt.Errorf("realtime: auth mode none cannot include a static token")
		}
		policy.Issuer, policy.JWKSURL = "", ""
		policy.Audience, policy.Algorithms = nil, nil
		policy.RequiredClaims = nil
	case AuthModeStaticBearer:
		if !hasStaticToken {
			return AuthPolicy{}, fmt.Errorf("realtime: static_bearer requires auth token")
		}
		policy.Issuer, policy.JWKSURL = "", ""
		policy.Audience, policy.Algorithms = nil, nil
		policy.RequiredClaims = nil
	case AuthModeOIDCJWT:
		if hasStaticToken {
			return AuthPolicy{}, fmt.Errorf("realtime: oidc_jwt cannot include a static token")
		}
		if err := validateJWTPolicy(policy); err != nil {
			return AuthPolicy{}, err
		}
	default:
		return AuthPolicy{}, fmt.Errorf("realtime: unsupported auth mode %q", policy.Mode)
	}
	policy.Audience = append([]string(nil), policy.Audience...)
	policy.Algorithms = append([]string(nil), policy.Algorithms...)
	if len(policy.RequiredClaims) > 0 {
		claims := make(map[string]string, len(policy.RequiredClaims))
		for key, value := range policy.RequiredClaims {
			claims[key] = value
		}
		policy.RequiredClaims = claims
	}
	return policy, nil
}

func validateJWTPolicy(policy AuthPolicy) error {
	if policy.Issuer == "" || !strings.HasPrefix(policy.Issuer, "https://") {
		return fmt.Errorf("realtime: oidc_jwt issuer must start with https://")
	}
	if len(policy.Issuer) > 2048 {
		return fmt.Errorf("realtime: oidc_jwt issuer is too long")
	}
	if !strings.HasPrefix(policy.JWKSURL, "https://") {
		return fmt.Errorf("realtime: oidc_jwt jwks_url must start with https://")
	}
	u, err := url.Parse(policy.JWKSURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("realtime: oidc_jwt jwks_url must be an https URL without credentials, query, or fragment")
	}
	lower := strings.ToLower(policy.JWKSURL)
	for _, prefix := range []string{
		"https://localhost", "https://localhost.", "https://127.",
		"https://10.", "https://192.168.", "https://169.254.",
		"https://[::", "https://[fc", "https://[fd",
	} {
		if strings.HasPrefix(lower, prefix) {
			return fmt.Errorf("realtime: oidc_jwt jwks_url must not point to a private or loopback address")
		}
	}
	if len(policy.Audience) > 16 || len(policy.Algorithms) == 0 || len(policy.Algorithms) > 16 {
		return fmt.Errorf("realtime: oidc_jwt audience and algorithms are bounded to 16 entries")
	}
	allowed := map[string]struct{}{
		"RS256": {}, "RS384": {}, "RS512": {},
		"ES256": {}, "ES384": {}, "ES512": {},
	}
	seen := make(map[string]struct{}, len(policy.Algorithms))
	for _, algorithm := range policy.Algorithms {
		if _, ok := allowed[algorithm]; !ok {
			return fmt.Errorf("realtime: oidc_jwt algorithm %q is not supported", algorithm)
		}
		if _, ok := seen[algorithm]; ok {
			return fmt.Errorf("realtime: oidc_jwt algorithms must be unique")
		}
		seen[algorithm] = struct{}{}
	}
	if len(policy.RequiredClaims) > 16 {
		return fmt.Errorf("realtime: oidc_jwt required_claims are bounded to 16 entries")
	}
	for key, value := range policy.RequiredClaims {
		if strings.TrimSpace(key) != key || key == "" || len(key) > 128 || len(value) > 256 {
			return fmt.Errorf("realtime: oidc_jwt required_claims contain an invalid entry")
		}
	}
	return nil
}

func bearerToken(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
