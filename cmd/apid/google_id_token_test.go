package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestGoogleIDTokenVerifier_ValidatesClaims(t *testing.T) {
	priv, pub := googleIDTokenRSAFixture(t, "google-test")
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{pub}}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(jwks.Close)

	v := newGoogleIDTokenVerifier(nil).(*googleJWKSVerifier)
	v.jwksURL = jwks.URL
	const clientID = "client-123"
	const nonce = "nonce-123"
	token := mintGoogleIDToken(t, priv, "google-test", jwt.Claims{
		Issuer:   "https://accounts.google.com",
		Subject:  "google-subject",
		Audience: jwt.Audience{clientID},
		Expiry:   jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, map[string]any{"nonce": nonce})

	claims, err := v.Verify(context.Background(), token, clientID, nonce)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "google-subject" || claims.Issuer != "https://accounts.google.com" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestGoogleIDTokenVerifier_RejectsUntrustedClaims(t *testing.T) {
	priv, pub := googleIDTokenRSAFixture(t, "google-test")
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{pub}}
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(jwks.Close)

	const clientID = "client-123"
	const nonce = "nonce-123"
	cases := []struct {
		name       string
		issuer     string
		audience   string
		tokenNonce string
	}{
		{name: "wrong issuer", issuer: "https://attacker.example", audience: clientID, tokenNonce: nonce},
		{name: "wrong audience", issuer: "https://accounts.google.com", audience: "other-client", tokenNonce: nonce},
		{name: "wrong nonce", issuer: "https://accounts.google.com", audience: clientID, tokenNonce: "other-nonce"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newGoogleIDTokenVerifier(nil).(*googleJWKSVerifier)
			v.jwksURL = jwks.URL
			token := mintGoogleIDToken(t, priv, "google-test", jwt.Claims{
				Issuer:   tc.issuer,
				Subject:  "google-subject",
				Audience: jwt.Audience{tc.audience},
				Expiry:   jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			}, map[string]any{"nonce": tc.tokenNonce})
			if _, err := v.Verify(context.Background(), token, clientID, nonce); err == nil {
				t.Fatal("Verify accepted an untrusted ID token")
			}
		})
	}
}

func googleIDTokenRSAFixture(t *testing.T, kid string) (*rsa.PrivateKey, jose.JSONWebKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	return priv, jose.JSONWebKey{
		Key:       &priv.PublicKey,
		KeyID:     kid,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}
}

func mintGoogleIDToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims jwt.Claims, custom map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), kid),
	)
	if err != nil {
		t.Fatalf("jose.NewSigner: %v", err)
	}
	b := jwt.Signed(signer).Claims(claims)
	if len(custom) > 0 {
		b = b.Claims(custom)
	}
	token, err := b.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return token
}
