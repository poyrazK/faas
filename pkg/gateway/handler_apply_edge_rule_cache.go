package gateway

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/wire"
)

// applyEdgeRuleCache (ADR-122 §Decision) is the kind=cache serve
// path. Returns true when the request was served from the cache
// (the caller MUST stop processing — no wake gate, no auth, no
// metrics increment beyond the cache outcome counter) and false
// on a miss (the caller falls through to the wake gate as
// before). The third return is the matched rule (nil when no
// rule matched), reused by the store path so we don't run the
// matcher twice on the same request.
//
// Placement invariant: this function is called AFTER
// enforceRequireAuthn + enforcePublicAuth (so a cache hit cannot
// bypass the auth gate) and BEFORE the wake gate (so a hit
// returns without calling h.gate.Wait — no VM, no
// gb_ram_hour). The call site is handler.go:4366.
//
// Cacheability predicate (deny-by-default; matches
// ResponseCache.Put + cacheWriter.Store below):
//
//   - matched rule MUST exist (no rule → no cache hit/miss
//     decision → fall through)
//   - request method MUST be in {GET, HEAD} (the rule's
//     closed cacheable-method vocab)
//   - request MUST NOT carry Authorization header (ADR-122 D3:
//     authed requests are a hard bypass)
//   - request MUST NOT carry a session cookie (same posture;
//     credentials of any kind bypass)
//   - cache key MUST find a fresh entry (within max_age_seconds);
//     stale-on-error is handled by applyEdgeRuleCacheStaleOnError
//     in commit 13 and is a wake-failure-only path
//
// On a fresh hit the function writes status + headers + body
// verbatim from the entry, calls observe() with the route's
// hit metric, and returns true. The applier is the only
// writer of the cache outcome counter for the "hit" outcome;
// commit 15 wires the other outcomes.
func (h *Handler) applyEdgeRuleCache(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder) (bool, *EdgeRuleCacheResolved) {
	if h == nil || h.responseCache == nil || h.edgeRules == nil {
		return false, nil
	}
	// Method gate is a cheap pre-flight: only {GET, HEAD} are
	// cacheable per ADR-122 D3. We DO NOT count this as
	// bypass_uncacheable here — that label only fires when a
	// cache rule actually matched the path AND the verb is
	// outside {GET, HEAD}, otherwise every POST-heavy app with
	// no cache rule would inflate the counter (the
	// counter's load-bearing meaning is "rule matched, wrong
	// verb mix").
	method := strings.ToUpper(r.Method)
	if method != "GET" && method != "HEAD" {
		return false, nil
	}
	host := r.Host
	if host == "" {
		host = app.Slug // fallback for unix-socket routing
	}
	path := r.URL.Path
	rule := h.edgeRules.MatchCache(r.Context(), host, path, method)
	if rule == nil {
		return false, nil
	}
	// Apply the rule's method filter as a defence-in-depth pass:
	// the matcher already filtered by methods, but a rule with
	// Methods=nil matches anything and we still want to honour
	// the closed cacheable-method vocab before consulting the
	// cache.
	if rule.Methods != nil && !rule.Methods[method] {
		h.metricsIncCacheOutcome("bypass_uncacheable")
		return false, nil
	}
	// Pre-flight on credentialed requests — runs AFTER the
	// matcher so bypass_authed means "a cache rule matched this
	// path AND the request carried credentials", which is the
	// informative signal the dashboard surfaces. The security
	// property (authed requests are NEVER cached) is enforced
	// by the absence of a storage path here, not by the counter.
	if r.Header.Get("Authorization") != "" || hasSessionCookie(r) {
		h.metricsIncCacheOutcome("bypass_authed")
		return false, nil
	}
	// Build the cache key. DeploymentID is empty in v1 because
	// the App value type doesn't carry one — plumbed in a
	// follow-on commit once applyEdgeRuleCache is wired into
	// the picker path. Without per-deployment binding, a
	// deploy bumps via InvalidateByApp in the same
	// NotifyAppChanged hook (commit 14) — slightly coarser
	// (whole app flush) but safe.
	key := CacheKey{
		AppID:          app.ID,
		DeploymentID:   "",
		RuleID:         rule.ID,
		Method:         method,
		NormalizedPath: path,
		Query:          sortQuery(r.URL.RawQuery),
		VaryHash:       computeVaryHash(r, rule.VaryOn),
	}
	outcome, entry := h.responseCache.Get(key)
	switch outcome {
	case "fresh":
		// Hit. Replay the stored response verbatim.
		// StatusCode + headers + body were captured at Put time
		// by cacheWriter. We do NOT add Vary: Accept-Language
		// etc. on the response — the cache key already
		// partitions by those values, so a downstream
		// intermediary that respects Vary would re-key the
		// response correctly without the platform emitting
		// Vary. Operators that need Vary on cached responses
		// can add it via kind=headers.
		h.metricsIncCacheOutcome("hit")
		w.Header().Del(wire.WakeHeader)
		for k, vs := range entry.header {
			// Skip hop-by-hop headers that don't survive
			// into the stored body anyway; mirroring
			// cacheWriter's drop-list to keep behaviour
			// symmetric.
			if isHopByHopHeader(k) || isPerRequestPlatformHeader(k) {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.Header().Set("Content-Length", itoaLen(entry.body))
		w.WriteHeader(entry.statusCode)
		_, _ = w.Write(entry.body)
		rec.status = entry.statusCode
		rec.Bytes = int64(len(entry.body))
		// Cache hit is a saved wake. Count the metric so the
		// dashboard reflects "wake-elision". The wakes_avoided
		// counter is gated on HealthyCount == 0 — only a hit
		// against a cold app genuinely displaced a wake; a
		// hit against an already-warm app saves latency but
		// no compute, and is NOT counted as savings.
		if h.metrics != nil {
			if h.backend != nil && h.backend.HealthyCount(app.ID) == 0 {
				h.metrics.responseCacheWakesAvoided.WithLabelValues(app.ID).Inc()
			}
		}
		// Cache hit is a saved wake. Count the metric so the
		// dashboard reflects "wake-elision". Commit 15 wires
		// the wakes_avoided counter + the healthy-check that
		// gates it; for now we just call observe with the
		// cached status.
		h.observe(r, entry.statusCode, app.ID, string(app.Plan), false, Target{})
		return true, rule
	case "stale_if_error_eligible":
		// Past fresh, inside stale_if_error window. Per
		// ADR-122 D5: stale serves ONLY on origin failure
		// (wake gate failure or upstream 5xx/timeout), never
		// on a normal miss. The applier here is the SERVE
		// path — the wake gate hasn't run yet — so a stale
		// entry is NOT served. The caller falls through to
		// the wake gate as if the entry were absent. If the
		// wake gate fails, the stale path is the
		// responsibility of commit 13's
		// applyEdgeRuleCacheStaleOnError wrapper (called
		// from the gate-failure branch).
		h.metricsIncCacheOutcome("miss")
		return false, rule
	case "":
		// Miss. Fall through to the wake gate.
		h.metricsIncCacheOutcome("miss")
		return false, rule
	}
	h.metricsIncCacheOutcome("miss")
	return false, rule
}

// metricsIncCacheOutcome (ADR-122 §Decision) is the small
// helper the applier + writer use to bump a closed-set
// outcome label. nil-safe: a handler with no metrics (the
// pre-metrics test corpus) is a no-op.
func (h *Handler) metricsIncCacheOutcome(outcome string) {
	if h == nil || h.metrics == nil {
		return
	}
	h.metrics.responseCache.WithLabelValues(outcome).Inc()
}

// hasSessionCookie reports whether the request carries a session
// cookie. The set of cookie names treated as "session" is the
// platform's auth-cookie vocabulary (the same set the authn
// middleware recognises). An empty Set-Cookie in any name means
// authed → bypass.
func hasSessionCookie(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, c := range r.Cookies() {
		// Conservative: any cookie is treated as a session
		// for the v1 cache. Future ADRs can introduce a
		// non-session allowlist (analytics cookies etc.)
		// without changing the store; the predicate is the
		// single chokepoint.
		if c.Name != "" {
			return true
		}
	}
	return false
}

// isHopByHopHeader reports whether k is a hop-by-hop header
// (RFC 7230 §6.1) that the cacheWriter drops on capture and
// the applyEdgeRuleCache replay must also skip. Without this
// symmetry, a stored Connection: close would tell the client
// to close after the cached response — incorrect on a fresh
// upstream connection the platform just opened.
func isHopByHopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

// itoaLen is a small helper for setting Content-Length without
// importing strconv just for one call site. Negative bodies
// are clamped to 0 — the cache never stores nil bodies, but
// the replay path is defensive against a buggy store.
func itoaLen(b []byte) string {
	n := len(b)
	if n < 0 {
		n = 0
	}
	// strconv.Itoa for clarity; the import already lands
	// elsewhere in the package so the dep cost is zero.
	return strconvItoa(n)
}
