// Package auth verifies application Auth access tokens in Go backends and
// checks permissions against the Auth app's policies (ADR-1190).
//
//	client, err := auth.New("https://shop-auth.gregale.app")
//	if err != nil {
//		return err
//	}
//	mux.Handle("/api/", client.Require(api))
//
//	func api(w http.ResponseWriter, r *http.Request) {
//		claims, _ := auth.FromContext(r.Context())
//		token, _ := auth.BearerToken(r)
//		ok, err := client.Authorize(r.Context(), token, "read", auth.Resource(org, "doc", id))
//		...
//	}
//
// Verify checks a token's ES256 signature against the Auth app's published
// keys, its issuer, its audience and its expiry, without a network call once
// the keys are cached. A token stays valid until it expires, at most 600
// seconds after a sign-out or ban. Authorize asks the Auth app, which also
// checks that the session is still active.
//
// An API or MCP server that third-party OAuth clients call verifies their
// tokens with WithAudience and its resource URL; Claims.ClientID and
// Claims.Scopes then name the client and what its user granted.
package auth

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// Audience is the aud claim of every access token.
	Audience = "authenticated"
	// DefaultLeeway tolerates clock skew on exp and nbf, as Gregale's edge
	// JWT rules do.
	DefaultLeeway = 60 * time.Second

	// Gregale's own verifiers cache the key set for five minutes and refetch
	// it for an unknown key ID at most once every 30 seconds, so a rotated-in
	// key is learnt at once and a revoked key is dropped within five minutes.
	jwksTTL        = 5 * time.Minute
	jwksMinRefetch = 30 * time.Second
	fetchTimeout   = 10 * time.Second
	maxTokenBytes  = 16 << 10
	maxBodyBytes   = 64 << 10
)

var (
	// ErrNoToken means the request carries no bearer token.
	ErrNoToken = errors.New("auth: no bearer token")
	// ErrInvalidToken means the token is malformed, unsigned by the Auth
	// app, for another issuer or audience, or expired.
	ErrInvalidToken = errors.New("auth: invalid access token")
	// ErrExpired is an ErrInvalidToken whose only fault is its expiry.
	ErrExpired = fmt.Errorf("%w: expired", ErrInvalidToken)
	// ErrUnavailable means the Auth app could not be reached.
	ErrUnavailable = errors.New("auth: Auth app unavailable")
)

// Claims are a verified access token's claims.
type Claims struct {
	Subject      string
	SessionID    string
	Role         string
	Email        string
	Phone        string
	AAL          string
	IsAnonymous  bool
	IssuedAt     time.Time
	ExpiresAt    time.Time
	AppMetadata  map[string]any
	UserMetadata map[string]any
	// ClientID names the third-party OAuth client the token was issued to,
	// and Scopes what its user granted it; both are empty for the Auth
	// app's own sessions.
	ClientID string
	Scopes   []string
	// Raw holds every claim, including ones this type does not name.
	Raw map[string]json.RawMessage
}

// Error is a refusal from the Auth app's API: the HTTP status and the stable
// code. errors.Is matches ErrInvalidToken for 401 and ErrUnavailable for 429
// and 5xx.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return fmt.Sprintf("auth: %s (HTTP %d)", e.Code, e.Status) }

func (e *Error) Is(target error) bool {
	switch target {
	case ErrInvalidToken:
		return e.Status == http.StatusUnauthorized
	case ErrUnavailable:
		return e.Status == http.StatusTooManyRequests || e.Status >= 500
	}
	return false
}

// Client verifies tokens issued by one Auth app. It is safe for concurrent
// use; create one per Auth app and reuse it.
type Client struct {
	origin   string
	issuer   string
	audience string
	jwksURL  string
	http     *http.Client
	leeway   time.Duration
	now      func() time.Time

	fetch     sync.Mutex
	mu        sync.Mutex
	keys      map[string]*ecdsa.PublicKey
	fetchedAt time.Time
	attemptAt time.Time
	fetchErr  error
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the client used to reach the Auth app. Redirects are
// never followed.
func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.http = hc } }

// WithLeeway sets the clock-skew tolerance for exp and nbf.
func WithLeeway(d time.Duration) Option { return func(c *Client) { c.leeway = d } }

// WithAudience accepts tokens for audience instead of the Auth app's own
// sessions (Audience). An API or MCP server that third-party OAuth clients
// call passes its resource URL: the Auth app gives a client's token the
// resource the client asked for as its audience.
func WithAudience(audience string) Option { return func(c *Client) { c.audience = audience } }

