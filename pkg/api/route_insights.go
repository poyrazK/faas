package api

import "encoding/json"

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
	Kind            string          `json:"kind"`
	Method          string          `json:"method"`
	Route           string          `json:"route"`
	State           string          `json:"state"`
	Message         string          `json:"message"`
	Evidence        json.RawMessage `json:"evidence"`
	FirstDetectedAt string          `json:"first_detected_at"`
	LastDetectedAt  string          `json:"last_detected_at"`
	ResolvedAt      string          `json:"resolved_at,omitempty"`
}
