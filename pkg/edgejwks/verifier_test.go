// Tests for pkg/edgejwks verifier. Mirrors the seam spec from PR 5 D6:
// tests 16-27 (valid/expired/wrong-iss/wrong-aud/wrong-alg/required-
// claims/bad-sig/clock-skew/rotation).
//
// We mint tokens with jose.Signer (key from a runtime RSA-2048 pair)
// and serve the matching public JWK over httptest. The verifier is
// wired with the production Cache + skew defaults; cmd-side wiring is
// tested separately in cmd/gatewayd-internal/edge_rules_jwks_test.go.
package edgejwks_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/onebox-faas/faas/pkg/edgejwks"
)

// jwksServer starts an httptest server that serves a JWK Set
// containing the single supplied pubJWK. Returns the server URL.
func jwksServer(t *testing.T, pubJWK jose.JSONWebKey, hits *atomic.Int64) (string, *jose.JSONWebKeySet) {
	t.Helper()
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{pubJWK}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&set)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &set
}

// mintToken signs claims with the supplied RSA key using RS256 and
// kid=k1. Returns the compact-serialized JWT string. go-jose v4's
// jwt.Builder.Claims merges multiple calls into one JSON object, so
// we pass std + custom separately.
func mintToken(t *testing.T, priv *rsa.PrivateKey, kid string, std jwt.Claims, custom map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: priv},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), kid),
	)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	b := jwt.Signed(signer).Claims(std)
	if len(custom) > 0 {
		b = b.Claims(custom)
	}
	out, err := b.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	return out
}

// rs256Fixture returns a fresh RSA-2048 key + matching pubJWK
// (kid=k1, RS256).
func rs256Fixture(t *testing.T, kid string) (*rsa.PrivateKey, jose.JSONWebKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pub := jose.JSONWebKey{
		Key:       &priv.PublicKey,
		KeyID:     kid,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}
	return priv, pub
}

// newVerifier wires a Cache + Verifier over a single registered
// JWKS URL pointing at the test server.
func newVerifier(t *testing.T, jwksURL, kid string, pub jose.JSONWebKey) edgejwks.Verifier {
	t.Helper()
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: http.DefaultClient})
	if err := c.Register(jwksURL); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Prime the cache so the first Verify doesn't pay the fetch cost.
	if _, _, err := c.Get(context.Background(), jwksURL, kid); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	return edgejwks.NewVerifier(c, edgejwks.DefaultSkew)
}

func TestVerify_ValidToken(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer:  "https://idp.example.com/",
		Subject: "alice",
		Expiry:  jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	claims, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "alice" {
		t.Fatalf("expected sub=alice, got %q", claims.Subject)
	}
	if claims.Issuer != "https://idp.example.com/" {
		t.Fatalf("expected iss=..., got %q", claims.Issuer)
	}
}

func TestVerify_RejectsMissingIssuer(t *testing.T) {
	t.Parallel()
	_, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	_, err := v.Verify(context.Background(), "not-a-token", edgejwks.VerifierRule{
		JWKSURL:    url,
		Algorithms: []string{"RS256"},
	})
	if !errors.Is(err, edgejwks.ErrJWTWrongIssuer) {
		t.Fatalf("error = %v, want ErrJWTWrongIssuer", err)
	}
}

func TestVerify_MissingToken(t *testing.T) {
	t.Parallel()
	_, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	_, err := v.Verify(context.Background(), "", edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	})
	if err == nil {
		t.Fatal("expected ErrJWTMissingToken")
	}
}

func TestVerify_ExpiredToken(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer: "https://idp.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	})
	if err == nil {
		t.Fatal("expected ErrJWTExpired")
	}
}

func TestVerify_WrongIssuer(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer: "https://evil.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	})
	if err == nil {
		t.Fatal("expected ErrJWTWrongIssuer")
	}
}

func TestVerify_WrongAudience(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer:   "https://idp.example.com/",
		Audience: jwt.Audience{"https://other.example.com"},
		Expiry:   jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Audience:   []string{"https://api.example.com"},
		Algorithms: []string{"RS256"},
	})
	if err == nil {
		t.Fatal("expected ErrJWTWrongAudience")
	}
}

func TestVerify_MissingAudienceSkipped(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer:   "https://idp.example.com/",
		Audience: jwt.Audience{"https://other.example.com"},
		Expiry:   jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL: url,
		Issuer:  "https://idp.example.com/",
		// Audience empty → aud check skipped
		Algorithms: []string{"RS256"},
	})
	if err != nil {
		t.Fatalf("expected pass (aud check skipped), got %v", err)
	}
}

func TestVerify_WrongAlgorithm(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	// Token is signed RS256; rule's vocabulary excludes RS256.
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer: "https://idp.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"ES256"}, // RS256 not in list
	})
	if err == nil {
		t.Fatal("expected ErrJWTWrongAlgorithm")
	}
}

