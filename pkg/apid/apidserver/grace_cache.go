// grace-window cache (issue #189 / IAM-5).
//
// cmd/apid doesn't read accounts.key_grace_window_days on the
// bearer-key auth path (that would add a per-request PG hit to
// every API call). Only the rotation handler reads it; the
// handler is rare (one POST per customer-initiated rotation, not
// per request). We still cache the read because a customer
// firing a CI rotation loop shouldn't hammer the same SELECT.
//
// The cache is intentionally tiny: keyed by account ID, value is
// a (pointer-or-nil, expires-at) pair. The TTL is 60 s in
// production so admin updates propagate within a minute; tests
// inject a sub-second TTL via graceWindowCacheForTest so the
// "cache hit" path is exercised without sleeping.
//
// The cache is invalidated on SetAccountKeyGraceWindow
// (PATCH /v1/account/keys/grace_window_days) so the admin
// surface is never stale. Rotation callers therefore observe
// the new value on the next request after the PATCH returns.
package apidserver

import (
	"context"
	"sync"
	"time"
)

// graceWindowCacheTTL is the lifetime of a cached grace-window
// read. 60 s balances "admin update propagates within a minute"
// against "the rotation handler doesn't refetch for every CI
// loop tick". Tunable via cmd/apid/main.go in a future PR; today
// the constant is the spec literal.
const graceWindowCacheTTL = 60 * time.Second

// graceEntry holds the cached read for one account. value is
// the per-account override (nil = no override; the rotation
// handler falls back to api.DefaultAPIKeyGraceWindowDays). ttl
// is when the entry expires; on read past ttl, the cache misses
// and the store is consulted.
type graceEntry struct {
	value *int
	ttl   time.Time
}

// graceWindowCache is the in-process cache apid's rotateKey
// handler reads. Construct one per server instance; the zero
// value is a ready-to-use cache with a real-clock TTL.
//
// graceWindowCache is intentionally not generic — the rotate
// handler is the only caller and the read-shape is stable. If a
// future feature needs the same cache shape, lift to pkg/cache.
type graceWindowCache struct {
	mu    sync.Mutex
	clock func() time.Time
	ttl   time.Duration
	m     map[string]graceEntry
}

// newGraceWindowCache returns a fresh cache with the production
// 60 s TTL and a real clock. Tests call newGraceWindowCacheForTest
// to inject a clock + TTL.
func newGraceWindowCache() *graceWindowCache {
	return &graceWindowCache{
		clock: time.Now,
		ttl:   graceWindowCacheTTL,
		m:     map[string]graceEntry{},
	}
}

// newGraceWindowCacheForTest returns a cache with a custom clock
// and TTL; the rotation handler tests use a 50 ms TTL to exercise
// the expiry path without sleeping the test.
func newGraceWindowCacheForTest(clock func() time.Time, ttl time.Duration) *graceWindowCache {
	return &graceWindowCache{
		clock: clock,
		ttl:   ttl,
		m:     map[string]graceEntry{},
	}
}

// Get returns the cached entry for accountID, or nil if the
// entry is missing or expired. The caller is responsible for
// the miss → store → Set path; this helper only reads.
//
// A `nil` value (no entry, or expired) is the cache-miss
// signal; the cache distinguishes "no override" (value == nil,
// entry != nil) from "missing" (entry == nil) so the rotate
// handler's default-fallback path is exercised correctly.
func (c *graceWindowCache) Get(accountID string) *int {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[accountID]
	if !ok || c.clock().After(e.ttl) {
		delete(c.m, accountID)
		return nil
	}
	return e.value
}

// Set inserts or refreshes the cache entry for accountID. value
// may be nil (no per-account override). The TTL is reset on
// every Set so admin-side Get always sees a fresh expiry.
func (c *graceWindowCache) Set(accountID string, value *int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[accountID] = graceEntry{
		value: value,
		ttl:   c.clock().Add(c.ttl),
	}
}

// Invalidate drops the cache entry for accountID. Called by
// the admin PATCH handler so the new value is observed on the
// next rotation. Idempotent: invalidating a missing entry is a
// no-op.
func (c *graceWindowCache) Invalidate(accountID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, accountID)
}

// graceStore is the narrow surface resolveGraceWindow needs
// from the store. Lifting this from state.Store keeps the
// cache testable without a full Store stub — the cache is
// the only consumer of this surface today.
type graceStore interface {
	GetAccountKeyGraceWindow(ctx context.Context, accountID string) (*int, error)
}

// resolveGraceWindow returns the effective grace window for
// accountID, consulting the cache first and falling back to
// store.GetAccountKeyGraceWindow on a miss. The cache is
// updated on the read path so subsequent rotations avoid the
// PG trip.
//
// The return value is in days (int) so the caller can drop
// directly into api.SetGraceWindowDays / audit payloads
// without a Duration→Days conversion. nil means "no per-account
// override" — the caller falls back to the plan default
// (api.DefaultAPIKeyGraceWindowDays).
func (c *graceWindowCache) resolveGraceWindow(ctx context.Context, store graceStore, accountID string) (*int, error) {
	if v := c.Get(accountID); v != nil || c.hasNegativeMiss(accountID) {
		return v, nil
	}
	v, err := store.GetAccountKeyGraceWindow(ctx, accountID)
	if err != nil {
		return nil, err
	}
	c.Set(accountID, v)
	return v, nil
}

// hasNegativeMiss returns true when the cache has a recorded
// miss for accountID (i.e. the read returned nil but the
// negative-cache sentinel is set). Lets the resolve path
// avoid a redundant PG trip on the no-override case. Today
// the cache stores literal nil values with a TTL, so this
// helper is a no-op until a future contributor adds a
// negative-cache sentinel; kept here as the seam.
func (c *graceWindowCache) hasNegativeMiss(accountID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.m[accountID]
	return ok
}
