package canary

import (
	"fmt"
	"math"
	"strings"
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
	// CircuitBreakerMinDecisiveServerErrors lets a small candidate sample
	// abort when it is failing outright. At a 1% step a low-traffic app takes
	// many minutes to collect CircuitBreakerMinRequests candidate requests;
	// a revision answering 5xx to most of them should not keep serving
	// customers that long. Five 5xx covering at least half the candidate's
	// requests is the same bar as the first-window 5xx rollback (ADR-625).
	CircuitBreakerMinDecisiveServerErrors int64 = 5
	// Managed service dependencies are less frequent than inbound requests, so
	// require a smaller but still useful denominator before comparing rates.
	CircuitBreakerMinDependencyCalls int64 = 10
	// CircuitBreakerMinCrashLoopRestarts is how many liveness-driven restarts
	// of the candidate mark a crash loop (ADR-1446). Restarts need no request
	// sample, so a crash-looping release is caught even with no traffic.
	CircuitBreakerMinCrashLoopRestarts float64 = 2

	circuitBreakerErrorRateFloorPct      = 5.0
	circuitBreakerErrorRateDeltaPct      = 5.0
	circuitBreakerErrorRateFactor        = 3.0
	circuitBreakerLatencyFactor          = 2.0
	circuitBreakerLatencyDeltaMS         = 100.0
	circuitBreakerColdLatencyFactor      = 2.0
	circuitBreakerColdLatencyDeltaMS     = 250.0
	circuitBreakerCPUPerRequestFactor    = 3.0
	circuitBreakerCPUPerRequestDeltaUsec = 10_000.0
	// circuitBreakerCPUComparableLoadDivisor: CPU/request is compared only
	// once the candidate served at least 1/divisor of the stable requests.
	circuitBreakerCPUComparableLoadDivisor = 2
	circuitBreakerDependencyErrorFloorPct  = 20.0
	circuitBreakerDependencyErrorDeltaPct  = 10.0
	circuitBreakerDependencyErrorFactor    = 3.0
	// A candidate must restart more than this multiple of its predecessor's
	// restarts in the same window, so a predecessor that already flaps does
	// not make every release look like a regression.
	circuitBreakerCrashLoopFactor = 2.0
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
	// ProbeRequests and ProbeServerErrors are synthetic probe responses
	// attributed to this deployment over the stage (ADR-1446), excluding
	// responses rejected by the app's own auth. They join the 5xx sample only
	// while organic traffic is below the request floor and never feed
	// latency, cold-boot, CPU, or dependency comparisons.
	ProbeRequests     int64
	ProbeServerErrors int64
}