// New returns a Client for the Auth app at authURL, its origin such as
// https://shop-auth.gregale.app. HTTP is accepted only on a loopback host.
func New(authURL string, opts ...Option) (*Client, error) {
	origin, err := authOrigin(authURL)
	if err != nil {
		return nil, err
	}
	c := &Client{origin: origin, issuer: origin + "/auth/v1", audience: Audience, http: http.DefaultClient, leeway: DefaultLeeway, now: time.Now}
	c.jwksURL = c.issuer + "/.well-known/jwks.json"
	for _, opt := range opts {
		opt(c)
	}
	if c.http == nil || c.leeway < 0 || c.audience == "" {
		return nil, errors.New("auth: invalid option")
	}
	noRedirects := *c.http
	noRedirects.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	c.http = &noRedirects
	return c, nil
}

func authOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("auth: %q is not an Auth app origin such as https://shop-auth.gregale.app", raw)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname())) {
		return "", fmt.Errorf("auth: %q must use HTTPS", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Verify checks token and returns its claims. A failure wraps
// ErrInvalidToken, or ErrUnavailable when the keys could not be fetched.
func (c *Client) Verify(ctx context.Context, token string) (*Claims, error) {
	if len(token) > maxTokenBytes {
		return nil, fmt.Errorf("%w: too long", ErrInvalidToken)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: not a JWT", ErrInvalidToken)
	}
	var header struct {
		Alg  string          `json:"alg"`
		Kid  string          `json:"kid"`
		Crit json.RawMessage `json:"crit"`
	}
	if err := decodeSegment(parts[0], &header); err != nil || header.Alg != "ES256" || header.Kid == "" || header.Crit != nil {
		return nil, fmt.Errorf("%w: header must name an ES256 key", ErrInvalidToken)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, fmt.Errorf("%w: malformed signature", ErrInvalidToken)
	}
	key, err := c.key(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, fmt.Errorf("%w: bad signature", ErrInvalidToken)
	}
	return c.claims(parts[1])
}

func (c *Client) claims(segment string) (*Claims, error) {
	var raw map[string]json.RawMessage
	var p struct {
		Iss          string          `json:"iss"`
		Aud          json.RawMessage `json:"aud"`
		Exp          *float64        `json:"exp"`
		Nbf          *float64        `json:"nbf"`
		Iat          *float64        `json:"iat"`
		Sub          string          `json:"sub"`
		SessionID    string          `json:"session_id"`
		Role         string          `json:"role"`
		Email        string          `json:"email"`
		Phone        string          `json:"phone"`
		AAL          string          `json:"aal"`
		IsAnonymous  bool            `json:"is_anonymous"`
		ClientID     string          `json:"client_id"`
		Scope        string          `json:"scope"`
		AppMetadata  map[string]any  `json:"app_metadata"`
		UserMetadata map[string]any  `json:"user_metadata"`
	}
	if decodeSegment(segment, &raw) != nil || decodeSegment(segment, &p) != nil {
		return nil, fmt.Errorf("%w: malformed claims", ErrInvalidToken)
	}
	now := c.now()
	switch {
	case p.Iss != c.issuer:
		return nil, fmt.Errorf("%w: issuer %q is not %q", ErrInvalidToken, p.Iss, c.issuer)
	case !hasAudience(p.Aud, c.audience):
		return nil, fmt.Errorf("%w: audience is not %q", ErrInvalidToken, c.audience)
	case p.Sub == "":
		return nil, fmt.Errorf("%w: no subject", ErrInvalidToken)
	case p.Exp == nil:
		return nil, fmt.Errorf("%w: no expiry", ErrInvalidToken)
	case now.After(unix(*p.Exp).Add(c.leeway)):
		return nil, ErrExpired
	case p.Nbf != nil && now.Add(c.leeway).Before(unix(*p.Nbf)):
		return nil, fmt.Errorf("%w: not valid yet", ErrInvalidToken)
	}
	claims := &Claims{Subject: p.Sub, SessionID: p.SessionID, Role: p.Role, Email: p.Email, Phone: p.Phone, AAL: p.AAL,
		IsAnonymous: p.IsAnonymous, ExpiresAt: unix(*p.Exp), ClientID: p.ClientID, Scopes: strings.Fields(p.Scope), AppMetadata: p.AppMetadata, UserMetadata: p.UserMetadata, Raw: raw}
	if p.Iat != nil {
		claims.IssuedAt = unix(*p.Iat)
	}
	return claims, nil
}

func hasAudience(aud json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(aud, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(aud, &many) != nil {
		return false
	}
	for _, a := range many {
		if a == want {
			return true
		}
	}
	return false
}

func unix(seconds float64) time.Time { return time.Unix(0, int64(seconds*float64(time.Second))) }

func decodeSegment(segment string, v any) error {
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// key returns the public key kid, refreshing the key set when it is older
// than its TTL or lacks kid. If a refresh fails, cached keys keep working.
func (c *Client) key(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	c.mu.Lock()
	key, fresh := c.keys[kid], c.now().Sub(c.fetchedAt) < jwksTTL
	c.mu.Unlock()
	if key != nil && fresh {
		return key, nil
	}
	if err := c.refresh(ctx); err != nil && key == nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if key = c.keys[kid]; key == nil {
		return nil, fmt.Errorf("%w: unknown signing key %q", ErrInvalidToken, kid)
	}
	return key, nil
}

func (c *Client) refresh(ctx context.Context) error {
	c.fetch.Lock()
	defer c.fetch.Unlock()
	c.mu.Lock()
	recent, lastErr := !c.attemptAt.IsZero() && c.now().Sub(c.attemptAt) < jwksMinRefetch, c.fetchErr
	c.mu.Unlock()
	if recent {
		return lastErr
	}
	// Detached from the caller, so one cancelled request does not fail the
	// refresh for every request in the next 30 seconds.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
	defer cancel()
	keys, err := c.fetchKeys(fetchCtx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attemptAt, c.fetchErr = c.now(), err
	if err == nil {
		c.keys, c.fetchedAt = keys, c.attemptAt
	}
	return err
}

func (c *Client) fetchKeys(ctx context.Context) (map[string]*ecdsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch keys: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil || resp.StatusCode != http.StatusOK || len(body) > maxBodyBytes {
		return nil, fmt.Errorf("%w: fetch keys: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty    string   `json:"kty"`
			Crv    string   `json:"crv"`
			Alg    string   `json:"alg"`
			Use    string   `json:"use"`
			KeyOps []string `json:"key_ops"`
			Kid    string   `json:"kid"`
			X      string   `json:"x"`
			Y      string   `json:"y"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("%w: malformed key set", ErrUnavailable)
	}
	keys := map[string]*ecdsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" || k.Kid == "" || (k.Alg != "" && k.Alg != "ES256") || (k.Use != "" && k.Use != "sig") || (k.KeyOps != nil && !contains(k.KeyOps, "verify")) {
			continue
		}
		if key := p256Key(k.X, k.Y); key != nil {
			keys[k.Kid] = key
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: key set has no ES256 keys", ErrUnavailable)
	}
	return keys, nil
}

// p256Key returns the point (x, y) as a public key, or nil when it is not on
// the curve.
func p256Key(x64, y64 string) *ecdsa.PublicKey {
	x, errX := base64.RawURLEncoding.DecodeString(x64)
	y, errY := base64.RawURLEncoding.DecodeString(y64)
	if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
		return nil
	}
	if _, err := ecdh.P256().NewPublicKey(append(append([]byte{4}, x...), y...)); err != nil {
		return nil
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// Authorize asks the Auth app whether token's user may take action on
// resource (see Resource). The Auth app checks that the session is still
// active, so a signed-out or banned user is refused at once.
func (c *Client) Authorize(ctx context.Context, token, action, resource string) (bool, error) {
	var out struct {
		Allowed bool `json:"allowed"`
	}
	if err := c.post(ctx, token, "/authz/v1/authorize", map[string]string{"action": action, "resource": resource}, &out); err != nil {
		return false, err
	}
	return out.Allowed, nil
}

func (c *Client) post(ctx context.Context, token, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(data, &problem)
		if problem.Code == "" {
			problem.Code = "http_" + fmt.Sprint(resp.StatusCode)
		}
		return &Error{Status: resp.StatusCode, Code: problem.Code}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("auth: malformed response from %s: %w", path, err)
	}
	return nil
}

// Resource names a resource in an organization the way authorization
// policies and gregale_auth.authorize() expect: org/<org id>/<kind>/<name>.
func Resource(org, kind, name string) string { return "org/" + org + "/" + kind + "/" + name }

// BearerToken returns the request's bearer token.
func BearerToken(r *http.Request) (string, error) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

type claimsKey struct{}

// FromContext returns the claims Require stored in ctx.
func FromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(claimsKey{}).(*Claims)
	return claims, ok
}

// Require returns middleware that serves next only for requests with a valid
// access token, whose claims FromContext then returns. It answers 401 for a
// missing or invalid token and 503 when the keys cannot be fetched.
func (c *Client) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := BearerToken(r)
		var claims *Claims
		if err == nil {
			claims, err = c.Verify(r.Context(), token)
		}
		if err != nil {
			status, code, challenge := http.StatusUnauthorized, "not_authenticated", `Bearer error="invalid_token"`
			switch {
			case errors.Is(err, ErrUnavailable):
				status, code, challenge = http.StatusServiceUnavailable, "auth_unavailable", ""
			case errors.Is(err, ErrNoToken):
				challenge = "Bearer"
			}
			if challenge != "" {
				w.Header().Set("WWW-Authenticate", challenge)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"status":%d,"code":%q}`, status, code)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsKey{}, claims)))
	})
}
