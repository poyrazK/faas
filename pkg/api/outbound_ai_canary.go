package api

import "time"

// Model canary states and rollback reasons (ADR-1340).
const (
	OutboundAIRouteCanaryActive          = "active"
	OutboundAIRouteCanaryRolledBack      = "rolled_back"
	OutboundAIRouteCanaryReasonErrorRate = "error_rate"
	OutboundAIRouteCanaryReasonLatency   = "latency"
)

// OutboundAIRouteCanary sends a share of a route's calls to one target
// first and is rolled back when that target does worse than the route's
// first target (ADR-1340). Target is the 1-based position in the route.
type OutboundAIRouteCanary struct {
	Target  int `json:"target"`
	Percent int `json:"percent"`
	// MaxErrorRateIncrease is in percentage points.
	MaxErrorRateIncrease int `json:"max_error_rate_increase"`
	// MaxLatencyIncreasePercent compares mean time to first byte; null
	// judges the canary on errors only.
	MaxLatencyIncreasePercent *int       `json:"max_latency_increase_percent"`
	MinCalls                  int        `json:"min_calls"`
	WindowMinutes             int        `json:"window_minutes"`
	State                     string     `json:"state"`
	Reason                    *string    `json:"reason"`
	StartedAt                 time.Time  `json:"started_at"`
	DecidedAt                 *time.Time `json:"decided_at"`
}

// PutOutboundAIRouteCanaryRequest starts a canary, or replaces the route's
// canary and its counts. Omitted settings take their defaults.
type PutOutboundAIRouteCanaryRequest struct {
	Target                    int  `json:"target"`
	Percent                   int  `json:"percent"`
	MaxErrorRateIncrease      *int `json:"max_error_rate_increase,omitempty"`
	MaxLatencyIncreasePercent *int `json:"max_latency_increase_percent,omitempty"`
	MinCalls                  *int `json:"min_calls,omitempty"`
	WindowMinutes             *int `json:"window_minutes,omitempty"`
}

// Normalized fills omitted settings with their defaults.
func (r PutOutboundAIRouteCanaryRequest) Normalized() PutOutboundAIRouteCanaryRequest {
	r.MaxErrorRateIncrease = intOrDefault(r.MaxErrorRateIncrease, OutboundAIRouteCanaryErrorIncreaseDefault)
	r.MinCalls = intOrDefault(r.MinCalls, OutboundAIRouteCanaryMinCallsDefault)
	r.WindowMinutes = intOrDefault(r.WindowMinutes, OutboundAIRouteCanaryWindowMinutesDefault)
	return r
}

func intOrDefault(value *int, fallback int) *int {
	if value != nil {
		return value
	}
	return &fallback
}

// Valid checks a normalized request's ranges. The target must also name an
// existing position after the first, which only the stored route can tell.
func (r PutOutboundAIRouteCanaryRequest) Valid() bool {
	r = r.Normalized()
	latencyOK := r.MaxLatencyIncreasePercent == nil ||
		(*r.MaxLatencyIncreasePercent >= 1 && *r.MaxLatencyIncreasePercent <= OutboundAIRouteCanaryLatencyIncreaseMax)
	return r.Target >= 2 && r.Target <= OutboundAIRouteTargetsMax &&
		r.Percent >= 1 && r.Percent <= OutboundAIRouteCanaryPercentMax &&
		*r.MaxErrorRateIncrease >= 0 && *r.MaxErrorRateIncrease <= OutboundAIRouteCanaryErrorIncreaseMax && latencyOK &&
		*r.MinCalls >= OutboundAIRouteCanaryMinCallsMin && *r.MinCalls <= OutboundAIRouteCanaryMinCallsMax &&
		*r.WindowMinutes >= OutboundAIRouteCanaryWindowMinutesMin && *r.WindowMinutes <= OutboundAIRouteCanaryWindowMinutesMax
}

// OutboundAIRouteTargetStats is one target's settled attempts in a canary's
// window. Errors are attempts with a provider status of 400 or above;
// MeanTTFBMS averages successful attempts that reported a first byte.
type OutboundAIRouteTargetStats struct {
	Target         int     `json:"target"`
	Calls          int64   `json:"calls"`
	Errors         int64   `json:"errors"`
	ErrorRate      float64 `json:"error_rate"`
	MeanTTFBMS     *int64  `json:"mean_ttfb_ms"`
	CostMillicents int64   `json:"cost_millicents"`
	// MeanCostMillicents is the cost per successful attempt, or null
	// (ADR-1434).
	MeanCostMillicents *int64 `json:"mean_cost_millicents"`
}

// OutboundAIRouteStats is each target's settled attempts since WindowStart:
// the window a latency or cost route judges its targets over (ADR-1434).
// Rows are kept for latency and cost routes and during a canary.
type OutboundAIRouteStats struct {
	Strategy    string                       `json:"strategy"`
	WindowStart time.Time                    `json:"window_start"`
	Targets     []OutboundAIRouteTargetStats `json:"targets"`
}

// OutboundAIRouteCanaryStatus is a canary with both targets' counts since
// WindowStart: the later of the canary's start and its window.
type OutboundAIRouteCanaryStatus struct {
	Canary      OutboundAIRouteCanary      `json:"canary"`
	WindowStart time.Time                  `json:"window_start"`
	Baseline    OutboundAIRouteTargetStats `json:"baseline"`
	CanaryStats OutboundAIRouteTargetStats `json:"canary_stats"`
}
