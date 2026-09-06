package gateway

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// cacheWriter (ADR-122 §Decision) is the response-body tee that
// captures the upstream's response for the kind=cache store
// path. Installed by ServeHTTP only when a kind=cache rule
// matched the request AND the request passed the
// cacheability predicate (method ∈ {GET, HEAD}; no
// Authorization; no session cookie; the host has at least one
// cache rule for this path/method).
//
// On WriteHeader the writer records statusCode and copies the
// outgoing header map (a defensive copy — the stdlib
// http.ResponseWriter reuses the map's backing arrays, and a
// later Set/Del on the live header must not retroactively
// mutate the cached body). On Write each chunk is appended to
// the in-memory buffer up to perEntryByteCap (1 MiB); on
// overflow the writer flips to bypass mode and stops buffering.
// Bypass mode is irreversible for this request — the buffer is
// cleared (so a downstream Put can't be tricked into storing a
// partial body), but the bytes already on the wire remain
// visible to the client.
//
// On the deferred finishCacheCapture in ServeHTTP, the writer
// commits the captured body to the cache IF and only if:
//
//   - status code is in the cacheable set
//     {200, 203, 300, 301, 308, 404, 410}
//   - response did NOT carry Set-Cookie
//   - response did NOT carry Cache-Control: no-store / private
//   - bypass did NOT fire (i.e., we buffered the full body)
//   - the body is non-empty (empty bodies are still stored on
//     200; an empty 200 cache hit is a legitimate "no rows"
//     response and serves correctly)
//
// Anything else drops the buffer with a single metric increment
// (`gateway_response_cache_total{outcome="store_skipped"}` in
// commit 15) so operators see why their cache didn't populate.
//
// cacheWriter is deliberately NOT safe to wrap further with
// capWriter — that's already done upstream (handler.go
// setupStreamingWriter); we sit INSIDE the capWriter chain, so
// our Write only sees bytes that already passed the cap. That
// ordering matters: a cacheWriter that wrapped capWriter could
// see a 413 problem+json fire mid-body, but at that point the
// WriteHeader has already happened and the status code is
// 413 — the cacheability predicate below would correctly skip
// the Put.
type cacheWriter struct {
	http.ResponseWriter
	inner *statusRecorder // the next-in-chain recorder (for header copy + status)
	rule  *EdgeRuleCacheResolved
	cap   int64

	buf       bytes.Buffer
	header    http.Header
	status    int
	headerOK  bool // WriteHeader was called
	bypass    bool // exceeded cap; stop buffering
	wroteBody bool

	// ruleAction is the state-typed view of the rule, captured
	// once at install time so Put on the cache has the
	// same shape every kind=cache entry has.
	ruleAction *state.EdgeRuleCacheAction
}

// ProblemHTMLRequest preserves browser error negotiation through the cache
// tee. Wake/admission failures can occur after this wrapper is installed.
func (c *cacheWriter) ProblemHTMLRequest() *http.Request {
	if c == nil {
		return nil
	}
	if provider, ok := c.ResponseWriter.(interface{ ProblemHTMLRequest() *http.Request }); ok {
		return provider.ProblemHTMLRequest()
	}
	return nil
}

// newCacheWriter installs a tee around w. The rec parameter is
// the statusRecorder already in the chain (the writer chain is
// w → cacheWriter → rec → ... → capWriter → real socket), and
// the cache parameter is the response cache that will receive
// the captured body at request-finish time.
//
// cap is the per-entry byte ceiling (1 MiB by default; see
// ResponseCacheDefaultMaxBytesEntry). The cap is independent of
// the plan-level response body cap (capWriter) — the cache cap
// is much smaller to bound resident memory per node.
func newCacheWriter(w http.ResponseWriter, rec *statusRecorder, rule *EdgeRuleCacheResolved, cap int64) *cacheWriter {
	return &cacheWriter{
		ResponseWriter: w,
		inner:          rec,
		rule:           rule,
		cap:            cap,
		ruleAction:     rule.toStateEdgeRuleCacheAction(),
		header:         http.Header{},
		status:         200, // default per http.ResponseWriter contract
	}
}

// WriteHeader captures the status code + a defensive copy of
// the header map at the moment the upstream commits. The cache
// store uses header to replay the response verbatim on a hit;
// header must NOT alias the live response map (the upstream
// stdlib writer reuses backing arrays across requests on the
// same connection).
func (c *cacheWriter) WriteHeader(code int) {
	if c.headerOK {
		// stdlib contract: WriteHeader after a previous WriteHeader
		// is a no-op. Mirror that here.
		return
	}
	c.status = code
	c.headerOK = true
	// Defensive copy. We snapshot from the embedded
	// ResponseWriter.Header() because that's where the
	// upstream stdlib reverse-proxy / hand-rolled
	// response writer stage its Set / Add calls. Reading
	// from c.inner.Header() would work in some chains
	// (the rec's Header() is its embedded ResponseWriter's
	// Header()) but breaks when the inner writer has its
	// own Header() override (e.g. capWriter). The embedded
	// ResponseWriter is the lowest-level writer in the
	// chain — its Header() is the canonical live map.
	for k, vs := range c.Header() {
		for _, v := range vs {
			c.header.Add(k, v)
		}
	}
	c.ResponseWriter.WriteHeader(code)
}

