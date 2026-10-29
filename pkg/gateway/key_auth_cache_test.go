package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type countingKeyAuth struct {
	calls atomic.Int32
	err   error
	key   RequireAuthnKey
	hook  func()
}

func (c *countingKeyAuth) AuthenticateKey(context.Context, []byte) (RequireAuthnAccount, RequireAuthnKey, error) {
	c.calls.Add(1)
	if c.hook != nil {
		c.hook()
	}
	if c.err != nil {
		return RequireAuthnAccount{}, RequireAuthnKey{}, c.err
	}
	return RequireAuthnAccount{ID: "acct"}, c.key, nil
}

func TestKeyAuthCacheKeepsVerifiedAndUnknownKeys(t *testing.T) {
	ctx := context.Background()
	store := &countingKeyAuth{key: RequireAuthnKey{ID: "k", Scopes: []string{"admin"}}}
	c := NewKeyAuthCache(store)
	for range 3 {
		if _, key, err := c.AuthenticateKey(ctx, api.HashAPIKey("good")); err != nil || key.ID != "k" {
			t.Fatalf("verified key: %v %v", key, err)
		}
	}
	if n := store.calls.Load(); n != 1 {
		t.Fatalf("store lookups for one verified key = %d, want 1", n)
	}

	store.err = ErrAPIKeyUnknown
	for range 3 {
		if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("forged")); !errors.Is(err, ErrAPIKeyUnknown) {
			t.Fatalf("unknown key: %v", err)
		}
	}
	if n := store.calls.Load(); n != 2 {
		t.Fatalf("store lookups after three misses of one forged key = %d, want 2", n)
	}

	c.Invalidate()
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("good")); !errors.Is(err, ErrAPIKeyUnknown) {
		t.Fatalf("revoked key after invalidation: %v, want the store's answer", err)
	}
}

func TestKeyAuthCacheOutageGraceAndTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	store := &countingKeyAuth{key: RequireAuthnKey{ID: "k"}}
	c := NewKeyAuthCache(store)
	c.now = func() time.Time { return now }
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("good")); err != nil {
		t.Fatal(err)
	}
	store.err = errors.New("control plane unavailable")
	now = now.Add(api.GatewayKeyAuthCacheTTL + time.Second)
	if _, key, err := c.AuthenticateKey(ctx, api.HashAPIKey("good")); err != nil || key.ID != "k" {
		t.Fatalf("verified key during an outage: %v %v, want it served within the grace", key, err)
	}
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("never-seen")); err == nil {
		t.Fatal("an unseen key was accepted during an outage")
	}
	now = now.Add(api.GatewayKeyAuthOutageGrace)
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("good")); err == nil {
		t.Fatal("verified key served past the outage grace")
	}
}

func TestKeyAuthCacheDropsAnswerRacingAnInvalidation(t *testing.T) {
	ctx := context.Background()
	store := &countingKeyAuth{key: RequireAuthnKey{ID: "k"}}
	c := NewKeyAuthCache(store)
	store.hook = func() { c.Invalidate() }
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("good")); err != nil {
		t.Fatal(err)
	}
	store.hook = nil
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("good")); err != nil {
		t.Fatal(err)
	}
	if n := store.calls.Load(); n != 2 {
		t.Fatalf("lookups = %d; an answer read across an invalidation must not be kept", n)
	}
}

func TestAuthenticateBearerThrottlesFailingSource(t *testing.T) {
	store := &countingKeyAuth{err: ErrAPIKeyUnknown}
	h := &Handler{requireAuthnAuthn: store, keyLookups: newKeyLookupLimiter()}
	req := httptest.NewRequest("GET", "http://app.example.test/", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	for i := range api.GatewayFailedKeyLookupsPerSource {
		if _, _, err := h.authenticateBearer(req, "gk_forged_"+string(rune('a'+i%26))); !errors.Is(err, ErrAPIKeyUnknown) {
			t.Fatalf("lookup %d: %v", i, err)
		}
	}
	if _, _, err := h.authenticateBearer(req, "gk_forged_more"); !errors.Is(err, errKeyLookupThrottled) {
		t.Fatalf("lookup past the budget: %v, want throttled", err)
	}
	if n := int(store.calls.Load()); n != api.GatewayFailedKeyLookupsPerSource {
		t.Fatalf("store lookups = %d, want %d (none once throttled)", n, api.GatewayFailedKeyLookupsPerSource)
	}
	other := httptest.NewRequest("GET", "http://app.example.test/", nil)
	other.Header.Set("X-Forwarded-For", "198.51.100.10")
	if _, _, err := h.authenticateBearer(other, "gk_other"); errors.Is(err, errKeyLookupThrottled) {
		t.Fatal("another source was throttled")
	}
}
