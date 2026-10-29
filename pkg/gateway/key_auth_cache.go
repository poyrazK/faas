package gateway

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrAPIKeyUnknown is a well-formed key with no api_keys row. The production
// adapter maps state.ErrNotFound to it so the cache can remember the miss.
var ErrAPIKeyUnknown = errAuthnSentinel("api_key_unknown")

// errKeyLookupThrottled reports a client that has failed too many key lookups
// recently; the caller answers 429 without asking the control plane.
var errKeyLookupThrottled = errors.New("gateway: key lookup throttled")

// KeyAuthCache keeps recent AuthenticateKey answers in gatewayd-internal.
// Without it every request to a key-protected app cost two control-plane
// Postgres reads, forged keys included, so one client could drive CP load at
// the gateway's request rate and a CP blip turned into 401s for those apps.
//
// Verified keys are kept for api.GatewayKeyAuthCacheTTL and, while the store
// is failing, served for up to api.GatewayKeyAuthOutageGrace. Definite misses
// (unknown, expired, revoked) are kept for the same TTL. Invalidate, wired to
// the key_changed notification, drops everything, so a revocation takes
// effect at once on a connected gateway and within the TTL otherwise.
type KeyAuthCache struct {
	next RequireAuthnAuthenticator
	now  func() time.Time

	mu    sync.Mutex
	epoch uint64
	hits  map[[sha256.Size]byte]keyAuthHit
	miss  map[[sha256.Size]byte]keyAuthMiss
}

type keyAuthHit struct {
	acct       RequireAuthnAccount
	key        RequireAuthnKey
	verifiedAt time.Time
}

type keyAuthMiss struct {
	err     error
	expires time.Time
}

// NewKeyAuthCache wraps next. A nil next returns nil, so the gateway's
// "no authenticator" handling is unchanged.
func NewKeyAuthCache(next RequireAuthnAuthenticator) *KeyAuthCache {
	if next == nil {
		return nil
	}
	return &KeyAuthCache{
		next: next,
		now:  time.Now,
		hits: map[[sha256.Size]byte]keyAuthHit{},
		miss: map[[sha256.Size]byte]keyAuthMiss{},
	}
}

