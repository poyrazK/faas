package api

import (
	"context"
	"net/url"
)

// GetServiceMap returns the account service map (ADR-1131). An empty rng
// selects the server default (ServiceMapDefaultRange).
func (c *Client) GetServiceMap(ctx context.Context, rng string) (ServiceMapResponse, error) {
	var out ServiceMapResponse
	path := "/v1/service-map"
	if rng != "" {
		q := url.Values{}
		q.Set("range", rng)
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ServiceMapResponse is GET /v1/service-map (ADR-1131): the account's
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
	// WakeAheadEnabled is the app's ADR-977 opt-in to restore the services it
	// calls while it wakes.
	WakeAheadEnabled bool `json:"wake_ahead_enabled"`
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
	// ColdCalls are the calls that found the target parked and waited for it
	// to restore (ADR-196); ColdCallPct is their share of Calls.
	ColdCalls   int64   `json:"cold_calls"`
	ColdCallPct float64 `json:"cold_call_pct"`
	// SuggestWakeAhead marks an edge where the caller has not opted into
	// wake-ahead (ADR-977) and its calls mostly wake the target: at least
	// ServiceWakeAheadMinWakes cold calls in the window, making up at least
	// ServiceWakeAheadMinColdShare of the edge's calls. Private TCP sessions
	// count as calls (ADR-1134).
	SuggestWakeAhead bool `json:"suggest_wake_ahead"`
	// WakeAheadStarted counts the ADR-977 wake-aheads the caller's wakes
	// started for this target in the window; WakeAheadUsed counts those the
	// target was called on while fresh. Both are zero when wake-ahead is off.
	WakeAheadStarted int64 `json:"wake_ahead_started"`
	WakeAheadUsed    int64 `json:"wake_ahead_used"`
	// TCPSessions counts the caller's private TCP sessions to the target
	// (ADR-576) in the window, TCPErrors those that failed after
	// authorization, and TCPColdSessions those that had to wake the target.
	// An edge with only TCP traffic has zero Calls and zero latency.
	TCPSessions     int64 `json:"tcp_sessions"`
	TCPErrors       int64 `json:"tcp_errors"`
	TCPColdSessions int64 `json:"tcp_cold_sessions"`
}