// CircuitBreakerObservation compares the candidate with its currently
// serving predecessor and carries the independent per-deployment OOM signal.
type CircuitBreakerObservation struct {
	Candidate          HealthWindow
	Stable             HealthWindow
	StableDeploymentID string
	HasStable          bool
	OOMKills           float64
	OOMSignalAvailable bool
	// Liveness-driven destroy+cold-boot cycles in the rollout window
	// (ADR-1446). LivenessSignalAvailable is false when the metric family is
	// not exported, which holds rather than reading as zero restarts.
	CandidateLivenessRestarts float64
	StableLivenessRestarts    float64
	LivenessSignalAvailable   bool
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
	if !o.LivenessSignalAvailable {
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "liveness restart signal unavailable"}
	}
	if o.CandidateLivenessRestarts >= CircuitBreakerMinCrashLoopRestarts &&
		o.CandidateLivenessRestarts > o.StableLivenessRestarts*circuitBreakerCrashLoopFactor {
		return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
			"crash loop (candidate %.0f liveness restarts, stable %.0f)",
			o.CandidateLivenessRestarts, o.StableLivenessRestarts)}
	}
	if decision, ok := decisiveSmallSample5xx(o); ok {
		return decision
	}
	if o.Candidate.Requests < CircuitBreakerMinRequests || o.Stable.Requests < CircuitBreakerMinRequests {
		if decision, ok := probeBacked5xx(o); ok {
			return decision
		}
		return CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: fmt.Sprintf(
			"insufficient request samples (candidate=%d stable=%d; need %d each)",
			o.Candidate.Requests, o.Stable.Requests, CircuitBreakerMinRequests)}
	}
	if decision, ok := serverErrorRegression(o.Candidate, o.Stable, ""); ok {
		return decision
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
	if cpuLoadComparable(o) &&
		candidateCPUPerRequest >= stableCPUPerRequest*circuitBreakerCPUPerRequestFactor &&
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

// LowTrafficAdvanceAllowed reports whether an inconclusive hold is only a
// sample-size hold with no negative evidence (ADR-1446). A scale-to-zero app
// may never send twenty requests to a 1% candidate, so an unbounded hold
// would strand it there. Any candidate 5xx, OOM kill, liveness restart, or
// unreadable OOM or liveness signal keeps the hold; regressions with enough
// samples abort before this.
func LowTrafficAdvanceAllowed(d CircuitBreakerDecision, o CircuitBreakerObservation) bool {
	if d.Action != CircuitBreakerHold {
		return false
	}
	if !strings.HasPrefix(d.Reason, "insufficient request samples") &&
		!strings.HasPrefix(d.Reason, "insufficient CPU/request samples") {
		return false
	}
	// A probe 5xx is negative evidence too; the probe path below decides once
	// enough probes arrive.
	return o.Candidate.ServerErrors == 0 && o.Candidate.ProbeServerErrors == 0 && quietSignalsClean(o)
}

// ProbeEvidenceAdvanceAllowed reports whether synthetic probes cover a
// request-sample hold (ADR-1446): organic plus probe responses meet the
// request floor on both sides, the candidate shows no 5xx regression against
// its predecessor, and the OOM and liveness signals are readable and clean.
// The canary then advances at the stage boundary on evidence instead of
// waiting api.CanaryLowTrafficMaxHold for traffic that may never come.
func ProbeEvidenceAdvanceAllowed(d CircuitBreakerDecision, o CircuitBreakerObservation) bool {
	if d.Action != CircuitBreakerHold || !strings.HasPrefix(d.Reason, "insufficient request samples") {
		return false
	}
	if o.Candidate.ProbeRequests == 0 || o.Stable.ProbeRequests == 0 {
		return false
	}
	c, s := withProbes(o.Candidate), withProbes(o.Stable)
	if c.Requests < CircuitBreakerMinRequests || s.Requests < CircuitBreakerMinRequests {
		return false
	}
	if _, regressed := serverErrorRegression(c, s, ""); regressed {
		return false
	}
	return quietSignalsClean(o)
}

func quietSignalsClean(o CircuitBreakerObservation) bool {
	return o.OOMSignalAvailable && o.OOMKills == 0 && o.LivenessSignalAvailable && o.CandidateLivenessRestarts == 0
}

// withProbes is the 5xx sample of one side with its probe responses added.
func withProbes(w HealthWindow) HealthWindow {
	return HealthWindow{Requests: w.Requests + w.ProbeRequests, ServerErrors: w.ServerErrors + w.ProbeServerErrors}
}

// probeBacked5xx applies the 5xx rules to organic plus probe responses while
// organic traffic is below the request floor (ADR-1446), so a quiet canary
// that fails its probes aborts before it is promoted.
func probeBacked5xx(o CircuitBreakerObservation) (CircuitBreakerDecision, bool) {
	if o.Candidate.ProbeRequests == 0 && o.Stable.ProbeRequests == 0 {
		return CircuitBreakerDecision{}, false
	}
	combined := CircuitBreakerObservation{Candidate: withProbes(o.Candidate), Stable: withProbes(o.Stable)}
	if decision, ok := decisiveSmallSample5xx(combined); ok {
		decision.Reason += "; synthetic probes included"
		return decision, true
	}
	if combined.Candidate.Requests < CircuitBreakerMinRequests || combined.Stable.Requests < CircuitBreakerMinRequests {
		return CircuitBreakerDecision{}, false
	}
	return serverErrorRegression(combined.Candidate, combined.Stable, "; synthetic probes included")
}

// ServerErrorRegression applies the breaker's 5xx rate comparison to two
// samples that both meet CircuitBreakerMinRequests. Config guards (ADR-1451)
// judge a config change against the config before it with the same rule.
func ServerErrorRegression(candidate, stable HealthWindow) (CircuitBreakerDecision, bool) {
	if candidate.Requests < CircuitBreakerMinRequests || stable.Requests < CircuitBreakerMinRequests {
		return CircuitBreakerDecision{}, false
	}
	return serverErrorRegression(candidate, stable, "")
}

// LatencyRegression applies the breaker's p95 comparison to two samples that
// both meet CircuitBreakerMinRequests. The post-release watch (ADR-1445)
// judges a completed release against its predecessor with it.
func LatencyRegression(candidate, stable HealthWindow) (CircuitBreakerDecision, bool) {
	if candidate.Requests < CircuitBreakerMinRequests || stable.Requests < CircuitBreakerMinRequests {
		return CircuitBreakerDecision{}, false
	}
	if candidate.P95LatencyMS >= stable.P95LatencyMS*circuitBreakerLatencyFactor &&
		candidate.P95LatencyMS-stable.P95LatencyMS >= circuitBreakerLatencyDeltaMS {
		return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
			"p95 latency regression (candidate %.0fms, stable %.0fms)", candidate.P95LatencyMS, stable.P95LatencyMS)}, true
	}
	return CircuitBreakerDecision{}, false
}