// Write tees bytes into the buffer until the cap is exceeded;
// past the cap, the writer enters bypass mode and stops
// buffering. Bypass is irreversible — finishCacheCapture will
// see bypass=true and skip the Put.
//
// Note: WriteHeader may not have been called yet (the upstream
// forwarder lazily calls WriteHeader on the first Write if no
// explicit status was set). We auto-claim WriteHeader(200) on
// the first Write so the captured status reflects "default
// 200" — matches the live response that hits the wire.
func (c *cacheWriter) Write(b []byte) (int, error) {
	if !c.headerOK {
		// Lazy WriteHeader from the upstream stdlib — mirror it.
		c.WriteHeader(http.StatusOK)
	}
	if !c.bypass {
		if c.buf.Len()+len(b) > int(c.cap) {
			// Cap exceeded. Flip to bypass and clear the
			// buffer so a later Put can't store a partial.
			// The bytes already written to the live writer
			// remain on the wire; only the cache buffer is
			// abandoned.
			c.buf.Reset()
			c.bypass = true
		} else {
			c.buf.Write(b)
		}
	}
	c.wroteBody = true
	return c.ResponseWriter.Write(b)
}

// Flush forwards so a streaming upstream calling Flush on the
// outer writer (cacheWriter here) still propagates to the
// underlying recorder. We don't try to flush the buffer — the
// buffer is committed at request-finish time, not in real
// time.
func (c *cacheWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// shouldStore reports whether the captured response is eligible
// for the cache. The predicate is the cache store's
// counterpart to the cache hit predicate in
// handler_apply_edge_rule_cache.go — symmetric so a hit can
// only ever replay what would have been stored.
//
//   - status in cacheable set: see ADR-122 §Decision cacheable
//     statuses (200/203/300/301/308/404/410). 302/303/307 are
//     deliberately NOT cached — they're the
//     method-preserving-temporarily-redirecting kind, and
//     caching a 302 would re-route a POST that the platform
//     only just re-routed.
//   - no Set-Cookie: a Set-Cookie on the response binds the
//     response to a single client session; caching it would
//     leak that session to other callers behind the cache.
//   - no Cache-Control: no-store / private: the app opted
//     out of caching; honour the opt-out. Other directives
//     (max-age, public) are advisory only — the cache TTL is
//     driven by the rule, not the upstream's max-age hint.
//   - !bypass: we captured the full body, not a partial.
//   - wroteBody: an empty 200 is still cacheable, but we
//     require at least one Write to commit — a request that
//     emitted status + headers + zero bytes is suspicious
//     (typically a HEAD-shaped response on a non-HEAD method)
//     and shouldn't bloat the cache.
func (c *cacheWriter) shouldStore() bool {
	if c.bypass {
		return false
	}
	if !c.wroteBody {
		return false
	}
	switch c.status {
	case 200, 203, 300, 301, 308, 404, 410:
	default:
		return false
	}
	if c.header.Get("Set-Cookie") != "" {
		return false
	}
	cc := c.header.Get("Cache-Control")
	if cc != "" {
		low := strings.ToLower(cc)
		if strings.Contains(low, "no-store") || strings.Contains(low, "private") {
			return false
		}
	}
	return true
}

// finishCacheCapture is the deferred hook from ServeHTTP that
// commits a captured body to the cache. Returns true if a Put
// fired (caller increments the appropriate metric in commit
// 15); false on a no-store decision (caller increments
// store_skipped).
//
// mustStore param is the matched rule + cache key + now() clock
// already resolved by ServeHTTP — passing them in keeps this
// helper free of h.* lookups, which would force a dependency on
// the full Handler just for the commit step.
func (c *cacheWriter) finishCacheCapture(cache *ResponseCache, key CacheKey, now time.Time) (stored bool) {
	if !c.shouldStore() {
		return false
	}
	// Compute the fresh + stale windows. MaxAgeSeconds is the
	// per-rule fresh window; StaleIfErrorSeconds is the
	// post-fresh window where stale-on-error can serve. A
	// rule with MaxAgeSeconds==0 disables fresh hits entirely
	// but still keeps the entry in the stale window — useful
	// for "always serve stale on failure, never on success"
	// deployments that want maximum reliability.
	maxAge := time.Duration(c.rule.MaxAgeSeconds) * time.Second
	stale := time.Duration(c.rule.StaleIfErrorSeconds) * time.Second
	cache.Put(key, c.status, c.header, c.buf.Bytes(), now.Add(maxAge), now.Add(maxAge+stale), c.ruleAction)
	return true
}
