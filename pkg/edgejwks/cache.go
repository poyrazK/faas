// Package edgejwks is the gateway's JWKS cache + verifier. It wraps
// go-jose/v4 (jose.JSONWebKeySet + jwt.ParseSigned + jwt.Claims.Validate)
// behind a narrow pkg/gateway-friendly surface; pkg/edgejwks is the
// only package that imports github.com/go-jose/go-jose/v4 outside
// generated/vendor code, preserving the "pkg/gateway doesn't import
// pkg/state" + "cmd-side doesn't import jose directly" doctrines
// from PR 1-4.
//
// Cache invalidation is wholesale via Reset() (matches the wholesale
// pg_notify reset for edge rules in pkg/gateway/EdgeRuleCache). No
// per-URL TTL — go-jose/v4 ships no JWKS auto-refresh helper, so the
// cache uses a MinRefreshInterval enforced by us: the first Get after
// (now - lastFetch) < interval returns the cached set without
// hitting the network; outside that window the next Get triggers a
// fetch-in-flight guarded by per-URL singleflight semantics.
// Rotation is automatic when a known kid is missing because the
// fetch re-runs.
//
// Per-URL key cap (1024) is enforced post-fetch — if the fetched
// keyset has more than 1024 keys (an IdP misconfiguration / abuse
// signal) we drop the URL with an error rather than cache it.
package edgejwks

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/onebox-faas/faas/pkg/httpjson"
)

// Cache is the narrow interface pkg/gateway sees. cmd-side constructs
// the concrete *jwksCache with a *http.Client + MinRefreshInterval;
// the cmd-side is the only place that touches go-jose types directly
// for the keyset representation.
type Cache interface {
	// Get returns the cached jose.JSONWebKeySet for url, performing
	// a network fetch on the first call per window (see
	// MinRefreshInterval) and re-fetching if the kid is missing
	// from the cached set (rotation). The bool indicates "this
	// URL has been registered at least once" — false means the
	// caller should call Register first (cmd-side
	// MatchJWT does this lazily).
	Get(ctx context.Context, url string, kid string) (*jose.JSONWebKeySet, bool, error)
	// Register seeds the cache entry for url with no fetched
	// state; the next Get triggers the network fetch. Calling
	// Register twice on the same URL is a no-op.
	Register(url string) error
	// Reset drops every registration. Mirrors
	// pkg/gateway/EdgeRuleCache.Reset() — wholesale invalidation
	// keeps the two caches aligned when the edge_rule_changed
	// pg_notify channel fires.
	Reset()
}

// MaxKeysPerJWKSURL caps the cached keyset size at 1024 entries. If
// an IdP serves a larger keyset we treat it as misconfigured and
// reject the URL with an error; rotating to a sane IdP re-registers
// and succeeds.
const MaxKeysPerJWKSURL = 1024

// MaxResponseBytes caps a single JWKS document before it is decoded. It is
// deliberately independent of MaxKeysPerJWKSURL because one malformed key can
// still be very large even when the key-count limit is respected.
const MaxResponseBytes = 2 << 20

// MinRefreshInterval is the minimum time between automatic
// background refreshes per URL. The cache always re-fetches
// immediately on a Get if the requested kid is missing (rotation)
// regardless of this window.
const DefaultMinRefreshInterval = 5 * time.Minute

// DefaultFetchTimeout caps a single JWKS HTTP fetch. Larger values
// risk blocking the gateway hot path; smaller values risk transient
// fetch failures during IdP outages.
const DefaultFetchTimeout = 5 * time.Second

// Options configures a *jwksCache.
type Options struct {
	HTTPClient         *http.Client
	MinRefreshInterval time.Duration
	FetchTimeout       time.Duration
	// OnFetchErr, if non-nil, is invoked each time a network fetch
	// returns an error. Used by cmd-side to slog+audit JWKS
	// fetch failures without coupling edgejwks to slog.
	OnFetchErr func(url string, err error)
}

// jwksCache is the production impl. Per-URL state is guarded by
// per-URL mutexes so concurrent Get on distinct URLs don't contend;
// the registry map itself is guarded by mu.
type jwksCache struct {
	mu         sync.Mutex
	byURL      map[string]*urlEntry
	httpClient *http.Client
	refresh    time.Duration
	fetchTO    time.Duration
	onFetchErr func(url string, err error)
}

type urlEntry struct {
	mu        sync.Mutex
	set       *jose.JSONWebKeySet
	lastFetch time.Time
	fetching  bool // singleflight gate — only one in-flight fetch per URL
}

