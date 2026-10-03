package api

import "time"

// RouteHealthEvaluationPolicy preserves the thresholds used for a saved decision.
type RouteHealthEvaluationPolicy struct {
	Version             int     `json:"version"`
	Windows             int     `json:"windows"`
	WindowSeconds       int64   `json:"window_seconds"`
	IngestionLagSeconds int64   `json:"ingestion_lag_seconds"`
	MinimumRequests     int64   `json:"minimum_requests"`
	MinimumErrors       int64   `json:"minimum_errors"`
	ErrorRateFloor      float64 `json:"error_rate_floor"`
	ErrorRateDelta      float64 `json:"error_rate_delta"`
	ErrorRateFactor     float64 `json:"error_rate_factor"`
	MinLatencyRequests  int64   `json:"minimum_latency_requests"`
	LatencyQuantile     float64 `json:"latency_quantile"`
	LatencyFactor       float64 `json:"latency_factor"`
	LatencyDeltaMS      float64 `json:"latency_delta_ms"`
	ComparisonEpsilon   float64 `json:"comparison_epsilon"`
}

// RouteHealthHistoryEntry is an immutable snapshot of a real advance decision.
// Identical retries within an observation window reuse the original entry.
type RouteHealthHistoryEntry struct {
	// Purpose is abort for committed automatic recovery; omitted for advance evaluations.
	Purpose                 string                      `json:"purpose,omitempty"`
	Version                 int                         `json:"version"`
	ID                      string                      `json:"id"`
	CheckedAt               time.Time                   `json:"checked_at"`
	Source                  string                      `json:"source"`
	TrafficPercent          int                         `json:"traffic_percent"`
	RequestedTrafficPercent int                         `json:"requested_traffic_percent"`
	Policy                  RouteHealthEvaluationPolicy `json:"policy"`
	Decision                RouteHealthDecision         `json:"decision"`
	Report                  RouteHealthReport           `json:"report"`
}

type RouteHealthHistoryPage struct {
	AppID        string                    `json:"app_id"`
	DeploymentID string                    `json:"deployment_id"`
	Entries      []RouteHealthHistoryEntry `json:"entries"`
	NextCursor   string                    `json:"next_cursor,omitempty"`
}
