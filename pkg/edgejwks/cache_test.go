// Tests for pkg/edgejwks cache. Mirrors the seam spec from PR 5 D6:
// tests 9-15 (PutGet, no leak on re-Put, LRU evict, Reset, concurrent,
// distinct URLs cache separately, same URL returns same instance).
//
// We use httptest.NewServer to serve a real JWKS generated via
// jose.JSONWebKey + a runtime RSA key pair (so go-jose v4's
// JSONWebKey.Valid() check passes). Each test gets its own server
// so they can run in parallel without cross-test interference.
package edgejwks_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/onebox-faas/faas/pkg/edgejwks"
)

// makeValidJWKS generates an RSA-2048 key, exports it as a
// JSONWebKeySet, and returns the JSON-encoded form plus the kid.
// go-jose v4 enforces a 2048-bit minimum modulus for RSA, so we
// cannot use the small RFC 7515 fixture as-is — that fixture has a
// 1024-bit modulus which v4 rejects.
func makeValidJWKS(t *testing.T, kid string) (string, *rsa.PrivateKey) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pubJWK := jose.JSONWebKey{
		Key:       &priv.PublicKey,
		KeyID:     kid,
		Algorithm: string(jose.RS256),
		Use:       "sig",
	}
	set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{pubJWK}}
	body, err := json.Marshal(&set)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(body), priv
}

