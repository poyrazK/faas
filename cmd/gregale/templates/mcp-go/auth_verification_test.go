package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthHonorsJWKVerificationOperations(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{
		"iss": "https://issuer.example", "aud": "https://mcp.example/mcp",
		"sub": "customer", "scope": "mcp:catalog", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	message := encode([]byte(`{"alg":"RS256","kid":"provider-key"}`)) + "." + encode(claims)
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	credential := message + "." + encode(signature)
	for _, tc := range []struct {
		name       string
		operations json.RawMessage
		accepted   bool
	}{
		{"absent", nil, true},
		{"verify", json.RawMessage(`["verify"]`), true},
		{"encryption only", json.RawMessage(`["encrypt"]`), false},
		{"sign only", json.RawMessage(`["sign"]`), false},
		{"empty", json.RawMessage(`[]`), false},
		{"null", json.RawMessage(`null`), false},
		{"string", json.RawMessage(`"verify"`), false},
		{"invalid item", json.RawMessage(`["verify",1]`), false},
		{"null item", json.RawMessage(`["verify",null]`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := map[string]any{
				"kty": "RSA", "kid": "provider-key",
				"n": base64.RawURLEncoding.EncodeToString(private.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(private.E)).Bytes()),
			}
			if tc.operations != nil {
				key["key_ops"] = tc.operations
			}
			encoded, err := json.Marshal(map[string]any{"keys": []any{key}})
			if err != nil {
				t.Fatal(err)
			}
			var set jsonWebKeySet
			if err := json.Unmarshal(encoded, &set); err != nil {
				t.Fatal(err)
			}
			auth := testScopedAuthenticator(t, scopedAuthConfig)
			auth.keyResolver.keys = set.Keys
			auth.keyResolver.loadedAt = time.Now()
			auth.keyResolver.lastRefresh = time.Now()
			identity, failure := auth.authenticate("Bearer " + credential)
			if tc.accepted {
				if failure != nil || identity == nil || identity.subject != "customer" {
					t.Fatalf("valid verification key rejected: identity=%+v failure=%+v", identity, failure)
				}
			} else if failure == nil || identity != nil || failure.status != 401 {
				t.Fatalf("disallowed verification key accepted: identity=%+v failure=%+v", identity, failure)
			}
		})
	}
}

func verificationKeyAndToken(t *testing.T) (jsonWebKey, string) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	key := jsonWebKey{Kty: "RSA", Kid: "known", N: encode(private.N.Bytes()), E: encode(big.NewInt(int64(private.E)).Bytes())}
	claims, err := json.Marshal(map[string]any{"iss": "https://issuer.example", "aud": "https://mcp.example/mcp", "sub": "customer", "scope": "mcp:catalog", "exp": time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	message := encode([]byte(`{"alg":"RS256","kid":"known"}`)) + "." + encode(claims)
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return key, message + "." + encode(signature)
}

func TestJWKRefreshKeepsFreshCachedTokensAvailable(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "rotation", true: "outage"}[fail], func(t *testing.T) {
			key, token := verificationKeyAndToken(t)
			started, release := make(chan struct{}), make(chan struct{})
			releaseRefresh := sync.OnceFunc(func() { close(release) })
			var requests atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					_ = json.NewEncoder(w).Encode(jsonWebKeySet{Keys: []jsonWebKey{key}})
					return
				}
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				if fail {
					w.WriteHeader(503)
					return
				}
				rotated := key
				rotated.Kid = "rotated"
				_ = json.NewEncoder(w).Encode(jsonWebKeySet{Keys: []jsonWebKey{key, rotated}})
			}))
			defer provider.Close()
			defer releaseRefresh()
			auth := testScopedAuthenticator(t, scopedAuthConfig)
			resolver := auth.keyResolver
			resolver.url, resolver.client = provider.URL, provider.Client()
			resolver.client.Timeout = 2 * time.Second
			if _, failure := auth.authenticate("Bearer " + token); failure != nil {
				t.Fatal(failure)
			}
			resolver.mu.Lock()
			resolver.lastRefresh = time.Now().Add(-31 * time.Second)
			resolver.mu.Unlock()
			refresh := make(chan error, 1)
			go func() { _, err := resolver.resolve("rotated", "RS256"); refresh <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("refresh did not start")
			}
			cached := make(chan *authFailure, 1)
			go func() { _, failure := auth.authenticate("Bearer " + token); cached <- failure }()
			select {
			case failure := <-cached:
				if failure != nil {
					t.Fatal(failure)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("fresh cached token waited for provider I/O")
			}
			releaseRefresh()
			select {
			case err := <-refresh:
				if (err != nil) != fail {
					t.Fatalf("refresh error %v, failure expected %v", err, fail)
				}
			case <-time.After(time.Second):
				t.Fatal("refresh did not finish")
			}
			if _, failure := auth.authenticate("Bearer " + token); failure != nil {
				t.Fatal(failure)
			}
			if requests.Load() != 2 {
				t.Fatalf("fetch count %d", requests.Load())
			}
		})
	}
}

func TestJWKRefreshCoalescesAndCoolsDownFailures(t *testing.T) {
	key, _ := verificationKeyAndToken(t)
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "deadline"}[fail], func(t *testing.T) {
			var requests atomic.Int32
			provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if fail {
					<-r.Context().Done()
					return
				}
				_ = json.NewEncoder(w).Encode(jsonWebKeySet{Keys: []jsonWebKey{key}})
			}))
			defer provider.Close()
			resolver := newJWKResolver(provider.URL)
			resolver.client = provider.Client()
			resolver.client.Timeout = 100 * time.Millisecond
			var workers sync.WaitGroup
			for range 20 {
				workers.Go(func() {
					_, err := resolver.resolve("known", "RS256")
					if (err != nil) != fail {
						t.Errorf("resolve error %v", err)
					}
				})
			}
			workers.Wait()
			if requests.Load() != 1 {
				t.Fatalf("concurrent fetch count %d", requests.Load())
			}
			if fail {
				resolver.mu.Lock()
				resolver.keys = []jsonWebKey{key}
				resolver.loadedAt = time.Now().Add(-6 * time.Minute)
				resolver.mu.Unlock()
				if _, err := resolver.resolve("known", "RS256"); err == nil {
					t.Fatal("used expired cached keys after a failure")
				}
				if requests.Load() != 1 {
					t.Fatal("failed refresh bypassed cooldown")
				}
			}
		})
	}
}

func TestJWKSelectionFiltersKeyTypeBeforeAmbiguity(t *testing.T) {
	rsaKey, _ := verificationKeyAndToken(t)
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := private.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	ecKey := jsonWebKey{Kty: "EC", Crv: "P-256", X: encode(encoded[1:33]), Y: encode(encoded[33:])}
	wrongCurve := ecKey
	wrongCurve.Crv = "P-384"
	keys := []jsonWebKey{rsaKey, ecKey, wrongCurve, {Kty: "oct"}}
	for _, algorithm := range []string{"RS256", "ES256"} {
		if _, ok := findJWK(keys, "", algorithm); !ok {
			t.Fatalf("mixed JWKS rejected %s", algorithm)
		}
	}
	for _, key := range []jsonWebKey{rsaKey, ecKey} {
		algorithm := "RS256"
		if key.Kty == "EC" {
			algorithm = "ES256"
		}
		if _, ok := findJWK(append(keys, key), "", algorithm); ok {
			t.Fatal("accepted two eligible verification keys")
		}
	}
}
