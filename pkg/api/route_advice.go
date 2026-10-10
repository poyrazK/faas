package api

// Route advice kinds (ADR-955). Each maps to the edge-rule kind it proposes.
const (
	RouteAdviceKindCache    = "cache"
	RouteAdviceKindAsync    = "async"
	RouteAdviceKindThrottle = "throttle"
)

// RouteAdviceResponse is GET /v1/apps/{slug}/routes/advice: edge-rule
// suggestions derived from retained request telemetry. Nothing is applied;
// every proposed rule is created disabled when the customer applies it.
type RouteAdviceResponse struct {
	Slug               string                  `json:"slug"`
	From               string                  `json:"from"`
	Until              string                  `json:"until"`
	WindowClamped      bool                    `json:"window_clamped,omitempty"`
	CacheMaxAgeSeconds int                     `json:"cache_max_age_seconds"`
	RoutesAnalyzed     int                     `json:"routes_analyzed"`
	Suggestions        []RouteAdviceSuggestion `json:"suggestions"`
}

// RouteAdviceSuggestion is one proposed edge rule for one observed route.
// ID is stable for the same kind, method and route, so a CLI can apply a
// suggestion it listed earlier.
type RouteAdviceSuggestion struct {
	ID        string                  `json:"id"`
	Kind      string                  `json:"kind"`
	Method    string                  `json:"method"`
	Route     string                  `json:"route"`
	Title     string                  `json:"title"`
	Rationale string                  `json:"rationale"`
	Evidence  RouteAdviceEvidence     `json:"evidence"`
	Impact    RouteAdviceImpact       `json:"impact"`
	Cautions  []string                `json:"cautions,omitempty"`
	Rules     []CreateEdgeRuleRequest `json:"rules"`
}

// RouteAdviceEvidence is the observed traffic a suggestion rests on.
type RouteAdviceEvidence struct {
	Requests                 int64  `json:"requests"`
	AnonymousRequests        int64  `json:"anonymous_requests"`
	ServerErrors             int64  `json:"server_errors"`
	Timeouts                 int64  `json:"timeouts"`
	ColdBoots                int64  `json:"cold_boots"`
	P95LatencyMs             int    `json:"p95_latency_ms"`
	Consumers                int64  `json:"consumers,omitempty"`
	TopConsumerID            string `json:"top_consumer_id,omitempty"`
	TopConsumerRequests      int64  `json:"top_consumer_requests,omitempty"`
	TopConsumerPeakPerMinute int64  `json:"top_consumer_peak_per_minute,omitempty"`
	NextConsumerPeakPerMin   int64  `json:"next_consumer_peak_per_minute,omitempty"`
}

// RouteAdviceImpact is the what-if estimate over the same window. Estimates
// are replays of aggregated telemetry, not guarantees.
type RouteAdviceImpact struct {
	Summary                    string `json:"summary"`
	EstimatedCacheHits         int64  `json:"estimated_cache_hits"`
	EstimatedWakesAvoided      int64  `json:"estimated_wakes_avoided"`
	EstimatedThrottledRequests int64  `json:"estimated_throttled_requests"`
	TimeoutsAffected           int64  `json:"timeouts_affected"`
}