// serverErrorRegression is the 5xx rate comparison for two samples that
// already meet the request floor.
func serverErrorRegression(c, s HealthWindow, note string) (CircuitBreakerDecision, bool) {
	candidateErrorPct := 100 * float64(c.ServerErrors) / float64(c.Requests)
	stableErrorPct := 100 * float64(s.ServerErrors) / float64(s.Requests)
	errorThreshold := math.Max(circuitBreakerErrorRateFloorPct, stableErrorPct*circuitBreakerErrorRateFactor)
	if c.ServerErrors < 2 || candidateErrorPct < errorThreshold || candidateErrorPct < stableErrorPct+circuitBreakerErrorRateDeltaPct {
		return CircuitBreakerDecision{}, false
	}
	return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
		"5xx regression (candidate %.1f%%, stable %.1f%%; %d/%d vs %d/%d requests%s)",
		candidateErrorPct, stableErrorPct, c.ServerErrors, c.Requests, s.ServerErrors, s.Requests, note)}, true
}

// cpuLoadComparable reports whether the candidate carried enough of the
// load for a CPU/request comparison to mean anything. An instance also
// spends CPU while it waits for requests (about 1.2 s per minute for a Node
// app on production-us), and that idle CPU is divided by the minute's
// requests. At a 5% canary step the stable instance spread it over 140-250
// requests a minute and the candidate over 4-10, so an unchanged release
// read as a 5.7x "CPU/request regression" and was aborted (hunt #7, H5-61).
// At comparable load both sides carry the same idle share. Every preset but
// "slow" reaches 50% before 100%, where the comparison still runs; the
// latency, 5xx, cold-boot, OOM and dependency signals apply at every step.
func cpuLoadComparable(o CircuitBreakerObservation) bool {
	return o.Candidate.CPURequests*circuitBreakerCPUComparableLoadDivisor >= o.Stable.CPURequests
}

// decisiveSmallSample5xx aborts a candidate that is failing outright before
// it reaches the ordinary sample floor. The stable predecessor still needs a
// full sample, so a shared outage (both failing) keeps the normal hold.
func decisiveSmallSample5xx(o CircuitBreakerObservation) (CircuitBreakerDecision, bool) {
	c, s := o.Candidate, o.Stable
	if c.Requests >= CircuitBreakerMinRequests || c.ServerErrors < CircuitBreakerMinDecisiveServerErrors ||
		s.Requests < CircuitBreakerMinRequests || 2*c.ServerErrors < c.Requests {
		return CircuitBreakerDecision{}, false
	}
	candidateErrorPct := 100 * float64(c.ServerErrors) / float64(c.Requests)
	stableErrorPct := 100 * float64(s.ServerErrors) / float64(s.Requests)
	if candidateErrorPct < stableErrorPct*circuitBreakerErrorRateFactor ||
		candidateErrorPct < stableErrorPct+circuitBreakerErrorRateDeltaPct {
		return CircuitBreakerDecision{}, false
	}
	return CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: fmt.Sprintf(
		"5xx regression (candidate %.1f%%, stable %.1f%%; %d/%d vs %d/%d requests; decisive before the %d-request floor)",
		candidateErrorPct, stableErrorPct, c.ServerErrors, c.Requests, s.ServerErrors, s.Requests, CircuitBreakerMinRequests)}, true
}
