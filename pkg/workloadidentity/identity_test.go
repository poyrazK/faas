package workloadidentity

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"
)

func TestMintAndVerify(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(key, DefaultIssuer, "kid-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	tok, err := signer.Mint(now, "acct-1", "app-1", "inst-1", "sts.amazonaws.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok.TokenType != "Bearer" || tok.ExpiresIn != 60 || tok.AccessToken == "" {
		t.Fatalf("token response = %+v", tok)
	}
	parsed, err := jwt.ParseSigned(tok.AccessToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var claims Claims
	if err := parsed.Claims(&key.PublicKey, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != DefaultIssuer || claims.Subject != "app:app-1" || claims.Audience[0] != "sts.amazonaws.com" || claims.AccountID != "acct-1" || claims.AppID != "app-1" || claims.InstanceID != "inst-1" {
		t.Fatalf("claims = %+v", claims)
	}
	if claims.IssuedAt.Time() != now || claims.Expiry.Time() != now.Add(time.Minute) || len(claims.ID) != 32 {
		t.Fatalf("timestamps/jti = issued=%v expiry=%v jti=%q", claims.IssuedAt.Time(), claims.Expiry.Time(), claims.ID)
	}
	if got := signer.JWKS().Keys[0].KeyID; got != "kid-1" {
		t.Fatalf("JWKS kid = %q", got)
	}
}

// A configured lifetime above five minutes is capped for app tokens too:
// a cloud trusting the issuer would accept a copied token for its whole
// stated life (2026-10-27 audit).
func TestMintCapsAConfiguredLifetimeAtFiveMinutes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(key, DefaultIssuer, "kid-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	tok, err := signer.Mint(now, uuid.NewString(), uuid.NewString(), uuid.NewString(), "sts.amazonaws.com")
	if err != nil {
		t.Fatal(err)
	}
	if tok.ExpiresIn != int64(DefaultTokenTTL/time.Second) {
		t.Fatalf("expires_in = %d, want %d", tok.ExpiresIn, int64(DefaultTokenTTL/time.Second))
	}
	parsed, err := jwt.ParseSigned(tok.AccessToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var claims jwt.Claims
	if err := parsed.Claims(&key.PublicKey, &claims); err != nil {
		t.Fatal(err)
	}
	if got := claims.Expiry.Time().Sub(claims.IssuedAt.Time()); got != DefaultTokenTTL {
		t.Fatalf("token lifetime = %v, want %v", got, DefaultTokenTTL)
	}
}

func TestMintExecutionUsesDistinctClaimsAndCapsLifetime(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(key, DefaultIssuer, "kid-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	accountID, executionID, leaseToken := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tok, err := signer.MintExecution(now, accountID, executionID, leaseToken, "gregale:outbound:integration-1")
	if err != nil {
		t.Fatal(err)
	}
	if tok.BearerValue() == "" {
		t.Fatal("execution token is empty")
	}
	parsed, err := jwt.ParseSigned(tok.BearerValue(), []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var claims ExecutionClaims
	if err := parsed.Claims(&key.PublicKey, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "execution:"+executionID || claims.AccountID != accountID || claims.ExecutionID != executionID || claims.LeaseToken != leaseToken ||
		claims.Expiry.Time() != now.Add(DefaultTokenTTL) || claims.Audience[0] != "gregale:outbound:integration-1" {
		t.Fatalf("execution claims = %+v", claims)
	}
	if _, err := signer.MintExecution(now, "not-an-account-uuid", executionID, leaseToken, "gregale:outbound:integration-1"); err == nil {
		t.Fatal("accepted a non-UUID account ID")
	}
}

func TestJWKSAdvertisesRFC7517SigningMetadata(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(key, DefaultIssuer, "kid-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	keys := signer.JWKS().Keys
	if len(keys) != 1 {
		t.Fatalf("JWKS key count = %d, want 1", len(keys))
	}
	if keys[0].KeyID != "kid-1" || keys[0].Algorithm != string(jose.RS256) || keys[0].Use != "sig" {
		t.Fatalf("JWK metadata = kid=%q alg=%q use=%q, want kid-1/RS256/sig", keys[0].KeyID, keys[0].Algorithm, keys[0].Use)
	}
}

func TestMintRejectsInvalidAudience(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, _ := NewSigner(key, DefaultIssuer, "", 0)
	for _, audience := range []string{"", "has space", strings.Repeat("x", maxAudienceLen+1)} {
		if _, err := signer.Mint(time.Now(), "acct", "app", "inst", audience); err == nil {
			t.Errorf("audience %q was accepted", audience)
		}
	}
}

func TestNewSignerRejectsWeakOrNonHTTPS(t *testing.T) {
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	if _, err := NewSigner(weak, DefaultIssuer, "", 0); err == nil {
		t.Error("accepted weak key")
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	if _, err := NewSigner(key, "http://identity.example", "", 0); err == nil {
		t.Error("accepted non-https issuer")
	}
}
