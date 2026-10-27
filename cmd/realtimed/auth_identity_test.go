package main

// adr: 1332

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/onebox-faas/faas/pkg/edgejwks"
	"github.com/onebox-faas/faas/pkg/realtime"
)

func TestJWTIdentityUsesVerifiedSessionAndRequiresExpiry(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyset := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "session-key", Algorithm: "ES256", Use: "sig"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(keyset)
	}))
	defer server.Close()
	cache := edgejwks.NewCache(edgejwks.Options{HTTPClient: server.Client()})
	authorizer := &realtimeJWTAuthorizer{cache: cache, verify: edgejwks.NewVerifier(cache, edgejwks.DefaultSkew)}
	policy := realtime.AuthPolicy{Issuer: "https://auth.example/auth/v1", JWKSURL: server.URL, Algorithms: []string{"ES256"}}
	mint := func(signingKey *ecdsa.PrivateKey, expiry *jwt.NumericDate) string {
		t.Helper()
		signer, signErr := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: signingKey}, (&jose.SignerOptions{}).WithHeader("kid", "session-key"))
		if signErr != nil {
			t.Fatal(signErr)
		}
		custom := map[string]any{"session_id": "d654628b-965b-4f84-965b-cc994ff0411b", "org_id": "org-7"}
		for i := range 80 {
			custom["aaa_"+strconv.Itoa(i)] = "filler"
		}
		token, signErr := jwt.Signed(signer).Claims(jwt.Claims{Subject: "user", Issuer: policy.Issuer, Expiry: expiry}).Claims(custom).Serialize()
		if signErr != nil {
			t.Fatal(signErr)
		}
		return token
	}
	expiry := jwt.NewNumericDate(time.Now().Add(time.Minute))
	identity, err := authorizer.AuthorizeIdentity(t.Context(), mint(key, expiry), policy)
	if err != nil || identity.Principal != "user" || identity.SessionID != "d654628b-965b-4f84-965b-cc994ff0411b" || !identity.ExpiresAt.Equal(expiry.Time()) {
		t.Fatalf("verified identity = %+v, err=%v", identity, err)
	}
	if identity.Claims != nil {
		t.Fatalf("claims without channel rules = %v", identity.Claims)
	}
	// adr: 20261019102657 — channel rules ask for the claims they read.
	withRules := policy
	withRules.ExtractClaims = []string{"org_id", "team_id"}
	identity, err = authorizer.AuthorizeIdentity(t.Context(), mint(key, expiry), withRules)
	if err != nil || identity.Claims["org_id"] != "org-7" || len(identity.Claims) != 1 || identity.SessionID == "" {
		t.Fatalf("identity with rule claims = %+v, err=%v", identity, err)
	}
	otherKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{
		"missing expiry":               mint(key, nil),
		"expired inside verifier skew": mint(key, jwt.NewNumericDate(time.Now().Add(-time.Second))),
		"untrusted signature":          mint(otherKey, expiry),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := authorizer.AuthorizeIdentity(t.Context(), token, policy); err == nil {
				t.Fatal("invalid identity admitted")
			}
		})
	}
	if principal, err := authorizer.Authorize(t.Context(), mint(key, nil), policy); err != nil || principal != "user" {
		t.Fatalf("legacy admission contract changed: principal=%q err=%v", principal, err)
	}
}
