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
// Expiry applies to every key ID, including a missing ID, so removed keys
// cannot stay trusted indefinitely just because callers keep presenting them.
//
// Per-URL key cap (1024) is enforced post-fetch — if the fetched
// keyset has more than 1024 keys (an IdP misconfiguration / abuse
// signal) we drop the URL with an error rather than cache it.
package edgejwks

import (
	"context"
	"errors"
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
	// MinRefreshInterval). An unknown kid may refresh early, at most once
	// per UnknownKIDRefreshInterval. A failed refresh serves the previous
	// set for one further interval of grace, then fails with
	// ErrJWKSUnavailable.
	// The bool indicates "this URL has been registered at least once" — false means the
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

// DefaultMinRefreshInterval is the window for demand-driven refreshes per
// URL. A Get after this window refreshes known and unknown key IDs alike;
// unknown IDs cannot trigger unbounded fetches inside the window.
const DefaultMinRefreshInterval = 5 * time.Minute

// UnknownKIDRefreshInterval is the minimum spacing of the early refresh an
// unrecognised key ID triggers inside the freshness window. An IdP that
// rotates in a new signing key is picked up within this bound instead of
// after MinRefreshInterval, while a client minting random kids still costs
// at most one fetch per interval per URL.
const UnknownKIDRefreshInterval = 30 * time.Second

// FetchFailureBackoff spaces fetch attempts after a failure, so requests
// during an IdP outage fail (or use the grace keyset) immediately instead of
// queueing one-by-one behind a fetch timeout each.
const FetchFailureBackoff = 5 * time.Second

// ErrJWKSUnavailable marks a keyset that could not be fetched and has no
// usable cached copy. It is the IdP or network failing, not the token, so
// the gateway answers 503 rather than 401.
var ErrJWKSUnavailable = errors.New("edgejwks: jwks unavailable")

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

// jwksCache is the production impl. Per-URL state is guarded by cancelable
// gates so waiters retain their own request deadlines. Distinct URLs do not
// contend; the registry map itself is guarded by mu.
type jwksCache struct {
	mu         sync.Mutex
	byURL      map[string]*urlEntry
	httpClient *http.Client
	refresh    time.Duration
	fetchTO    time.Duration
	onFetchErr func(url string, err error)
}

type urlEntry struct {
	gate        chan struct{}
	set         *jose.JSONWebKeySet
	lastFetch   time.Time
	lastAttempt time.Time // last fetch start, success or not
	lastFailure time.Time // zero after a successful fetch
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
		c.byURL[rawURL] = &urlEntry{gate: make(chan struct{}, 1)}
	}
	c.mu.Unlock()
	return nil
}

// Get returns the cached keyset for url, fetching on the first call after
// expiry. Expiry is independent of kid (which may be empty); a kid absent
// from a fresh set additionally allows one early refresh per
// UnknownKIDRefreshInterval so key rotations are picked up promptly.
func (c *jwksCache) Get(ctx context.Context, rawURL string, kid string) (*jose.JSONWebKeySet, bool, error) {
	c.mu.Lock()
	entry, ok := c.byURL[rawURL]
	c.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	select {
	case entry.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, true, ctx.Err()
	}
	defer func() { <-entry.gate }()
	// The gate and ctx.Done may both have been ready. Do not return a
	// cached success or start network work for an already-canceled caller.
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	now := time.Now()
	if entry.set != nil && now.Sub(entry.lastFetch) < c.refresh && !c.unknownKIDRefreshDue(entry, kid, now) {
		return entry.set, true, nil
	}
	if !entry.lastFailure.IsZero() && now.Sub(entry.lastFailure) < FetchFailureBackoff {
		return c.graceSet(entry, now, errors.New("fetch backing off after a recent failure"))
	}
	entry.lastAttempt = now
	if err := entry.fetch(ctx, c.httpClient, c.fetchTO, rawURL); err != nil {
		if ctx.Err() == nil {
			entry.lastFailure = now
		}
		if c.onFetchErr != nil {
			c.onFetchErr(rawURL, err)
		}
		return c.graceSet(entry, now, err)
	}
	entry.lastFailure = time.Time{}
	return entry.set, true, nil
}

// unknownKIDRefreshDue reports whether a key ID absent from a still-fresh set
// warrants an early refetch: the IdP may have rotated in a new signing key.
// The spacing bound keeps random kids from turning into a fetch per request.
func (c *jwksCache) unknownKIDRefreshDue(entry *urlEntry, kid string, now time.Time) bool {
	if kid == "" || len(entry.set.Key(kid)) > 0 {
		return false
	}
	return now.Sub(entry.lastAttempt) >= UnknownKIDRefreshInterval
}

// graceSet answers a failed or backed-off refresh. A set fetched within
// MinRefreshInterval plus one further interval of grace is still served, so
// a short IdP outage does not reject every token; past that bound the set is
// dropped and the caller fails closed, so a key the IdP has removed is
// trusted for at most two refresh intervals (ADR-091 D14).
func (c *jwksCache) graceSet(entry *urlEntry, now time.Time, cause error) (*jose.JSONWebKeySet, bool, error) {
	if entry.set != nil && now.Sub(entry.lastFetch) < 2*c.refresh {
		return entry.set, true, nil
	}
	entry.set = nil
	return nil, true, fmt.Errorf("%w: %w", ErrJWKSUnavailable, cause)
}

// fetch performs a single HTTP GET of the JWKS URL, parses into a
// jose.JSONWebKeySet, enforces MaxKeysPerJWKSURL, and stores in
// entry. Singleflight: only one fetch per URL at a time; concurrent
// callers wait via entry.gate (already held by the caller), or cancel their
// own wait without interrupting the in-flight request.
func (e *urlEntry) fetch(ctx context.Context, hc *http.Client, timeout time.Duration, rawURL string) error {
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