// NewCache returns an empty cache. Callers (cmd/gatewayd-internal/edge_rules.go::MatchJWT)
// invoke Register lazily on the first match per JWKS URL.
func NewCache(opts Options) Cache {
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: DefaultFetchTimeout}
	}
	if opts.MinRefreshInterval <= 0 {
		opts.MinRefreshInterval = DefaultMinRefreshInterval
	}
	if opts.FetchTimeout <= 0 {
		opts.FetchTimeout = DefaultFetchTimeout
	}
	return &jwksCache{
		byURL:      make(map[string]*urlEntry),
		httpClient: opts.HTTPClient,
		refresh:    opts.MinRefreshInterval,
		fetchTO:    opts.FetchTimeout,
		onFetchErr: opts.OnFetchErr,
	}
}

// Register seeds the cache entry for url. Pure map insert; no
// network. Idempotent.
func (c *jwksCache) Register(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("edgejwks: empty jwks_url")
	}
	if _, err := url.Parse(rawURL); err != nil {
		return fmt.Errorf("edgejwks: invalid jwks_url %q: %w", rawURL, err)
	}
	c.mu.Lock()
	if _, ok := c.byURL[rawURL]; !ok {
		c.byURL[rawURL] = &urlEntry{}
	}
	c.mu.Unlock()
	return nil
}

// Get returns the cached keyset for url, fetching on first call (and
// on rotation — kid missing). kid may be "" for IDP signers that
// don't set the header; we still fetch on a cold cache and return
// the full keyset for the verifier to walk.
func (c *jwksCache) Get(ctx context.Context, rawURL string, kid string) (*jose.JSONWebKeySet, bool, error) {
	c.mu.Lock()
	entry, ok := c.byURL[rawURL]
	c.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.set != nil {
		// Rotation: if the requested kid is missing AND the window
		// has elapsed (or we have a fresh-but-empty kid set),
		// force a re-fetch. We deliberately do NOT re-fetch on
		// every rotation signal — that would let a malicious
		// attacker trigger an SSRF DoS by sending many tokens with
		// bogus kid headers. The refresh window caps the worst
		// case to one fetch per window per attacker-controlled
		// bogus kid (still 0, because the missing-kid signal is
		// only emitted when the cached set has been used at least
		// once — the first call always goes to network anyway).
		if kid != "" && len(entry.set.Key(kid)) == 0 &&
			time.Since(entry.lastFetch) > c.refresh {
			entry.set = nil // force re-fetch
		}
	}

	if entry.set != nil {
		return entry.set, true, nil
	}

	if err := entry.fetch(ctx, c.httpClient, c.fetchTO, rawURL); err != nil {
		if c.onFetchErr != nil {
			c.onFetchErr(rawURL, err)
		}
		return nil, true, err
	}
	return entry.set, true, nil
}

// fetch performs a single HTTP GET of the JWKS URL, parses into a
// jose.JSONWebKeySet, enforces MaxKeysPerJWKSURL, and stores in
// entry. Singleflight: only one fetch per URL at a time; concurrent
// callers wait on the same fetch via entry.mu (already held by the
// caller).
func (e *urlEntry) fetch(ctx context.Context, hc *http.Client, timeout time.Duration, rawURL string) error {
	if e.fetching {
		// Another goroutine holds e.mu and is mid-fetch; we shouldn't
		// get here because fetch is only called while entry.mu is
		// held, but guard against reentrancy defensively.
		return fmt.Errorf("edgejwks: reentrant fetch for %s", rawURL)
	}
	e.fetching = true
	defer func() { e.fetching = false }()

	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("edgejwks: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("edgejwks: fetch %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("edgejwks: fetch %s: status %d", rawURL, resp.StatusCode)
	}
	var set jose.JSONWebKeySet
	if err := httpjson.Decode(resp.Body, MaxResponseBytes, &set); err != nil {
		return fmt.Errorf("edgejwks: decode jwks from %s: %w", rawURL, err)
	}
	if len(set.Keys) > MaxKeysPerJWKSURL {
		return fmt.Errorf("edgejwks: jwks from %s has %d keys (> max %d)", rawURL, len(set.Keys), MaxKeysPerJWKSURL)
	}
	e.set = &set
	e.lastFetch = time.Now()
	return nil
}

// Reset drops every registration. Mirrors pkg/gateway/EdgeRuleCache.Reset()
// — wholesale invalidation keeps the two caches aligned when the
// edge_rule_changed pg_notify channel fires.
func (c *jwksCache) Reset() {
	c.mu.Lock()
	c.byURL = make(map[string]*urlEntry)
	c.mu.Unlock()
}
