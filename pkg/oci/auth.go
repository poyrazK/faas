package oci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OCI distribution-spec v2 Bearer-token plumbing, extracted from
// RegistryClient so pkg/storage.OCIRegistryStorageBackend (issue #96
// slice 2) can reuse the same challenge-parse + token-fetch path.
//
// The two callers diverge in exactly one place: the OCIRegistryStorageBackend
// sends Basic credentials to the token endpoint so private repos can be
// pushed to, while RegistryClient stays anonymous for public pulls. The
// optional *BasicAuth parameter threads that single difference through
// without duplicating the URL build, the JSON decode, or the refresh-token
// handling.

// BasicAuth is an optional username/password pair to send on the token
// endpoint. nil means anonymous. Spec-compliant registries expect the
// header on the realm endpoint (NOT on /v2/... requests).
type BasicAuth struct {
	Username string
	Password string
}

// Token is the parsed body of a token-endpoint response. RefreshToken +
// ExpiresIn are optional; many public registries omit refresh_token and
// return long-lived (1h+) access tokens only.
type Token struct {
	// AccessToken is the Bearer value sent on /v2/ requests. Always
	// populated when err == nil.
	AccessToken string
	// RefreshToken is set when the server issued one. The caller tracks
	// it per (realm, scope) and posts it on the next FetchToken call
	// once the access token nears expiry.
	RefreshToken string
	// ExpiresIn is the server-asserted lifetime of AccessToken; zero
	// means "unknown" and the caller should default to a conservative
	// refresh window (we use 1 minute).
	ExpiresIn time.Duration
}

// IsExpiredAt reports whether the token's safe-window has elapsed.
// We treat a token as expired 30 s before its server-asserted ExpiresIn
// to leave headroom for in-flight requests when the refresh itself
// races with a 401.
//
// issued is the wall-clock time the token was received (the
// FetchToken call returned successfully). check is the time the caller
// wants to know "is this expired now?". Two args keep the caller honest
// about clock skew between FetchToken and IsExpiredAt, and let tests
// pin both sides explicitly.
func (t Token) IsExpiredAt(issued, check time.Time) bool {
	if t.ExpiresIn == 0 {
		return false // unknown lifetime; caller decides
	}
	expiry := issued.Add(t.ExpiresIn)
	return check.Add(30 * time.Second).After(expiry)
}

// authChallenge is the parsed subset of a `WWW-Authenticate: Bearer …`
// header.
type authChallenge struct {
	realm   string
	service string
	scope   string
}

// String renders the canonical "realm/service/scope" tuple. Used as a
// cache key by callers (the OCIRegistryStorageBackend's tokenCache).
func (c authChallenge) String() string {
	return c.realm + "|" + c.service + "|" + c.scope
}

// ParseChallenge exports parseChallenge so callers outside the package
// can build an authChallenge from a captured Www-Authenticate header.
// Behaviour mirrors the package-private parseChallenge exactly.
func ParseChallenge(header string) AuthChallenge {
	return newAuthChallenge(parseChallenge(header))
}

// newAuthChallenge is the package-private constructor used by the
// RegistryClient's call sites (parseChallenge is package-private; the
// exported ParseChallenge wraps this for outside callers).
func newAuthChallenge(c authChallenge) AuthChallenge {
	return AuthChallenge{c: c}
}

// AuthChallenge is the exported view of the parsed Bearer challenge.
// The unexported fields stay package-private so callers can't mutate
// realm/service/scope after construction.
type AuthChallenge struct{ c authChallenge }

// Realm returns the token-endpoint URL from the challenge.
func (a AuthChallenge) Realm() string { return a.c.realm }

// Service returns the optional service identifier.
func (a AuthChallenge) Service() string { return a.c.service }

// Scope returns the optional scope string (e.g. "repository:org/app:pull").
func (a AuthChallenge) Scope() string { return a.c.scope }

// parseChallenge extracts realm/service/scope from a Bearer challenge
// header (e.g. `Bearer realm="https://auth/token",service="registry",
// scope="repository:x:pull"`).
func parseChallenge(header string) authChallenge {
	var ch authChallenge
	rest, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return ch
	}
	for _, part := range splitParams(rest) {
		k, v, found := strings.Cut(part, "=")
		if !found {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.TrimSpace(k) {
		case "realm":
			ch.realm = v
		case "service":
			ch.service = v
		case "scope":
			ch.scope = v
		}
	}
	return ch
}

// splitParams splits a challenge's comma-separated key="value" params
// without breaking on commas inside quoted values (scopes can contain
// them).
func splitParams(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// FetchToken performs the Bearer-token GET the WWW-Authenticate challenge
// points at (realm?service=&scope=). When auth is non-nil the request
// carries an "Authorization: Basic" header so private registries that
// require creds at the token endpoint accept the request.
//
// The caller passes a User-Agent (we don't default to anything registry-
// specific here so the same token client can serve both pkg/oci and
// pkg/storage backends). The refresh_token in the response is returned
// to the caller — keeping it package-private lets each caller decide
// its own cache lifetime policy.
func FetchToken(ctx context.Context, hc *http.Client, ua string, ch AuthChallenge, auth *BasicAuth) (Token, error) {
	realm := ch.Realm()
	if realm == "" {
		return Token{}, fmt.Errorf("oci: 401 with no bearer realm; not a public registry?")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm, nil)
	if err != nil {
		return Token{}, fmt.Errorf("oci: build token request: %w", err)
	}
	q := req.URL.Query()
	if svc := ch.Service(); svc != "" {
		q.Set("service", svc)
	}
	if sc := ch.Scope(); sc != "" {
		q.Set("scope", sc)
	}
	req.URL.RawQuery = q.Encode()
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if auth != nil && auth.Username != "" {
		// Distribution-spec compliant registries expect Basic on the
		// token endpoint when the repo is private. The /v2/... calls
		// only carry the Bearer header.
		req.SetBasicAuth(auth.Username, auth.Password)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("oci: fetch token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Token{}, fmt.Errorf("oci: token endpoint returned %d", resp.StatusCode)
	}
	var raw struct {
		Token        string `json:"token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		// ExpiresIn is documented as seconds-since-issue by the
		// distribution spec but in the wild some registries return
		// RFC3339 timestamps. We parse both.
		ExpiresIn   int    `json:"expires_in"`
		IssueExpiry string `json:"expires_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return Token{}, fmt.Errorf("oci: decode token: %w", err)
	}
	access := raw.Token
	if access == "" {
		access = raw.AccessToken
	}
	if access == "" {
		return Token{}, fmt.Errorf("oci: token endpoint returned no token")
	}
	tok := Token{
		AccessToken:  access,
		RefreshToken: raw.RefreshToken,
	}
	if raw.ExpiresIn > 0 {
		tok.ExpiresIn = time.Duration(raw.ExpiresIn) * time.Second
	}
	return tok, nil
}

// TokenRequest is the cached refresh-token state. We POST it on the
// token endpoint (refresh_token grant) instead of GETting a new bearer
// from scratch; the server returns a fresh Token. Spec-compliant
// servers accept this on the realm endpoint with grant_type=refresh_token.
//
// This helper is provided for the OCIRegistryStorageBackend's bearer
// cache; the package itself doesn't cache tokens (RegistryClient is
// stateless and re-fetches on every 401).
func TokenRequest(refreshToken string) url.Values {
	q := url.Values{}
	q.Set("grant_type", "refresh_token")
	q.Set("refresh_token", refreshToken)
	return q
}
