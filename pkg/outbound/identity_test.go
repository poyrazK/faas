package outbound

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func testWorkloadSigner(t *testing.T, issuer string) (*workloadidentity.Signer, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, issuer, "test-key", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	return signer, jwks
}

func testWorkloadToken(t *testing.T, signer *workloadidentity.Signer, now time.Time, appID, integrationID string) string {
	t.Helper()
	token, err := signer.Mint(now, "account-1", appID, "instance-1", identityAudiencePrefix+integrationID)
	if err != nil {
		t.Fatal(err)
	}
	return token.AccessToken
}

func TestWorkloadIdentityVerifierChecksIssuerSignatureAudienceAndTime(t *testing.T) {
	signer, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	valid := testWorkloadToken(t, signer, now, "app-1", "integration-1")
	identity, err := verifier.Verify(valid, "integration-1")
	if err != nil || identity.AppID != "app-1" || identity.AccountID != "account-1" || identity.InstanceID != "instance-1" {
		t.Fatalf("verified identity = %+v, %v", identity, err)
	}
	otherSigner, _ := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	tests := map[string]string{
		"missing":        "",
		"malformed":      "not-a-jwt",
		"wrong key":      testWorkloadToken(t, otherSigner, now, "app-1", "integration-1"),
		"expired":        testWorkloadToken(t, signer, now.Add(-10*time.Minute), "app-1", "integration-1"),
		"future":         testWorkloadToken(t, signer, now.Add(10*time.Minute), "app-1", "integration-1"),
		"wrong audience": testWorkloadToken(t, signer, now, "app-1", "integration-2"),
	}
	wrongIssuerVerifier, err := NewWorkloadIdentityVerifier(jwks, "https://different.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongIssuerVerifier.Verify(valid, "integration-1"); !errors.Is(err, ErrInvalidWorkloadIdentity) {
		t.Fatalf("wrong issuer error = %v", err)
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := verifier.Verify(raw, "integration-1"); !errors.Is(err, ErrInvalidWorkloadIdentity) {
				t.Fatalf("Verify error = %v, want invalid identity", err)
			}
		})
	}
	if _, err := NewWorkloadIdentityVerifier(jwks, "http://identity.example"); err == nil {
		t.Fatal("accepted an insecure issuer")
	}
}

func TestWorkloadIdentityVerifierRejectsUntrustedJWKS(t *testing.T) {
	_, jwks := testWorkloadSigner(t, workloadidentity.DefaultIssuer)
	if _, err := NewWorkloadIdentityVerifier(nil, workloadidentity.DefaultIssuer); err == nil {
		t.Fatal("accepted empty JWKS")
	}
	var set map[string]any
	if err := json.Unmarshal(jwks, &set); err != nil {
		t.Fatal(err)
	}
	keys := set["keys"].([]any)
	set["keys"] = append(keys, keys[0])
	duplicate, _ := json.Marshal(set)
	if _, err := NewWorkloadIdentityVerifier(duplicate, workloadidentity.DefaultIssuer); err == nil {
		t.Fatal("accepted duplicate key ID")
	}
	set["keys"] = keys
	key := keys[0].(map[string]any)
	key["alg"] = "RS512"
	wrongAlgorithm, _ := json.Marshal(set)
	if _, err := NewWorkloadIdentityVerifier(wrongAlgorithm, workloadidentity.DefaultIssuer); err == nil {
		t.Fatal("accepted an unexpected JWK algorithm")
	}
}

func TestWorkloadIdentityVerifierRejectsInconsistentClaimsAndLongLivedTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	trustedSigner, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "test-key", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	jwks, _ := json.Marshal(trustedSigner.JWKS())
	verifier, err := NewWorkloadIdentityVerifier(jwks, workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	base := workloadidentity.Claims{
		Claims: jwt.Claims{
			Issuer: workloadidentity.DefaultIssuer, Subject: "app:app-1",
			Audience: jwt.Audience{"gregale:outbound:integration-1"},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-time.Second)),
			Expiry: jwt.NewNumericDate(now.Add(time.Minute)), ID: "unique-id",
		},
		AccountID: "account-1", AppID: "app-1", InstanceID: "instance-1",
	}
	cases := map[string]func(*workloadidentity.Claims){
		"subject mismatch": func(c *workloadidentity.Claims) { c.Subject = "app:other" },
		"missing account":  func(c *workloadidentity.Claims) { c.AccountID = "" },
		"missing instance": func(c *workloadidentity.Claims) { c.InstanceID = "" },
		"multiple audience": func(c *workloadidentity.Claims) {
			c.Audience = append(c.Audience, "other")
		},
		"missing expiry": func(c *workloadidentity.Claims) { c.Expiry = nil },
		"long lifetime":  func(c *workloadidentity.Claims) { c.Expiry = jwt.NewNumericDate(now.Add(10 * time.Minute)) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			claims := base
			mutate(&claims)
			raw, err := jwt.Signed(signer).Claims(claims).Serialize()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := verifier.Verify(raw, "integration-1"); !errors.Is(err, ErrInvalidWorkloadIdentity) {
				t.Fatalf("Verify error = %v, want invalid identity", err)
			}
		})
	}
}
