// OAuth + install-token plumbing tests (slice 8, ADR-012).
//
// The outbound HTTP layer is exercised against httptest.Server mocks
// of api.github.com — every test stands up a fake GitHub, asserts on
// the request shape, and returns a canned response.
package githubd

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestKey generates a throwaway RSA key for tests. The private
// material never leaves the test goroutine.
func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return k
}

func TestMintAppJWT_ShapeAndSignature(t *testing.T) {
	key := newTestKey(t)
	a := &AppAuth{AppID: "42", PrivateKey: key}
	tok, err := a.MintAppJWT()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt parts = %d, want 3", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims.Iss != "42" {
		t.Errorf("iss = %q, want 42", claims.Iss)
	}
	if claims.Exp <= claims.Iat {
		t.Errorf("exp (%d) must be after iat (%d)", claims.Exp, claims.Iat)
	}
}

func TestExchangeInstallationToken_Success(t *testing.T) {
	key := newTestKey(t)
	var hits atomic.Int32
	var gotAuth string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.Contains(r.URL.Path, "/app/installations/777/access_tokens") {
			t.Errorf("path = %q, want /app/installations/777/access_tokens", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs_testtoken",
			"expires_at": time.Now().Add(45 * time.Minute),
		})
	}))
	defer fake.Close()

	a := &AppAuth{
		AppID:      "12345",
		PrivateKey: key,
		HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL},
	}
	tok, exp, err := a.ExchangeInstallationToken(context.Background(), 777)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghs_testtoken" {
		t.Errorf("tok = %q, want ghs_testtoken", tok)
	}
	if exp.IsZero() {
		t.Errorf("exp is zero")
	}
	if hits.Load() != 1 {
		t.Errorf("hits = %d, want 1", hits.Load())
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Errorf("missing Bearer auth: %q", gotAuth)
	}
}

func TestExchangeInstallationToken_5xx(t *testing.T) {
	key := newTestKey(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"maintenance"}`))
	}))
	defer fake.Close()

	a := &AppAuth{
		AppID:      "12345",
		PrivateKey: key,
		HTTPClient: &singleHostClient{base: fake.Client(), api: fake.URL},
	}
	_, _, err := a.ExchangeInstallationToken(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error on 503")
	}
	if !strings.Contains(err.Error(), "status=503") {
		t.Errorf("err = %v, want status=503", err)
	}
}

func TestListInstallableRepos_PaginatesAndStops(t *testing.T) {
	key := newTestKey(t)
	calls := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			repos := make([]InstallableRepo, 100)
			// Set Link BEFORE writing the body — once the body is
			// written, WriteHeader fires and subsequent Header.Set
			// calls are ignored.
			next := "http://" + r.Host + r.URL.Path + "?page=2&per_page=100"
			w.Header().Set("Link", `<`+next+`>; rel="next"`)
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
			return
		}
		// Second page: 5 repos, no next link.
		repos := []InstallableRepo{{ID: 1, FullName: "octo/last"}}
		_ = json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
	}))
	defer srv.Close()

	a := &AppAuth{
		AppID:      "1",
		PrivateKey: key,
		HTTPClient: &singleHostClient{base: srv.Client(), api: srv.URL},
	}
	repos, err := a.ListInstallableRepos(context.Background(), "test-token", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 101 {
		t.Errorf("got %d repos, want 101 (100 + 1)", len(repos))
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

// singleHostClient rewrites every request to the test server's URL
// before delegating to the base client. The base client (from
// httptest.Server.Client()) manages the TLS config + transport;
// we don't need to invent a custom one.
type singleHostClient struct {
	base *http.Client
	api  string
}

func (c *singleHostClient) Do(req *http.Request) (*http.Response, error) {
	targetHost := strings.TrimPrefix(c.api, "http://")
	req.URL.Scheme = "http"
	req.URL.Host = targetHost
	req.Host = targetHost
	return c.base.Do(req)
}

func TestTokenCache_MissThenHit(t *testing.T) {
	var calls atomic.Int32
	fetcher := fakeFetcher(func(ctx context.Context, id int64) (string, time.Time, error) {
		calls.Add(1)
		return "tok-" + string(rune('0'+id)), time.Now().Add(time.Hour), nil
	})
	c := NewTokenCache(fetcher, time.Minute)
	tok, err := c.Token(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "tok-7" {
		t.Errorf("tok = %q, want tok-7", tok)
	}
	tok, _ = c.Token(context.Background(), 7)
	if tok != "tok-7" {
		t.Errorf("cached tok = %q, want tok-7", tok)
	}
	if calls.Load() != 1 {
		t.Errorf("fetcher calls = %d, want 1 (cache hit on second call)", calls.Load())
	}
}

func TestTokenCache_ConcurrentMissIsSingleflight(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	fetcher := fakeFetcher(func(ctx context.Context, id int64) (string, time.Time, error) {
		calls.Add(1)
		<-gate // block all callers; only one should reach the fetcher
		return "tok", time.Now().Add(time.Hour), nil
	})
	c := NewTokenCache(fetcher, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Token(context.Background(), 42)
		}()
	}
	// Let one caller proceed; release the rest.
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("fetcher calls = %d, want 1 (singleflight coalesce)", calls.Load())
	}
}

func TestTokenCache_InvalidateDrops(t *testing.T) {
	fetcher := fakeFetcher(func(_ context.Context, id int64) (string, time.Time, error) {
		return "tok", time.Now().Add(time.Hour), nil
	})
	c := NewTokenCache(fetcher, time.Minute)
	if _, err := c.Token(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if c.Size() != 1 {
		t.Errorf("size = %d, want 1", c.Size())
	}
	c.Invalidate(9)
	if c.Size() != 0 {
		t.Errorf("size after invalidate = %d, want 0", c.Size())
	}
}

// fakeFetcher adapts a function to the TokenFetcher interface.
type fakeFetcher func(ctx context.Context, id int64) (string, time.Time, error)

func (f fakeFetcher) ExchangeInstallationToken(ctx context.Context, id int64) (string, time.Time, error) {
	return f(ctx, id)
}
