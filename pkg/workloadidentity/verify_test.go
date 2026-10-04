package workloadidentity

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestFlagsVerifierRejectsUntrustedClaims(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(key, DefaultIssuer, "flags", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(signer.JWKS(), DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	claims := Claims{Claims: jwt.Claims{Issuer: DefaultIssuer, Subject: "app:app", Audience: jwt.Audience{FlagsAudience}, IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Minute)), ID: "unique"}, AccountID: "account", AppID: "app", InstanceID: "instance"}
	for _, tt := range []struct {
		name string
		edit func(*Claims)
	}{
		{"valid", func(*Claims) {}},
		{"wrong audience", func(c *Claims) { c.Audience = jwt.Audience{"sts.amazonaws.com"} }},
		{"wrong issuer", func(c *Claims) { c.Issuer = "https://untrusted.example" }},
		{"wrong subject", func(c *Claims) { c.Subject = "app:other" }},
		{"missing account", func(c *Claims) { c.AccountID = "" }},
		{"missing instance", func(c *Claims) { c.InstanceID = "" }},
		{"missing expiry", func(c *Claims) { c.Expiry = nil }},
		{"missing issued at", func(c *Claims) { c.IssuedAt = nil }},
		{"missing token id", func(c *Claims) { c.ID = "" }},
		{"expired", func(c *Claims) {
			c.IssuedAt = jwt.NewNumericDate(now.Add(-time.Minute))
			c.Expiry = jwt.NewNumericDate(now.Add(-10 * time.Second))
		}},
		{"future issuance", func(c *Claims) { c.IssuedAt = jwt.NewNumericDate(now.Add(time.Minute)) }},
		{"unbounded lifetime", func(c *Claims) { c.Expiry = jwt.NewNumericDate(now.Add(6 * time.Minute)) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := claims
			tt.edit(&c)
			raw, err := jwt.Signed(signer.signer).Claims(c).Serialize()
			if err != nil {
				t.Fatal(err)
			}
			_, err = verifier.Verify(raw, FlagsAudience, now)
			if (err == nil) != (tt.name == "valid") {
				t.Fatalf("verification error = %v", err)
			}
		})
	}
	other, err := NewSigner(key, DefaultIssuer, "untrusted-key", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, err := other.Mint(now, "account", "app", "instance", FlagsAudience)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(token.AccessToken, FlagsAudience, now); err == nil {
		t.Fatal("accepted unknown signing key")
	}
}

func TestFlagsVerifierTrustRequiresPublicUniqueSigningKeys(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	public := jose.JSONWebKey{Key: &key.PublicKey, KeyID: "flags", Use: "sig", Algorithm: "RS256"}
	private := public
	private.Key = key
	wrongUse := public
	wrongUse.Use = "enc"
	missingModulus := public
	missingModulus.Key = &rsa.PublicKey{E: 65537}
	for _, keys := range [][]jose.JSONWebKey{nil, {public, public}, {private}, {wrongUse}, {missingModulus}} {
		if _, err := NewVerifier(jose.JSONWebKeySet{Keys: keys}, DefaultIssuer); err == nil {
			t.Fatal("accepted invalid public trust")
		}
	}
}
