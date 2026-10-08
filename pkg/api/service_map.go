package api

// ServiceMapResponse is GET /v1/service-map (ADR-732): the account's
// caller → target app edges observed by the internal service proxy over
// Range. Source and AsOf follow the /v1/apps/metrics contract; a degraded
// Source carries nil Nodes and Edges rather than a partial map.
type ServiceMapResponse struct {
	Range  string `json:"range"`
	Source string `json:"source"`
	AsOf   string `json:"as_of"`
	// Nodes are the apps that appear on at least one returned edge, sorted
	// by slug. Apps without service traffic are omitted.
	Nodes []ServiceMapNode `json:"nodes"`
	// Edges are sorted by Calls descending and capped at ServiceMapMaxEdges.
	Edges []ServiceMapEdge `json:"edges"`
	// Truncated reports that the cap dropped lower-volume edges.
	Truncated bool `json:"truncated"`
}

// ServiceMapNode is one app on the service map.
type ServiceMapNode struct {
	AppID   string `json:"app_id"`
	AppSlug string `json:"app_slug"`
}

// ServiceMapEdge is the unsampled call record for one caller → target pair.
// Errors are final 5xx responses after proxy retries; identity and
// authorization failures are never counted. Latency percentiles cover
// successful calls only and include routing, wake, and forwarding time.
type ServiceMapEdge struct {
	CallerAppID   string  `json:"caller_app_id"`
	CallerAppSlug string  `json:"caller_app_slug"`
	TargetAppID   string  `json:"target_app_id"`
	TargetAppSlug string  `json:"target_app_slug"`
	Calls         int64   `json:"calls"`
	Errors        int64   `json:"errors"`
	ErrorRatePct  float64 `json:"error_rate_pct"`
	LatencyP50MS  float64 `json:"latency_p50_ms"`
	LatencyP95MS  float64 `json:"latency_p95_ms"`
}
