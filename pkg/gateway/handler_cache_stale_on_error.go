package gateway

import (
	"context"
	"net/http"
)

// cacheRuleContextKey is the unexported context-key type the
// kind=cache applier uses to stash the matched rule on the
// request ctx, so the wake-failure branch further down in
// ServeHTTP can attempt a stale-on-error serve without
// re-running the matcher.
//
// ADR-122 §Decision: keep the cache-rule lookup count to ONE per
// request. The applier runs the matcher once at the top of
// ServeHTTP; the wake-failure wrapper consults the same matched
// rule via ctx, not via a second MatchCache call. A second
// MatchCache would be safe (the matcher is idempotent) but
// wasteful, and the bug class we're defending against here is
// "two requests for the same path produce different rule
// matches between the cache consult and the wake-failure
// consult" — using the same rule snapshot across both is
// strictly safer.
type cacheRuleContextKey struct{}

// cacheRuleSnapshot is the per-request bundle the wake-failure
// branch needs to attempt a stale-on-error serve. It captures
// the matched rule + the cache key components that were
// resolved at the top of ServeHTTP.
type cacheRuleSnapshot struct {
	Rule     *EdgeRuleCacheResolved
	AppID    string
	Method   string
	Path     string
	Query    string
	VaryHash [32]byte
}

// withCacheRuleContext stashes the snapshot on ctx.
func withCacheRuleContext(ctx context.Context, rule *EdgeRuleCacheResolved, appID, method, path, query string, varyHash [32]byte) context.Context {
	if rule == nil {
		return ctx
	}
	return context.WithValue(ctx, cacheRuleContextKey{}, &cacheRuleSnapshot{
		Rule: rule, AppID: appID, Method: method, Path: path, Query: query, VaryHash: varyHash,
	})
}

// cacheRuleFromContext returns the snapshot or nil if no rule
// was matched on this request.
func cacheRuleFromContext(ctx context.Context) *cacheRuleSnapshot {
	v, _ := ctx.Value(cacheRuleContextKey{}).(*cacheRuleSnapshot)
	return v
}