func TestVerify_RejectsUnsupportedRuleAlgorithm(t *testing.T) {
	t.Parallel()
	_, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	_, err := v.Verify(context.Background(), "not-a-token", edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"HS256"},
	})
	if !errors.Is(err, edgejwks.ErrJWTWrongAlgorithm) {
		t.Fatalf("error = %v, want ErrJWTWrongAlgorithm", err)
	}
}

func TestVerify_RejectsJWKMetadataMismatch(t *testing.T) {
	t.Parallel()
	priv, base := rs256Fixture(t, "k1")
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer:  "https://idp.example.com/",
		Subject: "alice",
		Expiry:  jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	cases := map[string]func(*jose.JSONWebKey){
		"algorithm mismatch": func(k *jose.JSONWebKey) { k.Algorithm = string(jose.RS384) },
		"algorithm missing":  func(k *jose.JSONWebKey) { k.Algorithm = "" },
		"encryption use":     func(k *jose.JSONWebKey) { k.Use = "enc" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pub := base
			mutate(&pub)
			url, _ := jwksServer(t, pub, nil)
			v := newVerifier(t, url, "k1", pub)
			_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
				JWKSURL:    url,
				Issuer:     "https://idp.example.com/",
				Algorithms: []string{"RS256"},
			})
			if !errors.Is(err, edgejwks.ErrJWTKeyMetadata) {
				t.Fatalf("error = %v, want ErrJWTKeyMetadata", err)
			}
		})
	}
}

func TestVerify_RequiredClaimsPresent(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer:  "https://idp.example.com/",
		Expiry:  jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		Subject: "alice",
	}, map[string]any{"role": "admin"})
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:        url,
		Issuer:         "https://idp.example.com/",
		Algorithms:     []string{"RS256"},
		RequiredClaims: map[string]string{"role": "admin"},
	})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_RequiredClaimsMissing(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer: "https://idp.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:        url,
		Issuer:         "https://idp.example.com/",
		Algorithms:     []string{"RS256"},
		RequiredClaims: map[string]string{"role": "admin"},
	})
	if err == nil {
		t.Fatal("expected ErrJWTMissingClaim")
	}
}

func TestVerify_RequiredClaimsWrong(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer: "https://idp.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, map[string]any{"role": "viewer"})
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:        url,
		Issuer:         "https://idp.example.com/",
		Algorithms:     []string{"RS256"},
		RequiredClaims: map[string]string{"role": "admin"},
	})
	if err == nil {
		t.Fatal("expected ErrJWTMissingClaim")
	}
}

func TestVerify_ClockSkewAccepted(t *testing.T) {
	t.Parallel()
	priv, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	// Token expired 30s ago; skew default is 60s → should pass.
	tok := mintToken(t, priv, "k1", jwt.Claims{
		Issuer: "https://idp.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(-30 * time.Second)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	})
	if err != nil {
		t.Fatalf("expected pass within skew window, got %v", err)
	}
}

func TestVerify_BadSignature(t *testing.T) {
	t.Parallel()
	_, pub := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub, nil)
	v := newVerifier(t, url, "k1", pub)
	// Mint with a DIFFERENT key, verify against the JWKS URL serving
	// k1's public key. Should fail with bad signature.
	otherPriv, _ := rs256Fixture(t, "other")
	tok := mintToken(t, otherPriv, "k1", jwt.Claims{
		Issuer: "https://idp.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	_, err := v.Verify(context.Background(), tok, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	})
	if err == nil {
		t.Fatal("expected ErrJWTBadSignature")
	}
}

func TestVerify_JWKSRotation(t *testing.T) {
	t.Parallel()
	priv1, pub1 := rs256Fixture(t, "k1")
	url, _ := jwksServer(t, pub1, nil)
	v := newVerifier(t, url, "k1", pub1)
	// First token signed with k1 → verifies.
	tok1 := mintToken(t, priv1, "k1", jwt.Claims{
		Issuer: "https://idp.example.com/",
		Expiry: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
	}, nil)
	if _, err := v.Verify(context.Background(), tok1, edgejwks.VerifierRule{
		JWKSURL:    url,
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	}); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	// Rotation simulation is structural here: jwksServer serves a
	// single fixed set, so kid=k1 keeps verifying. The full rotation
	// path (cache eviction + new fetch + new verify) is exercised by
	// TestCache_MissingKidForcesRefetchAfterWindow + the cache
	// round-trip in cmd/gatewayd-internal/edge_rules_test.go.
}

func TestVerify_JWKSNotRegistered(t *testing.T) {
	t.Parallel()
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: http.DefaultClient})
	v := edgejwks.NewVerifier(c, edgejwks.DefaultSkew)
	_, err := v.Verify(context.Background(), "fake.token", edgejwks.VerifierRule{
		JWKSURL:    "https://example.com/jwks",
		Issuer:     "https://idp.example.com/",
		Algorithms: []string{"RS256"},
	})
	if err == nil {
		t.Fatal("expected ErrJWKSNotRegistered")
	}
}
