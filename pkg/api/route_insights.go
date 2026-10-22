package api

import (
	"encoding/json"
	"net/http"
)

// Route insight kinds and states (ADR-1198). The cache_* kinds check existing
// cache rules from refill evidence (ADR-1199).
const (
	RouteInsightKeptAwake     = "kept_awake"
	RouteInsightCacheable     = "cacheable"
	RouteInsightCacheStale    = "cache_stale"
	RouteInsightCacheTooShort = "cache_too_short"

	RouteInsightActive   = "active"
	RouteInsightResolved = "resolved"
)

// Route insight fix actions (ADR-20261020161310), matching the route_insights
// fix_action CHECK.
const (
	// RouteInsightFixCreateCacheRule creates an enabled @app cache rule for
	// the route with the measured safe max-age.
	RouteInsightFixCreateCacheRule = "create_cache_rule"
	// RouteInsightFixSetCacheMaxAge changes the max-age of the cache rule
	// that serves exactly this route.
	RouteInsightFixSetCacheMaxAge = "set_cache_max_age"
	// RouteInsightFixAddCacheOverride adds a narrower cache rule for the
	// route ahead of a rule that also covers other routes.
	RouteInsightFixAddCacheOverride = "add_cache_override"
	// RouteInsightFixDisableCacheRule disables the cache rule that serves
	// exactly this route when no max-age was measured safe.
	RouteInsightFixDisableCacheRule = "disable_cache_rule"
)

// Problem codes of POST /v1/apps/{slug}/routes/insights/{id}/fix.
const (
	CodeRouteInsightNoFix      = "route_insight_no_fix"
	CodeRouteInsightFixChanged = "route_insight_fix_changed"
)

// ErrRouteInsightNoFix is the 409 for an insight that is resolved or has no
// fix the platform can apply.
func ErrRouteInsightNoFix(reason string) *Problem {
	return NewProblem(http.StatusConflict, CodeRouteInsightNoFix, "No fix to apply", reason)
}

// ErrRouteInsightFixChanged is the 409 for a fix_id that no longer matches
// the fix the insight would apply now.
func ErrRouteInsightFixChanged() *Problem {
	return NewProblem(http.StatusConflict, CodeRouteInsightFixChanged, "The fix changed",
		"the insight or the app's edge rules changed since the fix was read; list the route insights again and review the current fix")
}

// RouteInsightsResponse is GET /v1/apps/{slug}/routes/insights: the app's
// active route insights, then those resolved in the last
// RouteInsightResolvedListWindow.
type RouteInsightsResponse struct {
	Slug     string         `json:"slug"`
	Insights []RouteInsight `json:"insights"`
}

// RouteInsight is one finding apid's evaluator recorded for a route:
// kept_awake when a client keeps the app from parking, cacheable when the
// gateways measured the route safe to cache. The same object is the payload
// of the routes.insight.detected and routes.insight.resolved webhooks.
type RouteInsight struct {
	ID              string          `json:"id"`
	Kind            string          `json:"kind"`
	Method          string          `json:"method"`
	Route           string          `json:"route"`
	State           string          `json:"state"`
	Message         string          `json:"message"`
	Evidence        json.RawMessage `json:"evidence"`
	FirstDetectedAt string          `json:"first_detected_at"`
	LastDetectedAt  string          `json:"last_detected_at"`
	ResolvedAt      string          `json:"resolved_at,omitempty"`
	// FixedAt and FixAction are set when an applied fix resolved the
	// insight (ADR-20261020161310).
	FixedAt   string `json:"fixed_at,omitempty"`
	FixAction string `json:"fix_action,omitempty"`
	// Fix is the change applying this insight would make now, planned from
	// its evidence and the app's current edge rules. Webhook payloads carry
	// no fix; read it from the list.
	Fix *RouteInsightFix `json:"fix,omitempty"`
}

// RouteInsightFix is one planned fix. ID covers every value the fix would
// write, so a caller that sends it back applies exactly what it reviewed.
type RouteInsightFix struct {
	ID            string `json:"id"`
	Action        string `json:"action"`
	Summary       string `json:"summary"`
	MaxAgeSeconds int    `json:"max_age_seconds,omitempty"`
	// RuleID is the cache rule the fix changes or overrides.
	RuleID string `json:"rule_id,omitempty"`
	// PurgeCache reports that the fix also purges the route's cached
	// responses.
	PurgeCache bool `json:"purge_cache,omitempty"`
}

// RouteInsightFixRequest is the body of
// POST /v1/apps/{slug}/routes/insights/{id}/fix.
type RouteInsightFixRequest struct {
	FixID string `json:"fix_id"`
}

// Cache purge outcomes of an applied fix.
const (
	RouteInsightPurgeRequested = "requested"
	RouteInsightPurgeFailed    = "failed"
)

// RouteInsightFixResponse reports an applied fix: the edge rule it created or
// changed, the cache purge it requested, and how many of the route's
// insights it resolved.
type RouteInsightFixResponse struct {
	Insight    RouteInsight     `json:"insight"`
	Fix        RouteInsightFix  `json:"fix"`
	Rule       EdgeRuleResponse `json:"rule"`
	CachePurge string           `json:"cache_purge,omitempty"`
	Resolved   int64            `json:"resolved"`
}
