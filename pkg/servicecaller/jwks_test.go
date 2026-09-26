package servicecaller

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJWKSRoundTripVerifiesAssertion(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kid := KeyID(pub)
	set, err := JWKSFromTrustedKeys(TrustedKeys{kid: pub})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := TrustedKeysFromJWKS(raw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	token, err := Mint(MintInput{CallerAppID: "caller-id", TargetAppID: "target-id", AccountID: "account-id"}, priv, kid, MaxTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := Verify(token, keys, "target-id", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if claim.CallerAppID != "caller-id" || claim.TargetAppID != "target-id" {
		t.Fatalf("verified claim = %+v", claim)
	}
}

func TestJWKSIsDeterministicAndRejectsInvalidKeys(t *testing.T) {
	pubA, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	left, err := JWKSFromTrustedKeys(TrustedKeys{KeyID(pubB): pubB, KeyID(pubA): pubA})
	if err != nil {
		t.Fatal(err)
	}
	right, err := JWKSFromTrustedKeys(TrustedKeys{KeyID(pubA): pubA, KeyID(pubB): pubB})
	if err != nil {
		t.Fatal(err)
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("JWK order is not deterministic:\n%s\n%s", leftJSON, rightJSON)
	}
	if _, err := JWKSFromTrustedKeys(TrustedKeys{"wrong-kid": pubA}); err == nil {
		t.Fatal("published a key under a non-matching kid")
	}

	valid := left.Keys[0]
	tests := []struct {
		name string
		edit func(*JWK)
	}{
		{"unsupported key type", func(j *JWK) { j.Kty = "RSA" }},
		{"unsupported algorithm", func(j *JWK) { j.Alg = "HS256" }},
		{"invalid key bytes", func(j *JWK) { j.X = "not-base64url" }},
		{"kid mismatch", func(j *JWK) { j.Kid = "wrong-kid" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			jwk := valid
			tc.edit(&jwk)
			raw, err := json.Marshal(JWKSet{Keys: []JWK{jwk}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := TrustedKeysFromJWKS(raw); err == nil {
				t.Fatal("accepted invalid key set")
			}
		})
	}
	duplicateJSON, _ := json.Marshal(JWKSet{Keys: []JWK{valid, valid}})
	if _, err := TrustedKeysFromJWKS(duplicateJSON); err == nil {
		t.Fatal("accepted duplicate key id")
	}
	if _, err := TrustedKeysFromJWKS([]byte(`{"keys":[]}` + strings.Repeat(" ", MaxJWKSBytes))); err == nil {
		t.Fatal("accepted oversized JWKS")
	}
	for _, raw := range [][]byte{[]byte(`null`), []byte(`{"keys":null}`)} {
		if _, err := TrustedKeysFromJWKS(raw); err == nil {
			t.Errorf("accepted JWKS without a keys array: %s", raw)
		}
	}
}

func TestParsePublicKeyPEMRequiresOneEd25519Block(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	parsed, err := ParsePublicKeyPEM(pemBytes)
	if err != nil || KeyID(parsed) != KeyID(pub) {
		t.Fatalf("ParsePublicKeyPEM() = %v, %v", parsed, err)
	}
	if _, err := ParsePublicKeyPEM(append(pemBytes, pemBytes...)); err == nil {
		t.Fatal("accepted multiple PEM blocks")
	}
	if _, err := ParsePublicKeyPEM(make([]byte, MaxPublicKeyPEMBytes+1)); err == nil {
		t.Fatal("accepted oversized PEM")
	}
}

func TestFetchTrustedKeysUsesSecureBoundedEndpoint(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	set, err := JWKSFromTrustedKeys(TrustedKeys{KeyID(pub): pub})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(set)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.Header.Get("Accept"), "application/jwk-set+json") {
			t.Errorf("request = %s %s Accept=%q", r.Method, r.URL, r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	keys, err := FetchTrustedKeys(context.Background(), server.Client(), server.URL)
	if err != nil || len(keys) != 1 {
		t.Fatalf("FetchTrustedKeys() = %v, %v", keys, err)
	}
	if _, err := FetchTrustedKeys(context.Background(), server.Client(), "http://example.com/keys"); err == nil {
		t.Fatal("allowed non-loopback HTTP key URL")
	}
	if _, err := FetchTrustedKeys(context.Background(), server.Client(), "https://user@example.com/keys"); err == nil {
		t.Fatal("allowed credentials in key URL")
	}
	if _, err := FetchTrustedKeys(context.Background(), server.Client(), "https://example.com/keys#fragment"); err == nil {
		t.Fatal("allowed fragment in key URL")
	}

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if _, err := FetchTrustedKeys(context.Background(), redirect.Client(), redirect.URL); err == nil {
		t.Fatal("followed key endpoint redirect")
	}

	badResponse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer badResponse.Close()
	if _, err := FetchTrustedKeys(context.Background(), badResponse.Client(), badResponse.URL); err == nil {
		t.Fatal("accepted non-200 key endpoint")
	}

	tooLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", MaxJWKSBytes+1)))
	}))
	defer tooLarge.Close()
	if _, err := FetchTrustedKeys(context.Background(), tooLarge.Client(), tooLarge.URL); err == nil {
		t.Fatal("accepted oversized key response")
	}
	if _, err := FetchTrustedKeys(context.Background(), nil, "file:///etc/passwd"); err == nil {
		t.Fatal("accepted a non-HTTP URL")
	}
	if _, err := TrustedKeysFromJWKS([]byte("invalid")); err == nil {
		t.Fatalf("JWKS parse error = %v", err)
	}
}
