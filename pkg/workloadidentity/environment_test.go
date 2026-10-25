package workloadidentity

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4/jwt"
)

// adr: 1141 — amendment 2026-10-25: a token names its deployment scope, and
// any scope but the default changes the subject, so a staging deployment
// never presents the subject a production trust policy accepts.
func TestEnvironmentSubject(t *testing.T) {
	for _, tc := range []struct{ environment, want string }{
		{"", "app:app_1"},
		{DefaultEnvironment, "app:app_1"},
		{"staging", "app:app_1:env:staging"},
		{"pr-12", "app:app_1:env:pr-12"},
	} {
		if got := Subject("app_1", tc.environment); got != tc.want {
			t.Errorf("Subject(%q) = %q, want %q", tc.environment, got, tc.want)
		}
	}
}

func TestMintEnvironmentAndVerify(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(key, DefaultIssuer, "env", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(signer.JWKS(), DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, environment := range []string{"", DefaultEnvironment, "staging"} {
		tok, err := signer.MintEnvironment(now, "acct", "app_1", "inst", environment, FlagsAudience)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := verifier.Verify(tok.AccessToken, FlagsAudience, now)
		if err != nil {
			t.Fatalf("%q: verify: %v", environment, err)
		}
		if claims.Subject != Subject("app_1", environment) || claims.Environment != environment {
			t.Fatalf("%q: subject %q environment %q", environment, claims.Subject, claims.Environment)
		}
	}
	for _, bad := range []string{"Staging", "a", "staging:prod", "-x-", "app:other"} {
		if _, err := signer.MintEnvironment(now, "acct", "app_1", "inst", bad, FlagsAudience); err == nil {
			t.Errorf("environment %q minted", bad)
		}
	}

	// A token whose subject and environment disagree is refused: a staging
	// claim cannot ride on the default subject, nor the reverse.
	for _, tc := range []struct{ subject, environment string }{
		{"app:app_1", "staging"},
		{"app:app_1:env:staging", ""},
		{"app:app_1:env:staging", "production"},
	} {
		c := Claims{Claims: jwt.Claims{Issuer: DefaultIssuer, Subject: tc.subject, Audience: jwt.Audience{FlagsAudience}, IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Minute)), ID: "jti"}, AccountID: "acct", AppID: "app_1", InstanceID: "inst", Environment: tc.environment}
		raw, err := jwt.Signed(signer.signer).Claims(c).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifier.Verify(raw, FlagsAudience, now); err == nil {
			t.Errorf("subject %q with environment %q verified", tc.subject, tc.environment)
		}
	}
}
