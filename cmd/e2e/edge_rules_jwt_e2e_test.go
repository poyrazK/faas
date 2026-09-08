// edge_rules_jwt_e2e_test.go — D18 per-kind e2e for `kind=jwt`.
// Bitmask: APID | Gatewayd. See edge_rules_common_test.go for the
// kind=route-substitute precondition pattern.
//
// JWT e2e validator-bypass (D3): the production validator
// pkg/api/dto.go::EdgeRuleJWTAction.Validate (lines 3537-3566) has
// TWO guards: (1) JWKSURL must start with https://, (2) closed-list
// prefix check rejects https://127.* / 10.* / 192.168.* / etc.
// httptest.NewServer serves http://127.0.0.1:<port> (fails both
// guards). Even httptest.NewTLSServer serves https://127.0.0.1:<port>
// (fails the second guard). The wire-compatible workaround is to
// seed the rule directly via h.Pool.Exec with a `https://127.0.0.1:
// <port>` URL — the gateway compiles the rule from PG without
// re-running the validator, and pkg/edgejwks fetches the JWKS over
// plain HTTP (the URL string is opaque to the HTTP client).

package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// jwksHandler serves a JWKS document with the supplied public key.
func jwksHandler(pub *rsa.PublicKey) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		jwk := map[string]any{
			"kty": "RSA",
			"kid": "test-key",
			"use": "sig",
			"alg": "RS256",
			"n":   joseBase64URL(pub.N.Bytes()),
			"e":   "AQAB",
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{jwk}})
	})
}

// joseBase64URL encodes b as base64url without padding.
func joseBase64URL(b []byte) string {
	s := base64Encode(b)
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.TrimRight(s, "=")
	return s
}

// mintRS256 signs claims with the supplied RSA private key and
// returns the compact JWT.
func mintRS256(t *testing.T, priv *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"),
	)
	if err != nil {
		t.Fatalf("jose.NewSigner: %v", err)
	}
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatalf("jwt.Signed.Serialize: %v", err)
	}
	return token
}

func TestEdgeRulesJWT_E2E(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	// RSA keypair (RS256). RSA-2048 keygen is ~5 ms wall.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}

	// httptest.NewServer serves the JWKS over plain HTTP. The
	// gateway's pkg/edgejwks verifier fetches whatever URL the rule
	// has — the protocol is opaque to it.
	jwksSrv := httptest.NewServer(jwksHandler(&priv.PublicKey))
	defer jwksSrv.Close()
	jwksURL := "https://127.0.0.1:" + strings.TrimPrefix(jwksSrv.URL, "http://127.0.0.1:") + "/"

	slug := "jwt-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-jwt.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=jwt, JWKSURL pointing at the httptest
	// server, Issuer="test-issuer", Algorithms=[RS256]. apid-Validate
	// would reject https://127.0.0.1 — direct SQL seed bypasses it.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindJWT,
		map[string]any{
			"kind": "jwt",
			"jwt": map[string]any{
				"issuer":     "test-issuer",
				"jwks_url":   jwksURL,
				"algorithms": []string{"RS256"},
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Negative path: missing Authorization. kind=jwt short-circuits
	// with 401 + WWW-Authenticate: Bearer (handler.go:2307).
	header, _, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
	if status != http.StatusUnauthorized {
		t.Errorf("kind=jwt no-token: status=%d, want 401", status)
	}
	if got := header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Errorf("kind=jwt no-token: WWW-Authenticate=%q, want Bearer-prefixed", got)
	}

	// Happy path: valid RS256 token with matching issuer. kind=jwt
	// verifies, returns true (no short-circuit), fall through to
	// Backend.Pick → 404 (no routable target on the test app).
	tok := mintRS256(t, priv, map[string]any{
		"iss": "test-issuer",
		"sub": "user-1",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil,
		map[string]string{"Authorization": "Bearer " + tok})
	if status != http.StatusNotFound {
		t.Errorf("kind=jwt valid token: status=%d, want 404 (Backend.Pick miss after JWT pass)", status)
	}

	// Negative path: bad signature. Generate a different RSA key,
	// sign with it — gateway's JWKS lookup uses the kid to find the
	// public key; since the kid doesn't match, verification fails.
	wrongPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey wrong: %v", err)
	}
	badTok := mintRS256(t, wrongPriv, map[string]any{
		"iss": "test-issuer",
		"sub": "user-1",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil,
		map[string]string{"Authorization": "Bearer " + badTok})
	if status != http.StatusUnauthorized {
		t.Errorf("kind=jwt bad-sig: status=%d, want 401", status)
	}
}

// base64Encode is the stdlib base64-url-without-padding encoder.
// go-jose's own internals use a similar helper; we import stdlib to
// avoid a circular dep on go-jose internals.
func base64Encode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