// AuthenticateKey implements RequireAuthnAuthenticator.
func (c *KeyAuthCache) AuthenticateKey(ctx context.Context, hash []byte) (RequireAuthnAccount, RequireAuthnKey, error) {
	id := keyAuthID(hash)
	now := c.now()
	c.mu.Lock()
	hit, hasHit := c.hits[id]
	if hasHit && now.Sub(hit.verifiedAt) < api.GatewayKeyAuthCacheTTL {
		c.mu.Unlock()
		return hit.acct, copyKeyAuth(hit.key), nil
	}
	if m, ok := c.miss[id]; ok && now.Before(m.expires) {
		c.mu.Unlock()
		return RequireAuthnAccount{}, RequireAuthnKey{}, m.err
	}
	epoch := c.epoch
	c.mu.Unlock()

	acct, key, err := c.next.AuthenticateKey(ctx, hash)
	definite := err == nil || errors.Is(err, ErrAPIKeyUnknown) || errors.Is(err, ErrAPIKeyExpired) || errors.Is(err, ErrAPIKeyRevoked)
	if !definite {
		// The store failed. A key verified shortly before keeps working
		// through the outage; anything else fails as before.
		if hasHit && now.Sub(hit.verifiedAt) < api.GatewayKeyAuthOutageGrace {
			return hit.acct, copyKeyAuth(hit.key), nil
		}
		return acct, key, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epoch != epoch {
		// Invalidated while the lookup was in flight: the answer may
		// predate a revocation, so it is returned but not kept.
		return acct, key, err
	}
	if err == nil {
		delete(c.miss, id)
		if len(c.hits) >= api.GatewayKeyAuthCacheEntries {
			c.sweepLocked(now)
		}
		if len(c.hits) < api.GatewayKeyAuthCacheEntries {
			c.hits[id] = keyAuthHit{acct: acct, key: copyKeyAuth(key), verifiedAt: now}
		}
		return acct, key, nil
	}
	delete(c.hits, id)
	if len(c.miss) >= api.GatewayKeyAuthCacheEntries {
		c.sweepLocked(now)
	}
	if len(c.miss) < api.GatewayKeyAuthCacheEntries {
		c.miss[id] = keyAuthMiss{err: err, expires: now.Add(api.GatewayKeyAuthCacheTTL)}
	}
	return acct, key, err
}

// Invalidate drops every cached answer. Wired to the key_changed
// notification, which apid sends on every key create, revoke and rotation.
func (c *KeyAuthCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	clear(c.hits)
	clear(c.miss)
}

func (c *KeyAuthCache) sweepLocked(now time.Time) {
	for id, h := range c.hits {
		if now.Sub(h.verifiedAt) >= api.GatewayKeyAuthOutageGrace {
			delete(c.hits, id)
		}
	}
	for id, m := range c.miss {
		if !now.Before(m.expires) {
			delete(c.miss, id)
		}
	}
}

func keyAuthID(hash []byte) [sha256.Size]byte {
	var id [sha256.Size]byte
	copy(id[:], hash)
	return id
}

func copyKeyAuth(k RequireAuthnKey) RequireAuthnKey {
	k.Scopes = append([]string(nil), k.Scopes...)
	return k
}

// keyLookupLimiter caps how many failed key lookups one client source (an
// IPv4 address or IPv6 /64) may cause per window. A random forged key misses
// every cache, so without it each one still reached Postgres; past the cap the
// gateway answers 429 without a lookup. Legitimate clients do not present
// dozens of invalid keys a minute.
type keyLookupLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	window  time.Time
	failed  map[string]int
	maxKeys int
}

func newKeyLookupLimiter() *keyLookupLimiter {
	return &keyLookupLimiter{now: time.Now, failed: map[string]int{}, maxKeys: api.GatewayKeyLookupSourcesMax}
}

func (l *keyLookupLimiter) rollLocked() {
	now := l.now()
	if now.Sub(l.window) >= api.GatewayFailedKeyLookupWindow {
		l.window = now
		clear(l.failed)
	}
}

// allow reports whether source may look up another key.
func (l *keyLookupLimiter) allow(source string) bool {
	if l == nil || source == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	return l.failed[source] < api.GatewayFailedKeyLookupsPerSource
}

// fail records a failed lookup by source.
func (l *keyLookupLimiter) fail(source string) {
	if l == nil || source == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rollLocked()
	if _, seen := l.failed[source]; !seen && len(l.failed) >= l.maxKeys {
		return
	}
	l.failed[source]++
}

// authenticateBearer verifies a bearer token for an app gate, refusing
// clients past their failed-lookup budget before any store call.
func (h *Handler) authenticateBearer(r *http.Request, tok string) (RequireAuthnAccount, RequireAuthnKey, error) {
	source := keyLookupSource(r)
	if !h.keyLookups.allow(source) {
		return RequireAuthnAccount{}, RequireAuthnKey{}, errKeyLookupThrottled
	}
	acct, key, err := h.requireAuthnAuthn.AuthenticateKey(r.Context(), api.HashAPIKey(tok))
	if err != nil {
		h.keyLookups.fail(source)
	}
	return acct, key, err
}

// keyLookupSource is the failed-lookup budget key: the trusted client
// address (an IPv6 /64), or "" (no budget) when the hop set none.
func keyLookupSource(r *http.Request) string {
	if ip, ok := TrustedClientIP(r); ok {
		return throttleIPKey(ip)
	}
	return ""
}

// writeKeyLookupThrottled answers a client past its failed-lookup budget.
func writeKeyLookupThrottled(w http.ResponseWriter) {
	api.WriteProblem(w, api.ErrAuthRateLimited(int(api.GatewayFailedKeyLookupWindow.Seconds())))
}
