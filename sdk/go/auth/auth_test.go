package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type signingKey struct {
	kid  string
	priv *ecdsa.PrivateKey
}

func newKey(t *testing.T, kid string) signingKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signingKey{kid: kid, priv: priv}
}

func (k signingKey) jwk() map[string]any {
	return map[string]any{"kty": "EC", "crv": "P-256", "alg": "ES256", "kid": k.kid, "key_ops": []string{"verify"},
		"x": b64(k.priv.X.FillBytes(make([]byte, 32))), "y": b64(k.priv.Y.FillBytes(make([]byte, 32)))}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (k signingKey) sign(t *testing.T, header, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	unsigned := b64(h) + "." + b64(c)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + b64(append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...))
}

// authApp serves a JWKS and /authz/v1/authorize like the Auth app.
type authApp struct {
	mu        sync.Mutex
	keys      []map[string]any
	jwksHits  atomic.Int32
	jwksFails atomic.Bool
	authorize func(w http.ResponseWriter, r *http.Request)
	server    *httptest.Server
}

func newAuthApp(t *testing.T, keys ...signingKey) *authApp {
	app := &authApp{}
	app.setKeys(keys...)
	app.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v1/.well-known/jwks.json":
			app.jwksHits.Add(1)
			if app.jwksFails.Load() {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			app.mu.Lock()
			defer app.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": app.keys})
		case "/authz/v1/authorize":
			app.authorize(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(app.server.Close)
	return app
}

func (a *authApp) setKeys(keys ...signingKey) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.keys = nil
	for _, k := range keys {
		a.keys = append(a.keys, k.jwk())
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClient(t *testing.T, app *authApp) (*Client, *clock) {
	t.Helper()
	c, err := New(app.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	c.now = clk.now
	return c, clk
}

func userClaims(c *Client, clk *clock) map[string]any {
	return map[string]any{"iss": c.issuer, "aud": "authenticated", "sub": "0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70",
		"session_id": "5c1b9d2e-0b5f-4d8f-8a3a-7a512f0e6f70", "role": "authenticated", "email": "ada@example.com", "aal": "aal1",
		"iat": clk.t.Unix(), "exp": clk.t.Add(600 * time.Second).Unix(), "app_metadata": map[string]any{"provider": "email"}}
}

func header(kid string) map[string]any {
	return map[string]any{"alg": "ES256", "typ": "JWT", "kid": kid}
}

func TestVerifyAcceptsAnAuthAppToken(t *testing.T) {
	key := newKey(t, "0A1B2C3D")
	app := newAuthApp(t, key)
	c, clk := newClient(t, app)
	claims, err := c.Verify(context.Background(), key.sign(t, header(key.kid), userClaims(c, clk)))
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70" || claims.SessionID == "" || claims.Email != "ada@example.com" || claims.AAL != "aal1" ||
		claims.Role != "authenticated" || !claims.ExpiresAt.Equal(clk.t.Add(600*time.Second)) || !claims.IssuedAt.Equal(clk.t) ||
		claims.AppMetadata["provider"] != "email" || string(claims.Raw["aal"]) != `"aal1"` {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestVerifyRefusesTokensItShouldNot(t *testing.T) {
	key, other := newKey(t, "0A1B2C3D"), newKey(t, "0A1B2C3D")
	app := newAuthApp(t, key)
	c, clk := newClient(t, app)
	with := func(change func(map[string]any)) string {
		claims := userClaims(c, clk)
		change(claims)
		return key.sign(t, header(key.kid), claims)
	}
	valid := with(func(map[string]any) {})
	parts := strings.Split(valid, ".")
	for name, tc := range map[string]struct {
		token string
		want  error
	}{
		"other issuer":    {with(func(m map[string]any) { m["iss"] = "https://evil.example/auth/v1" }), ErrInvalidToken},
		"other audience":  {with(func(m map[string]any) { m["aud"] = "service" }), ErrInvalidToken},
		"audience list":   {with(func(m map[string]any) { m["aud"] = []string{"service", "other"} }), ErrInvalidToken},
		"no subject":      {with(func(m map[string]any) { delete(m, "sub") }), ErrInvalidToken},
		"no expiry":       {with(func(m map[string]any) { delete(m, "exp") }), ErrInvalidToken},
		"expired":         {with(func(m map[string]any) { m["exp"] = clk.t.Add(-61 * time.Second).Unix() }), ErrExpired},
		"not valid yet":   {with(func(m map[string]any) { m["nbf"] = clk.t.Add(61 * time.Second).Unix() }), ErrInvalidToken},
		"service token":   {key.sign(t, header(key.kid), map[string]any{"role": "service_role", "iat": clk.t.Unix(), "exp": clk.t.Add(time.Minute).Unix()}), ErrInvalidToken},
		"other key":       {other.sign(t, header(key.kid), userClaims(c, clk)), ErrInvalidToken},
		"tampered claims": {parts[0] + "." + b64([]byte(`{"sub":"x"}`)) + "." + parts[2], ErrInvalidToken},
		"HS256":           {b64([]byte(`{"alg":"HS256","kid":"0A1B2C3D"}`)) + "." + parts[1] + "." + parts[2], ErrInvalidToken},
		"none":            {b64([]byte(`{"alg":"none"}`)) + "." + parts[1] + ".", ErrInvalidToken},
		"no key ID":       {key.sign(t, map[string]any{"alg": "ES256"}, userClaims(c, clk)), ErrInvalidToken},
		"critical header": {key.sign(t, map[string]any{"alg": "ES256", "kid": key.kid, "crit": []string{"b64"}}, userClaims(c, clk)), ErrInvalidToken},
		"two segments":    {parts[0] + "." + parts[1], ErrInvalidToken},
		"short signature": {parts[0] + "." + parts[1] + "." + b64([]byte("short")), ErrInvalidToken},
		"oversized token": {strings.Repeat("a", maxTokenBytes+1), ErrInvalidToken},
		"unknown key":     {key.sign(t, header("FFFFFFFF"), userClaims(c, clk)), ErrInvalidToken},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Verify(context.Background(), tc.token); !errors.Is(err, tc.want) {
				t.Fatalf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
	expiredWithinLeeway := with(func(m map[string]any) {
		m["exp"] = clk.t.Add(-59 * time.Second).Unix()
		m["aud"] = []string{"authenticated"}
	})
	if _, err := c.Verify(context.Background(), expiredWithinLeeway); err != nil {
		t.Fatalf("a token expired within the leeway is refused: %v", err)
	}
}

func TestVerifyLearnsRotatedKeysAndForgetsRevokedOnes(t *testing.T) {
	old, next := newKey(t, "0A1B2C3D"), newKey(t, "1B2C3D4E")
	app := newAuthApp(t, old)
	c, clk := newClient(t, app)
	ctx := context.Background()
	if _, err := c.Verify(ctx, old.sign(t, header(old.kid), userClaims(c, clk))); err != nil {
		t.Fatal(err)
	}
	// A rotation publishes the new key before it signs: an unknown key ID
	// refetches at once.
	app.setKeys(old, next)
	clk.advance(31 * time.Second)
	if _, err := c.Verify(ctx, next.sign(t, header(next.kid), userClaims(c, clk))); err != nil {
		t.Fatalf("rotated-in key: %v", err)
	}
	if hits := app.jwksHits.Load(); hits != 2 {
		t.Fatalf("JWKS fetched %d times, want 2", hits)
	}
	// Unknown key IDs refetch at most once every 30 seconds.
	clk.advance(31 * time.Second)
	for i := 0; i < 5; i++ {
		_, _ = c.Verify(ctx, next.sign(t, header("FFFFFFFF"), userClaims(c, clk)))
	}
	if hits := app.jwksHits.Load(); hits != 3 {
		t.Fatalf("JWKS fetched %d times after unknown key IDs, want 3", hits)
	}
	// Revoking the old key reaches the verifier when the cache expires.
	app.setKeys(next)
	oldToken := old.sign(t, header(old.kid), userClaims(c, clk))
	if _, err := c.Verify(ctx, oldToken); err != nil {
		t.Fatalf("cached key refused before the cache expired: %v", err)
	}
	clk.advance(jwksTTL)
	if _, err := c.Verify(ctx, old.sign(t, header(old.kid), userClaims(c, clk))); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked key after the cache expired: %v", err)
	}
}

func TestVerifyKeepsCachedKeysWhileTheAuthAppIsDown(t *testing.T) {
	key := newKey(t, "0A1B2C3D")
	app := newAuthApp(t, key)
	c, clk := newClient(t, app)
	ctx := context.Background()
	app.jwksFails.Store(true)
	if _, err := c.Verify(ctx, key.sign(t, header(key.kid), userClaims(c, clk))); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no cached keys: %v, want ErrUnavailable", err)
	}
	app.jwksFails.Store(false)
	clk.advance(jwksMinRefetch)
	if _, err := c.Verify(ctx, key.sign(t, header(key.kid), userClaims(c, clk))); err != nil {
		t.Fatal(err)
	}
	app.jwksFails.Store(true)
	clk.advance(jwksTTL)
	if _, err := c.Verify(ctx, key.sign(t, header(key.kid), userClaims(c, clk))); err != nil {
		t.Fatalf("stale keys refused while the Auth app is down: %v", err)
	}
}

func TestFetchKeysSkipsKeysItCannotUse(t *testing.T) {
	good := newKey(t, "0A1B2C3D")
	app := newAuthApp(t)
	offCurve := good.jwk()
	offCurve["kid"], offCurve["y"] = "BAD00001", b64(make([]byte, 32))
	signOnly := good.jwk()
	signOnly["kid"], signOnly["key_ops"] = "BAD00002", []string{"sign"}
	rsa := map[string]any{"kty": "RSA", "kid": "BAD00003", "n": "AQAB", "e": "AQAB"}
	app.keys = []map[string]any{offCurve, signOnly, rsa, good.jwk()}
	c, _ := newClient(t, app)
	keys, err := c.fetchKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[good.kid] == nil {
		t.Fatalf("keys = %v", keys)
	}
	app.keys = []map[string]any{rsa}
	if _, err := c.fetchKeys(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("key set without ES256 keys: %v", err)
	}
}

func TestRequire(t *testing.T) {
	key := newKey(t, "0A1B2C3D")
	app := newAuthApp(t, key)
	c, clk := newClient(t, app)
	handler := c.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := FromContext(r.Context())
		if !ok {
			t.Error("no claims in context")
			return
		}
		_, _ = io.WriteString(w, claims.Subject)
	}))
	serve := func(authorization string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := serve("bearer " + key.sign(t, header(key.kid), userClaims(c, clk))); rec.Code != 200 || rec.Body.String() != "0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70" {
		t.Fatalf("valid token: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(""); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != "Bearer" || rec.Body.String() != `{"status":401,"code":"not_authenticated"}` {
		t.Fatalf("no token: %d %q %s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body)
	}
	if rec := serve("Basic abc"); rec.Code != 401 {
		t.Fatalf("basic credentials: %d", rec.Code)
	}
	if rec := serve("Bearer a.b.c"); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != `Bearer error="invalid_token"` {
		t.Fatalf("invalid token: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	app.jwksFails.Store(true)
	clk.advance(jwksTTL)
	unknown := key.sign(t, header("FFFFFFFF"), userClaims(c, clk))
	if rec := serve("Bearer " + unknown); rec.Code != 503 || rec.Body.String() != `{"status":503,"code":"auth_unavailable"}` {
		t.Fatalf("Auth app down: %d %s", rec.Code, rec.Body)
	}
}

func TestAuthorize(t *testing.T) {
	app := newAuthApp(t)
	c, _ := newClient(t, app)
	var got struct {
		authorization, contentType string
		body                       map[string]string
	}
	status, response := 200, `{"allowed":true}`
	app.authorize = func(w http.ResponseWriter, r *http.Request) {
		got.authorization, got.contentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}
	resource := Resource("6f1c0d1e-0000-4000-8000-000000000001", "doc", "42")
	allowed, err := c.Authorize(context.Background(), "tok", "read", resource)
	if err != nil || !allowed {
		t.Fatalf("Authorize = %v, %v", allowed, err)
	}
	if got.authorization != "Bearer tok" || got.contentType != "application/json" || got.body["action"] != "read" || got.body["resource"] != "org/6f1c0d1e-0000-4000-8000-000000000001/doc/42" {
		t.Fatalf("request = %+v", got)
	}
	status, response = 200, `{"allowed":false}`
	if allowed, err := c.Authorize(context.Background(), "tok", "write", resource); err != nil || allowed {
		t.Fatalf("denied: %v, %v", allowed, err)
	}
	for _, tc := range []struct {
		status int
		body   string
		is     error
		code   string
	}{
		{401, `{"status":401,"code":"not_authenticated"}`, ErrInvalidToken, "not_authenticated"},
		{503, `{"status":503,"code":"auth_unavailable"}`, ErrUnavailable, "auth_unavailable"},
		{400, `{"status":400,"code":"invalid_request"}`, nil, "invalid_request"},
		{502, `bad gateway`, ErrUnavailable, "http_502"},
	} {
		status, response = tc.status, tc.body
		_, err := c.Authorize(context.Background(), "tok", "read", resource)
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Code != tc.code || (tc.is != nil && !errors.Is(err, tc.is)) {
			t.Fatalf("HTTP %d: %v", tc.status, err)
		}
		if tc.is == nil && (errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrUnavailable)) {
			t.Fatalf("HTTP %d matched a sentinel: %v", tc.status, err)
		}
	}
	app.server.Close()
	if _, err := c.Authorize(context.Background(), "tok", "read", resource); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Auth app unreachable: %v", err)
	}
}

func TestNewAcceptsOnlyAnAuthAppOrigin(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://shop-auth.gregale.app":         true,
		"https://shop-auth.gregale.app/":        true,
		"http://127.0.0.1:8080":                 true,
		"http://localhost:8080":                 true,
		"http://shop-auth.gregale.app":          false,
		"https://shop-auth.gregale.app/auth/v1": false,
		"https://user@shop-auth.gregale.app":    false,
		"https://shop-auth.gregale.app?x=1":     false,
		"shop-auth.gregale.app":                 false,
		"":                                      false,
	} {
		c, err := New(raw)
		if (err == nil) != ok {
			t.Fatalf("New(%q) error = %v", raw, err)
		}
		if ok && (strings.HasSuffix(c.issuer, "//auth/v1") || !strings.HasSuffix(c.jwksURL, "/auth/v1/.well-known/jwks.json")) {
			t.Fatalf("New(%q) issuer %q", raw, c.issuer)
		}
	}
	if _, err := New("https://shop-auth.gregale.app", WithHTTPClient(nil)); err == nil {
		t.Fatal("nil HTTP client accepted")
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect followed")
	}))
	defer target.Close()
	redirecting := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirecting.Close()
	c, err := New(redirecting.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.fetchKeys(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirected key set: %v", err)
	}
}

func TestVerifyWithAudienceAcceptsAnOAuthClientsToken(t *testing.T) {
	key := newKey(t, "0A1B2C3D")
	app := newAuthApp(t, key)
	own, clk := newClient(t, app)
	resource := "https://shop-mcp.gregale.app/mcp"
	mcp, err := New(app.server.URL, WithAudience(resource))
	if err != nil {
		t.Fatal(err)
	}
	mcp.now = clk.now
	claims := userClaims(mcp, clk)
	claims["aud"], claims["client_id"], claims["scope"] = resource, "6a1b2c3d-0000-4000-8000-000000000002", "openid email"
	token := key.sign(t, header(key.kid), claims)
	verified, err := mcp.Verify(context.Background(), token)
	if err != nil || verified.ClientID != "6a1b2c3d-0000-4000-8000-000000000002" || strings.Join(verified.Scopes, " ") != "openid email" {
		t.Fatalf("Verify = %+v, %v", verified, err)
	}
	if _, err := own.Verify(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("a client's token passed the app's own audience: %v", err)
	}
	if _, err := mcp.Verify(context.Background(), key.sign(t, header(key.kid), userClaims(own, clk))); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("an app session passed the resource's audience: %v", err)
	}
	if _, err := New(app.server.URL, WithAudience("")); err == nil {
		t.Fatal("accepted an empty audience")
	}
}

func TestVerifyReadsTheActiveOrganization(t *testing.T) {
	key := newKey(t, "0A1B2C3D")
	app := newAuthApp(t, key)
	c, clk := newClient(t, app)
	payload := userClaims(c, clk)
	for name, value := range map[string]any{"org_id": "o1", "org_slug": "acme", "org_role": "admin", "org_permissions": []string{"org:read", "members:manage"}} {
		payload[name] = value
	}
	claims, err := c.Verify(context.Background(), key.sign(t, header(key.kid), payload))
	if err != nil {
		t.Fatal(err)
	}
	want := &Organization{ID: "o1", Slug: "acme", Role: "admin", Permissions: []string{"org:read", "members:manage"}}
	if !reflect.DeepEqual(claims.Organization, want) {
		t.Fatalf("organization = %+v", claims.Organization)
	}
	plain, err := c.Verify(context.Background(), key.sign(t, header(key.kid), userClaims(c, clk)))
	if err != nil || plain.Organization != nil {
		t.Fatalf("without org claims: %+v, %v", plain.Organization, err)
	}
}
