package debugger

import "math"

// Route regression kinds stored on debug_regression_observations.kind.
const (
	RegressionKindLatency          = "latency"
	RegressionKindErrors           = "errors"
	RegressionKindLatencyAndErrors = "latency_and_errors"
)

// Error-rate regression thresholds (ADR-127 amendment). Both windows need
// enough represented requests for a rate to mean something; the current side
// needs enough failed requests that a handful of errors cannot open an
// observation; and the server-error share must rise by at least 5
// percentage points and at least double.
const (
	RegressionErrorMinRequests = int64(50)
	RegressionErrorMinErrors   = int64(10)
	RegressionErrorMinDeltaPct = 5.0
	RegressionErrorMinFactor   = 2.0
)

// RouteErrors is one side of a route's error-rate comparison: represented
// requests and how many of them ended with an HTTP 5xx.
type RouteErrors struct {
	Requests int64
	Errors   int64
}

// RatePct is the server-error share in percent, rounded to two decimals.
func (r RouteErrors) RatePct() float64 {
	if r.Requests <= 0 || r.Errors <= 0 {
		return 0
	}
	errors := r.Errors
	if errors > r.Requests {
		errors = r.Requests
	}
	return math.Round(float64(errors)*10000/float64(r.Requests)) / 100
}

// ErrorRateRegressed reports whether the current deployment fails a route
// materially more often than the previous one.
func ErrorRateRegressed(baseline, current RouteErrors) bool {
	if baseline.Requests < RegressionErrorMinRequests || current.Requests < RegressionErrorMinRequests ||
		current.Errors < RegressionErrorMinErrors {
		return false
	}
	baseRate, currentRate := baseline.RatePct(), current.RatePct()
	if currentRate-baseRate < RegressionErrorMinDeltaPct {
		return false
	}
	return baseRate == 0 || currentRate >= baseRate*RegressionErrorMinFactor
}

// RegressionKind combines the latency and error-rate signals; "" means the
// route did not regress.
func RegressionKind(latency, errors bool) string {
	switch {
	case latency && errors:
		return RegressionKindLatencyAndErrors
	case latency:
		return RegressionKindLatency
	case errors:
		return RegressionKindErrors
	default:
		return ""
	}
}

// RegressionHasErrors reports whether a stored kind includes the error-rate
// signal.
func RegressionHasErrors(kind string) bool {
	return kind == RegressionKindErrors || kind == RegressionKindLatencyAndErrors
}

// RegressionHasLatency reports whether a stored kind includes the latency
// signal. An empty kind predates error-rate detection and is latency.
func RegressionHasLatency(kind string) bool {
	return kind == "" || kind == RegressionKindLatency || kind == RegressionKindLatencyAndErrors
}
