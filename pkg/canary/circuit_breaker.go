package canary

import (
	"fmt"
	"math"
)

const (
	// CircuitBreakerMinRequests keeps a handful of early requests from
	// deciding the fate of a rollout. Both the candidate and its stable
	// predecessor must meet this floor before percentage and latency signals
	// are compared.
	CircuitBreakerMinRequests int64 = 20
	// Cold boots are rarer than ordinary requests. Require a separate sample
	// floor before cold-boot request latency can abort a rollout.
	CircuitBreakerMinColdBootRequests int64 = 5
	// CPU-per-request is sampled at a coarser cadence than gateway request
	// telemetry. Reuse the normal request floor so one short sample cannot
	// decide a rollout.
	CircuitBreakerMinCPURequests int64 = 20
	// Managed service dependencies are less frequent than inbound requests, so
	// require a smaller but still useful denominator before comparing rates.
	CircuitBreakerMinDependencyCalls int64 = 10

	circuitBreakerErrorRateFloorPct       = 5.0
	circuitBreakerErrorRateDeltaPct       = 5.0
	circuitBreakerErrorRateFactor         = 3.0
	circuitBreakerLatencyFactor           = 2.0
	circuitBreakerLatencyDeltaMS          = 100.0
	circuitBreakerColdLatencyFactor       = 2.0
	circuitBreakerColdLatencyDeltaMS      = 250.0
	circuitBreakerCPUPerRequestFactor     = 3.0
	circuitBreakerCPUPerRequestDeltaUsec  = 10_000.0
	circuitBreakerDependencyErrorFloorPct = 20.0
	circuitBreakerDependencyErrorDeltaPct = 10.0
	circuitBreakerDependencyErrorFactor   = 3.0
)

type CircuitBreakerAction string

const (
	CircuitBreakerAdvance CircuitBreakerAction = "advance"
	CircuitBreakerHold    CircuitBreakerAction = "hold"
	CircuitBreakerAbort   CircuitBreakerAction = "abort"
)

// HealthWindow is the request-level health summary for one deployment over
// the same observation interval. Request counts include the telemetry
// publisher's collapsed-row weight.
type HealthWindow struct {
	Requests             int64
	ServerErrors         int64
	P95LatencyMS         float64
	ColdBootRequests     int64
	ColdBootP95LatencyMS float64
	CPURequests          int64
	CPUUsec              int64
	DependencyCalls      int64
	DependencyErrors     int64
}

// CircuitBreakerObservation compares the candidate with its currently
// serving predecessor and carries the independent per-deployment OOM signal.
type CircuitBreakerObservation struct {
	Candidate                 HealthWindow
	Stable                    HealthWindow
	StableDeploymentID        string
	HasStable                 bool
	OOMKills                  float64
	OOMSignalAvailable        bool
	CPURequestSignalAvailable bool
	DependencySignalAvailable bool
}

// CircuitBreakerDecision is a compact result used by the progression loop.
// Reason contains only metric names and aggregates; request payloads, routes,
// and trace identifiers never enter the audit trail.
type CircuitBreakerDecision struct {
	Action CircuitBreakerAction
	Reason string
}

