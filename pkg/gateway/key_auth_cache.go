package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
// (unknown, expired, revoked) are kept for the same TTL. ApplyKeyChange, wired
// to the key_changed notification, drops the entries of the account or
// organization whose keys were revoked or rotated, so a revocation takes
// effect at once on a connected gateway and within the TTL otherwise. Key
// creation drops nothing (bug hunt H93): a new key cannot be cached yet, and
// flushing every account's keys on each `gregale login` kept the hit rate low
// and removed the outage fallback.
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
	// stale marks a hit that must be revalidated before it is served
	// fresh: notifications may have been missed (a LISTEN resync). It
	// still serves as the outage fallback.
	stale bool
}

// KeyChange is one key_changed notification: what happened and whose keys.
type KeyChange struct {
	Kind    string `json:"kind"`
	Account string `json:"account"`
	Org     string `json:"org"`
}

// ParseKeyChange decodes a key_changed payload. ok is false when the payload
// names no account or organization, which callers treat as "anything may
// have changed".
func ParseKeyChange(payload string) (KeyChange, bool) {
	var ch KeyChange
	if json.Unmarshal([]byte(payload), &ch) != nil {
		return KeyChange{}, false
	}
	if ch.Account == "" && ch.Org == "" {
		return KeyChange{}, false
	}
	return ch, true
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
	if hasHit && !hit.stale && now.Sub(hit.verifiedAt) < api.GatewayKeyAuthCacheTTL {
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
	if err != nil && !isDefiniteKeyMiss(err) {
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

// Invalidate drops every cached answer: the response to a key_changed
// notification that names no owner.
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

// ApplyKeyChange applies one key_changed notification. A created key changes
// no cached answer. A revocation or rotation drops the verified keys of the
// named account (its own keys and the organization keys it created, which
// resolve to it) or of the named organization; an in-flight lookup started
// before it is not kept. Definite misses stay: a revoked or unknown key does
// not come back.
func (c *KeyAuthCache) ApplyKeyChange(ch KeyChange) {
	if c == nil || ch.Kind == "created" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for id, h := range c.hits {
		if (ch.Account != "" && h.acct.ID == ch.Account) || (ch.Org != "" && h.key.OrgID == ch.Org) {
			delete(c.hits, id)
		}
	}
}

// Expire marks every verified key for revalidation without dropping it, so
// each is checked against the store on its next use but still serves as the
// outage fallback. The response to a LISTEN resync, when revocations may
// have been missed: flushing then would remove the fallback just as a
// recovering control plane takes the full lookup load.
func (c *KeyAuthCache) Expire() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for id, h := range c.hits {
		h.stale = true
		c.hits[id] = h
	}
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

// copyKeyAuth returns k with its own Scopes slice.
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
	if isDefiniteKeyMiss(err) {
		h.keyLookups.fail(source)
	}
	return acct, key, err
}

// isDefiniteKeyMiss reports a lookup the store answered with "no such usable
// key". Only these count against a client's failed-lookup budget: a store
// error says nothing about the client, and counting it locked busy backend
// clients out with 429 for up to a minute after a control-plane blip (H93).
func isDefiniteKeyMiss(err error) bool {
	return errors.Is(err, ErrAPIKeyUnknown) || errors.Is(err, ErrAPIKeyExpired) || errors.Is(err, ErrAPIKeyRevoked)
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