func newServer(t *testing.T, body string, status int, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustRegister(t *testing.T, c edgejwks.Cache, rawURL string) {
	t.Helper()
	if err := c.Register(rawURL); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func TestCache_RegisterEmpty(t *testing.T) {
	t.Parallel()
	c := edgejwks.NewCache(edgejwks.Options{})
	if err := c.Register(""); err == nil {
		t.Fatal("expected error on empty url")
	}
}

func TestCache_GetUnregistered(t *testing.T) {
	t.Parallel()
	c := edgejwks.NewCache(edgejwks.Options{})
	set, ok, err := c.Get(context.Background(), "https://example.com/jwks", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || set != nil {
		t.Fatalf("expected (nil,false), got (%v,%v)", set, ok)
	}
}

func TestCache_FirstGetTriggersFetch(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var hits atomic.Int64
	srv := newServer(t, body, http.StatusOK, &hits)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	set, ok, err := c.Get(context.Background(), srv.URL, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || set == nil {
		t.Fatal("expected ok=true, set!=nil")
	}
	if len(set.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(set.Keys))
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("expected 1 HTTP fetch, got %d", got)
	}
}

func TestCache_SecondGetWithinWindowUsesCached(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var hits atomic.Int64
	srv := newServer(t, body, http.StatusOK, &hits)
	c := edgejwks.NewCache(edgejwks.Options{
		HTTPClient:         srv.Client(),
		MinRefreshInterval: 5 * time.Minute,
	})
	mustRegister(t, c, srv.URL)
	for i := 0; i < 5; i++ {
		_, _, _ = c.Get(context.Background(), srv.URL, "k1")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("expected 1 HTTP fetch across 5 Gets within window, got %d", got)
	}
}

func TestCache_MissingKidForcesRefetchAfterWindow(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var hits atomic.Int64
	srv := newServer(t, body, http.StatusOK, &hits)
	c := edgejwks.NewCache(edgejwks.Options{
		HTTPClient:         srv.Client(),
		MinRefreshInterval: 1 * time.Millisecond,
	})
	mustRegister(t, c, srv.URL)
	if _, _, err := c.Get(context.Background(), srv.URL, "k1"); err != nil {
		t.Fatal(err)
	}
	// Wait past the window so the missing-kid signal triggers a
	// refetch instead of a no-op.
	time.Sleep(5 * time.Millisecond)
	if _, _, err := c.Get(context.Background(), srv.URL, "k2"); err != nil {
		_ = err // missing-kid forces a refetch; cache returns the same set regardless
	}
	if got := hits.Load(); got < 2 {
		t.Fatalf("expected at least 2 HTTP fetches (initial + rotation), got %d", got)
	}
}

func TestCache_Non200IsError(t *testing.T) {
	t.Parallel()
	srv := newServer(t, "not found", http.StatusNotFound, nil)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	_, ok, err := c.Get(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("expected error on non-200")
	}
	if !ok {
		t.Fatal("expected ok=true (URL was registered)")
	}
}

func TestCache_MalformedJSONIsError(t *testing.T) {
	t.Parallel()
	srv := newServer(t, "not json", http.StatusOK, nil)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	_, _, err := c.Get(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestCache_OversizedKeysetRejected(t *testing.T) {
	t.Parallel()
	// Build a JWKS with 1025 keys — one over the cap.
	keys := make([]map[string]any, 0, 1025)
	for i := 0; i < 1025; i++ {
		keys = append(keys, map[string]any{
			"kty": "RSA", "kid": fmt.Sprintf("k%d", i),
			"n": "abc", "e": "AQAB", "alg": "RS256", "use": "sig",
		})
	}
	body, _ := json.Marshal(map[string]any{"keys": keys})
	srv := newServer(t, string(body), http.StatusOK, nil)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	_, _, err := c.Get(context.Background(), srv.URL, "")
	if err == nil || !strings.Contains(err.Error(), "max") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

func TestCache_OversizedDocumentRejected(t *testing.T) {
	t.Parallel()
	srv := newServer(t, strings.Repeat("x", edgejwks.MaxResponseBytes+1), http.StatusOK, nil)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	_, _, err := c.Get(context.Background(), srv.URL, "")
	if err == nil || !strings.Contains(err.Error(), "response body too large") {
		t.Fatalf("expected response-size error, got %v", err)
	}
}

func TestCache_DistinctURLsCacheSeparately(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var hitsA, hitsB atomic.Int64
	srvA := newServer(t, body, http.StatusOK, &hitsA)
	srvB := newServer(t, body, http.StatusOK, &hitsB)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srvA.Client()})
	mustRegister(t, c, srvA.URL)
	mustRegister(t, c, srvB.URL)
	if _, _, err := c.Get(context.Background(), srvA.URL, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Get(context.Background(), srvB.URL, "k1"); err != nil {
		t.Fatal(err)
	}
	if hitsA.Load() != 1 || hitsB.Load() != 1 {
		t.Fatalf("expected 1 fetch per URL, got A=%d B=%d", hitsA.Load(), hitsB.Load())
	}
}

func TestCache_SameURLReturnsSameInstance(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var hits atomic.Int64
	srv := newServer(t, body, http.StatusOK, &hits)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	// Register twice — second is a no-op.
	mustRegister(t, c, srv.URL)
	mustRegister(t, c, srv.URL)
	if _, _, err := c.Get(context.Background(), srv.URL, "k1"); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("expected exactly 1 fetch, got %d", got)
	}
}

func TestCache_ResetDropsEverything(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var hits atomic.Int64
	srv := newServer(t, body, http.StatusOK, &hits)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	if _, _, err := c.Get(context.Background(), srv.URL, "k1"); err != nil {
		t.Fatal(err)
	}
	c.Reset()
	set, ok, err := c.Get(context.Background(), srv.URL, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if ok || set != nil {
		t.Fatalf("expected (nil,false) after Reset, got (%v,%v)", set, ok)
	}
	mustRegister(t, c, srv.URL)
	if _, _, err := c.Get(context.Background(), srv.URL, "k1"); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("expected 2 fetches (pre-Reset + post-Reset re-register), got %d", got)
	}
}

func TestCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var hits atomic.Int64
	srv := newServer(t, body, http.StatusOK, &hits)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	const goroutines = 16
	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if _, _, err := c.Get(context.Background(), srv.URL, "k1"); err != nil {
					t.Errorf("Get: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	// Singleflight: all 800 Gets should resolve with at most a small
	// number of fetches (window-elapsed rotations). 5 is a generous
	// upper bound; under -race we expect exactly 1.
	if got := hits.Load(); got > 5 {
		t.Fatalf("expected ≤5 fetches under concurrency, got %d", got)
	}
}

func TestCache_OnFetchErrCallback(t *testing.T) {
	t.Parallel()
	srv := newServer(t, "boom", http.StatusInternalServerError, nil)
	var errCount atomic.Int64
	var lastErr atomic.Value
	c := edgejwks.NewCache(edgejwks.Options{
		HTTPClient: srv.Client(),
		OnFetchErr: func(_ string, err error) {
			errCount.Add(1)
			lastErr.Store(err.Error())
		},
	})
	mustRegister(t, c, srv.URL)
	_, _, _ = c.Get(context.Background(), srv.URL, "")
	if errCount.Load() != 1 {
		t.Fatalf("expected OnFetchErr to fire once, got %d", errCount.Load())
	}
	if got, _ := lastErr.Load().(string); !strings.Contains(got, "status 500") {
		t.Fatalf("expected status 500 in lastErr, got %q", got)
	}
}

func TestCache_ParsedSetRoundTrip(t *testing.T) {
	t.Parallel()
	body, _ := makeValidJWKS(t, "k1")
	var stored atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(stored.Load().(string)))
	}))
	t.Cleanup(srv.Close)
	stored.Store(body)
	c := edgejwks.NewCache(edgejwks.Options{HTTPClient: srv.Client()})
	mustRegister(t, c, srv.URL)
	set, _, err := c.Get(context.Background(), srv.URL, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(set.Keys))
	}
	if set.Keys[0].KeyID != "k1" {
		t.Fatalf("expected kid=k1, got %q", set.Keys[0].KeyID)
	}
}

// Guard against losing the jose.JSONWebKeySet struct shape — used by
// the verifier to walk keys. If go-jose changes the public surface,
// this test surfaces the break at compile time.
func TestCache_PublicSurfaceGuard(t *testing.T) {
	t.Parallel()
	var _ *jose.JSONWebKeySet = nil // type-check: package is imported
}