// EvaluateCircuitBreaker applies Gregale's built-in first-pass policy.
// Unavailable signals and small samples hold a canary at its current stage;
// a clear candidate regression aborts it.
func EvaluateCircuitBreaker(o CircuitBreakerObservation) CircuitBreakerDecision {
	if !o.HasStable {
		// There is no successful revision to compare against or restore on a
		// first deployment. Keep its existing readiness and smoke gates.
		return CircuitBreakerDecision{Action: CircuitBreakerAdvance, Reason: "no stable predecessor"}
	}
	if o.OOMKills > 0 {
		return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: "workload OOM kill detected"}
	}
	if !o.OOMSignalAvailable {
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "workload OOM signal unavailable"}
	}
	if o.Candidate.Requests < CircuitBreakerMinRequests || o.Stable.Requests < CircuitBreakerMinRequests {
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: fmt.Sprintf(
			"insufficient request samples (candidate=%d stable=%d; need %d each)",
			o.Candidate.Requests, o.Stable.Requests, CircuitBreakerMinRequests)}
	}
	candidateErrorPct := 100 * float64(o.Candidate.ServerErrors) / float64(o.Candidate.Requests)
	stableErrorPct := 100 * float64(o.Stable.ServerErrors) / float64(o.Stable.Requests)
	errorThreshold := math.Max(circuitBreakerErrorRateFloorPct, stableErrorPct*circuitBreakerErrorRateFactor)
	if o.Candidate.ServerErrors >= 2 &&
		candidateErrorPct >= errorThreshold &&
		candidateErrorPct >= stableErrorPct+circuitBreakerErrorRateDeltaPct {
		return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
			"5xx regression (candidate %.1f%%, stable %.1f%%; %d/%d vs %d/%d requests)",
			candidateErrorPct, stableErrorPct,
			o.Candidate.ServerErrors, o.Candidate.Requests,
			o.Stable.ServerErrors, o.Stable.Requests)}
	}

	if o.Candidate.P95LatencyMS >= o.Stable.P95LatencyMS*circuitBreakerLatencyFactor &&
		o.Candidate.P95LatencyMS-o.Stable.P95LatencyMS >= circuitBreakerLatencyDeltaMS {
		return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
			"p95 latency regression (candidate %.0fms, stable %.0fms)",
			o.Candidate.P95LatencyMS, o.Stable.P95LatencyMS)}
	}
	if o.Candidate.ColdBootRequests >= CircuitBreakerMinColdBootRequests &&
		o.Stable.ColdBootRequests >= CircuitBreakerMinColdBootRequests &&
		o.Candidate.ColdBootP95LatencyMS >= o.Stable.ColdBootP95LatencyMS*circuitBreakerColdLatencyFactor &&
		o.Candidate.ColdBootP95LatencyMS-o.Stable.ColdBootP95LatencyMS >= circuitBreakerColdLatencyDeltaMS {
		return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
			"cold-boot request latency regression (candidate %.0fms, stable %.0fms; %d vs %d cold boots)",
			o.Candidate.ColdBootP95LatencyMS, o.Stable.ColdBootP95LatencyMS,
			o.Candidate.ColdBootRequests, o.Stable.ColdBootRequests)}
	}
	if !o.CPURequestSignalAvailable {
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "CPU/request signal unavailable"}
	}
	if o.Candidate.CPURequests < CircuitBreakerMinCPURequests || o.Stable.CPURequests < CircuitBreakerMinCPURequests {
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: fmt.Sprintf(
			"insufficient CPU/request samples (candidate=%d stable=%d; need %d each)",
			o.Candidate.CPURequests, o.Stable.CPURequests, CircuitBreakerMinCPURequests)}
	}
	if o.Candidate.CPUUsec <= 0 || o.Stable.CPUUsec <= 0 {
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "CPU/request signal unavailable"}
	}
	candidateCPUPerRequest := float64(o.Candidate.CPUUsec) / float64(o.Candidate.CPURequests)
	stableCPUPerRequest := float64(o.Stable.CPUUsec) / float64(o.Stable.CPURequests)
	if candidateCPUPerRequest >= stableCPUPerRequest*circuitBreakerCPUPerRequestFactor &&
		candidateCPUPerRequest-stableCPUPerRequest >= circuitBreakerCPUPerRequestDeltaUsec {
		return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
			"CPU/request regression (candidate %.0fµs, stable %.0fµs; %d vs %d requests)",
			candidateCPUPerRequest, stableCPUPerRequest,
			o.Candidate.CPURequests, o.Stable.CPURequests)}
	}
	if !o.DependencySignalAvailable {
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "dependency error signal unavailable"}
	}
	if o.Candidate.DependencyCalls >= CircuitBreakerMinDependencyCalls {
		candidateDependencyErrorPct := 100 * float64(o.Candidate.DependencyErrors) / float64(o.Candidate.DependencyCalls)
		if o.Stable.DependencyCalls >= CircuitBreakerMinDependencyCalls {
			stableDependencyErrorPct := 100 * float64(o.Stable.DependencyErrors) / float64(o.Stable.DependencyCalls)
			dependencyErrorThreshold := math.Max(circuitBreakerDependencyErrorFloorPct, stableDependencyErrorPct*circuitBreakerDependencyErrorFactor)
			if o.Candidate.DependencyErrors >= 2 &&
				candidateDependencyErrorPct >= dependencyErrorThreshold &&
				candidateDependencyErrorPct >= stableDependencyErrorPct+circuitBreakerDependencyErrorDeltaPct {
				return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
					"dependency error regression (candidate %.1f%%, stable %.1f%%; %d/%d vs %d/%d calls)",
					candidateDependencyErrorPct, stableDependencyErrorPct,
					o.Candidate.DependencyErrors, o.Candidate.DependencyCalls,
					o.Stable.DependencyErrors, o.Stable.DependencyCalls)}
			}
		} else if o.Candidate.DependencyErrors >= 2 && candidateDependencyErrorPct >= circuitBreakerDependencyErrorFloorPct {
			// A newly introduced managed dependency has no stable-side rate to
			// compare with. Use an absolute error floor after enough candidate
			// calls rather than silently treating the missing baseline as zero.
			return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
				"dependency error regression (candidate %.1f%%; %d/%d calls; stable sample unavailable)",
				candidateDependencyErrorPct, o.Candidate.DependencyErrors, o.Candidate.DependencyCalls)}
		}
	}
	return CircuitBreakerDecision{Action: CircuitBreakerAdvance}
}
