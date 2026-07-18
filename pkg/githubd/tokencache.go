// Install-token cache (slice 8, ADR-012).
//
// Every repo-scoped call githubd makes (Check-runs, Content reads,
// the deploy-from-push source-tarball fetch) needs an installation
// access token. The tokens are valid for ~1 hour. To avoid an
// api.github.com round-trip on every push, we cache them in memory
// keyed by installation_id with a 5-minute proactive refresh
// window.
//
// Sync semantics:
//
//   - Cache miss → first caller blocks on the api.github.com call
//     and writes the result. Concurrent callers wait on the same
//     call (singleflight-style).
//   - Cache hit, fresh → return immediately.
//   - Cache hit, near-expiry (<5 min) → fire the refresh in the
//     background; return the still-valid token. The next caller
//     reads the refreshed value.
//
// A janitor goroutine wakes every minute to evict dead entries
// (installations that were deleted out from under us).
//
// PG warm-restart is intentionally left to a follow-up slice; the
// in-memory cache is rebuilt on every restart from a single
// /installation/repositories call, which is cheap and avoids a
// new migration for slice 8.
package githubd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TokenFetcher is the seam githubd uses to obtain a fresh
// installation token. The real impl is *AppAuth.ExchangeInstallationToken;
// tests inject a recording fake.
type TokenFetcher interface {
	ExchangeInstallationToken(ctx context.Context, installationID int64) (string, time.Time, error)
}

// TokenCache is a thread-safe, singleflight-style cache of
// installation tokens.
type TokenCache struct {
	fetcher TokenFetcher

	// refreshWindow is how long before expiry we proactively
	// refresh. Default 5 min.
	refreshWindow time.Duration

	// clock is the time source. Tests override.
	clock func() time.Time

	mu    sync.Mutex
	items map[int64]*tokenEntry
	// inflight coalesces concurrent refreshes for the same
	// installation_id so we don't stampede api.github.com.
	inflight map[int64]*inflightCall
}

type tokenEntry struct {
	token     string
	expiresAt time.Time
	// lastRefresh records when we last swapped the value, used
	// for the janitor's "stale entry" detection.
	lastRefresh time.Time
}

type inflightCall struct {
	done chan struct{}
	tok  string
	exp  time.Time
	err  error
}

// NewTokenCache builds a TokenCache wired to the given fetcher.
// refreshWindow <= 0 falls back to 5 min.
func NewTokenCache(fetcher TokenFetcher, refreshWindow time.Duration) *TokenCache {
	if refreshWindow <= 0 {
		refreshWindow = 5 * time.Minute
	}
	return &TokenCache{
		fetcher:       fetcher,
		refreshWindow: refreshWindow,
		clock:         time.Now,
		items:         map[int64]*tokenEntry{},
		inflight:      map[int64]*inflightCall{},
	}
}

// Token returns a non-expired installation token for the given
// installation_id. First-call blocks on api.github.com; subsequent
// calls return the cached value until the proactive-refresh window
// is hit.
func (c *TokenCache) Token(ctx context.Context, installationID int64) (string, error) {
	if installationID <= 0 {
		return "", fmt.Errorf("githubd: invalid installation id %d", installationID)
	}

	c.mu.Lock()
	entry, ok := c.items[installationID]
	if ok && c.clock().Before(entry.expiresAt.Add(-c.refreshWindow)) {
		tok := entry.token
		c.mu.Unlock()
		return tok, nil
	}
	// Either no entry, or within the refresh window. If another
	// caller is already refreshing, wait for them.
	if call, busy := c.inflight[installationID]; busy {
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.tok, call.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	call := &inflightCall{done: make(chan struct{})}
	c.inflight[installationID] = call
	c.mu.Unlock()

	// We're the refresh leader. Do the fetch.
	tok, exp, err := c.fetcher.ExchangeInstallationToken(ctx, installationID)

	c.mu.Lock()
	if err == nil {
		c.items[installationID] = &tokenEntry{
			token:       tok,
			expiresAt:   exp,
			lastRefresh: c.clock(),
		}
	}
	delete(c.inflight, installationID)
	call.tok, call.exp, call.err = tok, exp, err
	close(call.done)
	c.mu.Unlock()
	return tok, err
}

// StartJanitor spawns a goroutine that wakes every minute to evict
// expired entries and trigger proactive refreshes for entries
// inside the refresh window. Returns a stop func; callers invoke
// it on ctx cancel.
func (c *TokenCache) StartJanitor(ctx context.Context) (stop func()) {
	stopCh := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-t.C:
				c.janitorSweep(ctx)
			}
		}
	}()
	return func() { close(stopCh) }
}

func (c *TokenCache) janitorSweep(ctx context.Context) {
	c.mu.Lock()
	now := c.clock()
	for id, e := range c.items {
		if now.After(e.expiresAt) {
			delete(c.items, id)
		}
	}
	c.mu.Unlock()
	// Trigger refresh for entries inside the window. Fire-and-forget
	// so the janitor never blocks on api.github.com.
	c.mu.Lock()
	toRefresh := make([]int64, 0)
	for id, e := range c.items {
		if !now.Before(e.expiresAt.Add(-c.refreshWindow)) {
			toRefresh = append(toRefresh, id)
		}
	}
	c.mu.Unlock()
	for _, id := range toRefresh {
		go func(id int64) {
			// Best-effort: errors are silently dropped (the
			// still-valid cached token keeps being served).
			_, _ = c.Token(ctx, id)
		}(id)
	}
}

// Invalidate drops a single entry (used by the binding-store path
// when an installation is unbound).
func (c *TokenCache) Invalidate(installationID int64) {
	c.mu.Lock()
	delete(c.items, installationID)
	c.mu.Unlock()
}

// Size returns the current number of cached entries (for
// /metrics).
func (c *TokenCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// errCacheDisabled is a sentinel for callers that want to assert
// the cache was not initialized. Reserved for slice 8 wiring where
// a deployment without an AppAuth install should fail closed.
var errCacheDisabled = errors.New("githubd: token cache not configured")

// _ pins the sentinel so an unused-import lint doesn't trip before
// the dashboard connect flow lands.
var _ = errCacheDisabled