// tryServeStaleWhileWaking serves an eligible stale response to a request
// that arrives while another request is already driving the app's wake gate.
// The wake leader continues to the newly ready instance and its cache writer
// refreshes this route; followers get a bounded stale response instead of
// waiting behind the restore.
func (h *Handler) tryServeStaleWhileWaking(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder) (bool, string) {
	if h == nil || h.responseCache == nil {
		return false, ""
	}
	snap := cacheRuleFromContext(r.Context())
	if snap == nil || snap.Rule == nil || snap.Rule.StaleIfErrorSeconds <= 0 {
		return false, ""
	}
	if h.gate == nil || !h.gate.Inflight(snap.AppID) {
		return false, ""
	}
	if r.Header.Get("Authorization") != "" || hasSessionCookie(r) {
		return false, ""
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		return false, ""
	}
	key := CacheKey{
		AppID:          snap.AppID,
		DeploymentID:   "",
		RuleID:         snap.Rule.ID,
		Method:         snap.Method,
		NormalizedPath: snap.Path,
		Query:          snap.Query,
		VaryHash:       snap.VaryHash,
	}
	outcome, entry := h.responseCache.Get(key)
	if outcome != "stale_if_error_eligible" {
		return false, ""
	}
	for k, vs := range entry.header {
		if isHopByHopHeader(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("x-faas-cache", "stale-while-waking")
	w.Header().Add("Warning", `110 - "Response is Stale"`)
	w.Header().Set("X-From-Cache", "stale")
	w.Header().Set("Content-Length", strconvItoa(len(entry.body)))
	w.WriteHeader(entry.statusCode)
	_, _ = w.Write(entry.body)
	rec.status = entry.statusCode
	rec.Bytes = int64(len(entry.body))
	if h.metrics != nil {
		h.metrics.cacheStaleWhileWaking.WithLabelValues(app.ID).Inc()
	}
	h.observe(r, entry.statusCode, app.ID, string(app.Plan), false, Target{})
	return true, "stale_while_waking"
}

// tryServeStaleOnWakeError (ADR-122 §Decision, kind=cache
// stale-on-error path) is called from ServeHTTP's wake-failure
// branch (handler.go:4531) BEFORE writeWakeError. Returns true
// if a stale entry was served (caller MUST skip writeWakeError
// + the capacity metrics increment); false on no-op.
//
// Predicates:
//
//   - h.responseCache is wired (else no cache to consult)
//   - The matched rule (if any) has StaleIfErrorSeconds > 0
//     — a rule with StaleIfErrorSeconds==0 disables stale-on-
//     error entirely (consistent with the runtime posture at
//     edge_rules_cache.go:30-33)
//   - The cache has a stale entry for this request (past
//     fresh, inside stale)
//   - The request itself passes the same auth-bypass +
//     method-vocab predicate the applier uses — an authed
//     request must NEVER be served stale even on origin
//     failure (the original served it uncached, and the
//     user-visible semantics must not silently change).
//
// On serve, the function stamps the response with:
//
//   - status code + body verbatim from the stored entry
//   - header "Warning: 110 - \"Response is Stale\"" per
//     RFC 7234 §5.5.2 — clients with stale-aware caching
//     (CDNs, SDKs) can opt to revalidate on the next hop
//   - header "X-From-Cache: stale" — platform-internal
//     debugging surface, ACTUALLY emitted at line 137 below
//     (the docstring is the contract; a regression that
//     drops the header would silently break operator
//     dashboards that grep for it)
//
// Returns the outcome string for metrics: "stale_if_error_served"
// or "" on no-op. The metric increment happens in commit 15;
// this helper only writes the response bytes.
func (h *Handler) tryServeStaleOnWakeError(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder) (bool, string) {
	if h == nil || h.responseCache == nil {
		return false, ""
	}
	snap := cacheRuleFromContext(r.Context())
	if snap == nil || snap.Rule == nil {
		return false, ""
	}
	if snap.Rule.StaleIfErrorSeconds <= 0 {
		return false, ""
	}
	// Re-check the auth-bypass predicate at the wake-failure
	// branch. The applier already enforced this at the top of
	// ServeHTTP; the check is duplicated here so the security
	// posture is the single chokepoint, not a stateful
	// contract between two call sites.
	if r.Header.Get("Authorization") != "" || hasSessionCookie(r) {
		return false, ""
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		return false, ""
	}
	key := CacheKey{
		AppID:          snap.AppID,
		DeploymentID:   "",
		RuleID:         snap.Rule.ID,
		Method:         snap.Method,
		NormalizedPath: snap.Path,
		Query:          snap.Query,
		VaryHash:       snap.VaryHash,
	}
	outcome, entry := h.responseCache.Get(key)
	if outcome != "stale_if_error_eligible" {
		return false, ""
	}
	// Stale-eligible → serve. The replay is structurally
	// identical to the fresh-hit path in
	// handler_apply_edge_rule_cache.go; the only differences
	// are the Warning header and the metrics outcome string.
	for k, vs := range entry.header {
		if isHopByHopHeader(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Add("Warning", `110 - "Response is Stale"`)
	// X-From-Cache: stale is the platform-internal debug surface
	// the docstring above promises. Operator dashboards grep
	// for it; a regression that drops this header would
	// silently break stale-serve alerting. Not part of the
	// public API contract.
	w.Header().Set("X-From-Cache", "stale")
	w.Header().Set("Content-Length", strconvItoa(len(entry.body)))
	w.WriteHeader(entry.statusCode)
	_, _ = w.Write(entry.body)
	rec.status = entry.statusCode
	rec.Bytes = int64(len(entry.body))
	// ADR-122 §Decision: stale_if_error_served is its own
	// outcome label so the hit-rate numerator cannot be
	// inflated by stale serves. The dashboard surfaces
	// stale_if_error_served separately so operators see
	// when their cache is being relied on as a fallback
	// rather than as a primary serve path.
	h.metricsIncCacheOutcome("stale_if_error_served")
	h.observe(r, entry.statusCode, app.ID, string(app.Plan), false, Target{})
	return true, "stale_if_error_served"
}
