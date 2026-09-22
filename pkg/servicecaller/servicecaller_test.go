// adr: 206
package servicecaller

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

const (
	callerApp = "11111111-1111-4111-8111-111111111111"
	targetApp = "22222222-2222-4222-8222-222222222222"
	otherApp  = "33333333-3333-4333-8333-333333333333"
	account   = "44444444-4444-4444-8444-444444444444"
)

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func mintFor(t *testing.T, priv ed25519.PrivateKey, kid string, in MintInput, now time.Time) string {
	t.Helper()
	token, err := Mint(in, priv, kid, MaxTTL, now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	return token
}

func defaultInput() MintInput {
	return MintInput{
		CallerAppID: callerApp, TargetAppID: targetApp,
		AccountID: account, CallerInstanceID: "inst-1",
	}
}

func TestMintVerifyRoundTrip(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	in := defaultInput()
	in.CallerEnv = EnvPreview
	token := mintFor(t, priv, "node-key-1", in, now)

	got, err := Verify(token, TrustedKeys{"node-key-1": pub}, targetApp, now.Add(time.Second))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.CallerAppID != callerApp {
		t.Errorf("CallerAppID = %q, want %q", got.CallerAppID, callerApp)
	}
	if got.TargetAppID != targetApp {
		t.Errorf("TargetAppID = %q, want %q", got.TargetAppID, targetApp)
	}
	if got.AccountID != account {
		t.Errorf("AccountID = %q, want %q", got.AccountID, account)
	}
	if got.CallerInstanceID != "inst-1" {
		t.Errorf("CallerInstanceID = %q, want inst-1", got.CallerInstanceID)
	}
	if got.CallerEnv != EnvPreview {
		t.Errorf("CallerEnv = %q, want %q", got.CallerEnv, EnvPreview)
	}
	if got.ID == "" {
		t.Error("ID (jti) is empty; a verifier cannot de-duplicate replays")
	}
}

// The audience binding is the replay guard. Without it, a service that
// legitimately receives an assertion could present it to a sibling service in
// the same account and be accepted as the original caller.
func TestVerifyRejectsReplayAtAnotherService(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	token := mintFor(t, priv, "k", defaultInput(), now)

	_, err := Verify(token, TrustedKeys{"k": pub}, otherApp, now.Add(time.Second))
	if !errors.Is(err, ErrWrongAudience) {
		t.Fatalf("Verify against a different target = %v, want ErrWrongAudience", err)
	}
}

func TestVerifyRejections(t *testing.T) {
	pub, priv := keypair(t)
	otherPub, _ := keypair(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	valid := mintFor(t, priv, "k", defaultInput(), now)

	tests := []struct {
		name     string
		token    string
		keys     TrustedKeys
		audience string
		at       time.Time
		want     error
	}{
		{"empty token", "", TrustedKeys{"k": pub}, targetApp, now, ErrMalformed},
		{"not a jws", "not.a.token", TrustedKeys{"k": pub}, targetApp, now, ErrMalformed},
		// An unknown kid must not reach the signature step: there is no
		// trusted key to check against, and the key set is the trust boundary.
		{"unknown kid", valid, TrustedKeys{"other": pub}, targetApp, now, ErrUnknownKey},
		{"no trusted keys", valid, TrustedKeys{}, targetApp, now, ErrUnknownKey},
		// Right kid, wrong key material: this is the forgery case.
		{"signature does not verify", valid, TrustedKeys{"k": otherPub}, targetApp, now, ErrBadSignature},
		{"expired", valid, TrustedKeys{"k": pub}, targetApp, now.Add(MaxTTL + time.Second), ErrExpired},
		{"not yet valid", valid, TrustedKeys{"k": pub}, targetApp, now.Add(-time.Minute), ErrExpired},
		{"wrong audience", valid, TrustedKeys{"k": pub}, otherApp, now, ErrWrongAudience},
		// A verifier wired without its own app id must fail closed rather
		// than accept whatever audience arrives.
		{"no expected audience", valid, TrustedKeys{"k": pub}, "", now, ErrWrongAudience},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Verify(tc.token, tc.keys, tc.audience, tc.at); !errors.Is(err, tc.want) {
				t.Errorf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
}

// A token from another family (pkg/internalsvc mints "gregale") must not pass
// here even if it is correctly signed by a trusted node key.
func TestVerifyRejectsForeignIssuer(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Unix(1_700_000_000, 0).UTC()
	token := mintWithIssuer(t, priv, "k", "gregale", now)

	_, err := Verify(token, TrustedKeys{"k": pub}, targetApp, now.Add(time.Second))
	if !errors.Is(err, ErrWrongIssuer) {
		t.Fatalf("Verify = %v, want ErrWrongIssuer", err)
	}
}

// alg=none is the classic JWT bypass; an explicit EdDSA allowlist must reject
// it at parse rather than treating an unsigned token as valid.
func TestVerifyRejectsUnsignedToken(t *testing.T) {
	pub, _ := keypair(t)
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"gregale.svc","sub":"` + callerApp + `","aud":["` + targetApp + `"]}`))

	_, err := Verify(header+"."+payload+".", TrustedKeys{"k": pub}, targetApp, time.Now())
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Verify(alg=none) = %v, want ErrMalformed", err)
	}
}

func TestMintValidation(t *testing.T) {
	_, priv := keypair(t)
	now := time.Now()

	tests := []struct {
		name string
		in   MintInput
		priv ed25519.PrivateKey
		kid  string
	}{
		{"no caller", MintInput{TargetAppID: targetApp}, priv, "k"},
		{"no target", MintInput{CallerAppID: callerApp}, priv, "k"},
		{"no kid", defaultInput(), priv, ""},
		{"bad key size", defaultInput(), ed25519.PrivateKey("short"), "k"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Mint(tc.in, tc.priv, tc.kid, MaxTTL, now); err == nil {
				t.Error("Mint accepted invalid input")
			}
		})
	}
}

// A non-positive or oversized TTL must clamp to MaxTTL. Minting an
// already-expired token would fail every call in the fleet, and honouring an
// oversized one would widen the replay window past the ADR-206 bound.
func TestMintClampsTTL(t *testing.T) {
	pub, priv := keypair(t)
	now := time.Unix(1_700_000_000, 0).UTC()

	for _, ttl := range []time.Duration{0, -time.Hour, time.Hour} {
		token, err := Mint(defaultInput(), priv, "k", ttl, now)
		if err != nil {
			t.Fatalf("Mint(ttl=%v): %v", ttl, err)
		}
		if _, err := Verify(token, TrustedKeys{"k": pub}, targetApp, now.Add(time.Second)); err != nil {
			t.Errorf("ttl=%v produced an immediately invalid token: %v", ttl, err)
		}
		if _, err := Verify(token, TrustedKeys{"k": pub}, targetApp, now.Add(MaxTTL+time.Second)); !errors.Is(err, ErrExpired) {
			t.Errorf("ttl=%v outlived MaxTTL: %v", ttl, err)
		}
	}
}

// Every assertion must carry a distinct jti so a verifier that chooses to
// track them can actually detect a replay.
func TestMintIssuesDistinctIDs(t *testing.T) {
	_, priv := keypair(t)
	now := time.Now()
	seen := map[string]struct{}{}
	for i := 0; i < 16; i++ {
		token := mintFor(t, priv, "k", defaultInput(), now)
		var claims Claims
		decodeClaims(t, token, &claims)
		if _, dup := seen[claims.ID]; dup {
			t.Fatalf("duplicate jti %q", claims.ID)
		}
		seen[claims.ID] = struct{}{}
	}
}

func decodeClaims(t *testing.T, token string, out *Claims) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
}

// mintWithIssuer signs a payload with an arbitrary issuer so the foreign-token
// rejection can be exercised against a genuinely valid signature. Production
// Mint always writes Issuer, so this lives in the test file only.
func mintWithIssuer(t *testing.T, priv ed25519.PrivateKey, kid, issuer string, now time.Time) string {
	t.Helper()
	claims := Claims{
		Issuer: issuer, Subject: callerApp, Audience: []string{targetApp},
		ExpiresAt: now.Add(MaxTTL).Unix(), IssuedAt: now.Unix(), NotBefore: now.Unix(),
		ID: "test-jti", AccountID: account,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.EdDSA, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), kid),
	)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return compact
}
