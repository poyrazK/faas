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

// ownedKeyAuth answers each key hash with the owner the test registered.
type ownedKeyAuth struct {
	calls  atomic.Int32
	err    error
	owners map[string]struct{ acct, org string }
}

func (o *ownedKeyAuth) AuthenticateKey(_ context.Context, hash []byte) (RequireAuthnAccount, RequireAuthnKey, error) {
	o.calls.Add(1)
	if o.err != nil {
		return RequireAuthnAccount{}, RequireAuthnKey{}, o.err
	}
	for tok, owner := range o.owners {
		if string(api.HashAPIKey(tok)) == string(hash) {
			return RequireAuthnAccount{ID: owner.acct}, RequireAuthnKey{ID: tok, OrgID: owner.org}, nil
		}
	}
	return RequireAuthnAccount{}, RequireAuthnKey{}, ErrAPIKeyUnknown
}

func TestKeyAuthCacheKeyChangesDropOnlyTheirOwner(t *testing.T) {
	ctx := context.Background()
	store := &ownedKeyAuth{owners: map[string]struct{ acct, org string }{
		"alice":     {acct: "acct-a"},
		"bob":       {acct: "acct-b"},
		"alice-org": {acct: "acct-a", org: "org-1"},
		"carol-org": {acct: "acct-c", org: "org-1"},
		"dave-org":  {acct: "acct-d", org: "org-2"},
	}}
	c := NewKeyAuthCache(store)
	warm := func() {
		for tok := range store.owners {
			if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey(tok)); err != nil {
				t.Fatalf("%s: %v", tok, err)
			}
		}
	}
	lookups := func(toks ...string) int32 {
		before := store.calls.Load()
		for _, tok := range toks {
			if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey(tok)); err != nil {
				t.Fatalf("%s: %v", tok, err)
			}
		}
		return store.calls.Load() - before
	}
	all := []string{"alice", "bob", "alice-org", "carol-org", "dave-org"}
	warm()

	for _, payload := range []string{
		`{"kind":"created","account":"acct-a","key":"k9"}`,
		`{"kind":"created","org":"org-1"}`,
	} {
		ch, ok := ParseKeyChange(payload)
		if !ok {
			t.Fatalf("payload %s did not parse", payload)
		}
		c.ApplyKeyChange(ch)
		if n := lookups(all...); n != 0 {
			t.Fatalf("after %s: %d store lookups, want 0 (a created key changes no cached answer)", payload, n)
		}
	}

	ch, _ := ParseKeyChange(`{"kind":"revoked","account":"acct-a"}`)
	c.ApplyKeyChange(ch)
	if n := lookups("alice", "alice-org"); n != 2 {
		t.Fatalf("account revocation: %d lookups of acct-a's keys, want 2", n)
	}
	if n := lookups("bob", "carol-org", "dave-org"); n != 0 {
		t.Fatalf("account revocation re-read %d other owners' keys", n)
	}

	ch, _ = ParseKeyChange(`{"kind":"rotated","org":"org-1"}`)
	c.ApplyKeyChange(ch)
	if n := lookups("alice-org", "carol-org"); n != 2 {
		t.Fatalf("org rotation: %d lookups of org-1's keys, want 2", n)
	}
	if n := lookups("alice", "bob", "dave-org"); n != 0 {
		t.Fatalf("org rotation re-read %d keys outside org-1", n)
	}

	if _, ok := ParseKeyChange(`{"key_id":"k1"}`); ok {
		t.Fatal("a payload naming no owner parsed as scoped")
	}
	if _, ok := ParseKeyChange(`not json`); ok {
		t.Fatal("a malformed payload parsed as scoped")
	}
}

func TestKeyAuthCacheExpireRevalidatesButKeepsOutageFallback(t *testing.T) {
	ctx := context.Background()
	store := &ownedKeyAuth{owners: map[string]struct{ acct, org string }{"alice": {acct: "acct-a"}}}
	c := NewKeyAuthCache(store)
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("alice")); err != nil {
		t.Fatal(err)
	}
	c.Expire()
	if _, _, err := c.AuthenticateKey(ctx, api.HashAPIKey("alice")); err != nil || store.calls.Load() != 2 {
		t.Fatalf("after a resync the key must be revalidated: err=%v lookups=%d", err, store.calls.Load())
	}

	c.Expire()
	store.err = errors.New("control plane unavailable")
	if _, key, err := c.AuthenticateKey(ctx, api.HashAPIKey("alice")); err != nil || key.ID != "alice" {
		t.Fatalf("a resync while the store fails dropped the outage fallback: %v %v", key, err)
	}
}

func TestAuthenticateBearerStoreErrorsDoNotSpendTheBudget(t *testing.T) {
	store := &countingKeyAuth{err: errors.New("control plane unavailable")}
	h := &Handler{requireAuthnAuthn: store, keyLookups: newKeyLookupLimiter()}
	req := httptest.NewRequest("GET", "http://app.example.test/", nil)
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	for i := range api.GatewayFailedKeyLookupsPerSource + 5 {
		if _, _, err := h.authenticateBearer(req, "gk_real"); errors.Is(err, errKeyLookupThrottled) {
			t.Fatalf("lookup %d throttled after store errors only", i)
		}
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
