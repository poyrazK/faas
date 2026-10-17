package canary

import (
	"strings"
	"testing"
)

func TestEvaluateCircuitBreaker(t *testing.T) {
	base := CircuitBreakerObservation{
		HasStable:                 true,
		StableDeploymentID:        "stable-1",
		OOMSignalAvailable:        true,
		LivenessSignalAvailable:   true,
		CPURequestSignalAvailable: true,
		DependencySignalAvailable: true,
		Candidate:                 HealthWindow{Requests: 100, ServerErrors: 1, P95LatencyMS: 120, ColdBootRequests: 10, ColdBootP95LatencyMS: 900, CPURequests: 100, CPUUsec: 10_000_000},
		Stable:                    HealthWindow{Requests: 100, ServerErrors: 1, P95LatencyMS: 100, ColdBootRequests: 10, ColdBootP95LatencyMS: 800, CPURequests: 100, CPUUsec: 10_000_000},
	}
	tests := []struct {
		name   string
		mutate func(*CircuitBreakerObservation)
		want   CircuitBreakerAction
	}{
		{name: "healthy candidate advances", want: CircuitBreakerAdvance},
		{name: "first deployment has no rollback comparison", mutate: func(o *CircuitBreakerObservation) { o.HasStable = false }, want: CircuitBreakerAdvance},
		{name: "oom aborts immediately", mutate: func(o *CircuitBreakerObservation) { o.OOMKills = 1 }, want: CircuitBreakerAbort},
		{name: "missing oom signal holds", mutate: func(o *CircuitBreakerObservation) { o.OOMSignalAvailable = false }, want: CircuitBreakerHold},
		{name: "missing liveness signal holds", mutate: func(o *CircuitBreakerObservation) { o.LivenessSignalAvailable = false }, want: CircuitBreakerHold},
		{name: "crash loop aborts without request samples", mutate: func(o *CircuitBreakerObservation) {
			o.CandidateLivenessRestarts = CircuitBreakerMinCrashLoopRestarts
			o.Candidate.Requests = 0
		}, want: CircuitBreakerAbort},
		{name: "single restart is not a crash loop", mutate: func(o *CircuitBreakerObservation) { o.CandidateLivenessRestarts = 1 }, want: CircuitBreakerAdvance},
		{name: "restarts matching a flapping predecessor do not abort", mutate: func(o *CircuitBreakerObservation) {
			o.CandidateLivenessRestarts = 4
			o.StableLivenessRestarts = 2
		}, want: CircuitBreakerAdvance},
		{name: "missing cpu per request signal holds", mutate: func(o *CircuitBreakerObservation) { o.CPURequestSignalAvailable = false }, want: CircuitBreakerHold},
		{name: "missing dependency error signal holds", mutate: func(o *CircuitBreakerObservation) { o.DependencySignalAvailable = false }, want: CircuitBreakerHold},
		{name: "insufficient cpu request samples hold", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.CPURequests = CircuitBreakerMinCPURequests - 1
		}, want: CircuitBreakerHold},
		{name: "small sample holds", mutate: func(o *CircuitBreakerObservation) { o.Candidate.Requests = CircuitBreakerMinRequests - 1 }, want: CircuitBreakerHold},
		// production-us hunt #9: a --safe canary answering 500 to every
		// request held at 1% for 6.5 minutes (candidate=19 of 19 failing)
		// waiting for 20 samples. Five decisive failures are enough.
		{name: "decisively failing small candidate aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 5, ServerErrors: 5}
			o.Stable = HealthWindow{Requests: 1414, ServerErrors: 0}
		}, want: CircuitBreakerAbort},
		{name: "half failing small candidate aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 10, ServerErrors: 5}
			o.Stable = HealthWindow{Requests: 200, ServerErrors: 2}
		}, want: CircuitBreakerAbort},
		{name: "four small-sample failures still hold", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 4, ServerErrors: 4}
			o.Stable = HealthWindow{Requests: 200, ServerErrors: 0}
		}, want: CircuitBreakerHold},
		{name: "minority small-sample failures hold", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 12, ServerErrors: 5}
			o.Stable = HealthWindow{Requests: 200, ServerErrors: 0}
		}, want: CircuitBreakerHold},
		{name: "small failing candidate with failing stable holds", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 6, ServerErrors: 6}
			o.Stable = HealthWindow{Requests: 200, ServerErrors: 180}
		}, want: CircuitBreakerHold},
		{name: "small failing candidate without stable sample holds", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 6, ServerErrors: 6}
			o.Stable = HealthWindow{Requests: CircuitBreakerMinRequests - 1}
		}, want: CircuitBreakerHold},
		{name: "strong 5xx regression aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 100, ServerErrors: 10, P95LatencyMS: 120, CPURequests: 100, CPUUsec: 10_000_000}
			o.Stable = HealthWindow{Requests: 100, ServerErrors: 1, P95LatencyMS: 100, CPURequests: 100, CPUUsec: 10_000_000}
		}, want: CircuitBreakerAbort},
		{name: "small absolute 5xx delta does not abort", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate = HealthWindow{Requests: 100, ServerErrors: 5, P95LatencyMS: 120, CPURequests: 100, CPUUsec: 10_000_000}
			o.Stable = HealthWindow{Requests: 100, ServerErrors: 1, P95LatencyMS: 100, CPURequests: 100, CPUUsec: 10_000_000}
		}, want: CircuitBreakerAdvance},
		{name: "p95 regression aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.P95LatencyMS = 250
			o.Stable.P95LatencyMS = 100
		}, want: CircuitBreakerAbort},
		{name: "cold-start p95 regression aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.ColdBootP95LatencyMS = 1800
			o.Stable.ColdBootP95LatencyMS = 800
		}, want: CircuitBreakerAbort},
		{name: "cpu per request regression aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.CPUUsec = 80_000_000 // 800ms per request vs 100ms stable.
		}, want: CircuitBreakerAbort},
		// production-us hunt #7 (H5-61): an unchanged release at a 5% step.
		// Idle CPU divided by the candidate's few requests is not a regression.
		{name: "cpu per request is not compared while the candidate carries little load", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.CPURequests, o.Candidate.CPUUsec = 23, 23*53_890
			o.Stable.CPURequests, o.Stable.CPUUsec = 462, 462*9_447
		}, want: CircuitBreakerAdvance},
		{name: "cpu per request regression at comparable load aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.CPURequests, o.Candidate.CPUUsec = 60, 60*800_000
			o.Stable.CPURequests, o.Stable.CPUUsec = 100, 100*100_000
		}, want: CircuitBreakerAbort},
		{name: "small cpu per request change does not abort", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.CPUUsec = 19_000_000 // 190ms per request; below 3x stable.
		}, want: CircuitBreakerAdvance},
		{name: "dependency error regression aborts", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.DependencyCalls, o.Candidate.DependencyErrors = 100, 30
			o.Stable.DependencyCalls, o.Stable.DependencyErrors = 100, 1
		}, want: CircuitBreakerAbort},
		{name: "new dependency uses absolute error floor", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.DependencyCalls, o.Candidate.DependencyErrors = 20, 5
		}, want: CircuitBreakerAbort},
		{name: "sparse dependency calls do not decide rollout", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.DependencyCalls, o.Candidate.DependencyErrors = CircuitBreakerMinDependencyCalls-1, 8
		}, want: CircuitBreakerAdvance},
		{name: "sparse candidate cold starts do not abort", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.ColdBootRequests = CircuitBreakerMinColdBootRequests - 1
			o.Candidate.ColdBootP95LatencyMS = 3000
		}, want: CircuitBreakerAdvance},
		{name: "sparse stable cold starts do not abort", mutate: func(o *CircuitBreakerObservation) {
			o.Stable.ColdBootRequests = CircuitBreakerMinColdBootRequests - 1
			o.Candidate.ColdBootP95LatencyMS = 3000
		}, want: CircuitBreakerAdvance},
		{name: "small p95 change does not abort", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.P95LatencyMS = 199
			o.Stable.P95LatencyMS = 100
		}, want: CircuitBreakerAdvance},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observation := base
			if tt.mutate != nil {
				tt.mutate(&observation)
			}
			if got := EvaluateCircuitBreaker(observation); got.Action != tt.want {
				t.Fatalf("EvaluateCircuitBreaker().Action = %q, want %q (decision: %+v)", got.Action, tt.want, got)
			}
		})
	}
}

func TestLowTrafficAdvanceAllowed(t *testing.T) {
	clean := CircuitBreakerObservation{HasStable: true, OOMSignalAvailable: true, LivenessSignalAvailable: true, Candidate: HealthWindow{Requests: 2}}
	sampleHold := CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "insufficient request samples (candidate=2 stable=3; need 20 each)"}
	tests := []struct {
		name   string
		d      CircuitBreakerDecision
		mutate func(*CircuitBreakerObservation)
		want   bool
	}{
		{name: "request sample hold with clean evidence", d: sampleHold, want: true},
		{name: "cpu sample hold with clean evidence", d: CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "insufficient CPU/request samples (candidate=1 stable=1; need 20 each)"}, want: true},
		{name: "candidate 5xx", d: sampleHold, mutate: func(o *CircuitBreakerObservation) { o.Candidate.ServerErrors = 1 }},
		{name: "candidate probe 5xx", d: sampleHold, mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.ProbeRequests, o.Candidate.ProbeServerErrors = 5, 1
		}},
		{name: "oom signal unavailable", d: sampleHold, mutate: func(o *CircuitBreakerObservation) { o.OOMSignalAvailable = false }},
		{name: "oom kill", d: sampleHold, mutate: func(o *CircuitBreakerObservation) { o.OOMKills = 1 }},
		{name: "liveness restart", d: sampleHold, mutate: func(o *CircuitBreakerObservation) { o.CandidateLivenessRestarts = 1 }},
		{name: "liveness signal unavailable", d: sampleHold, mutate: func(o *CircuitBreakerObservation) { o.LivenessSignalAvailable = false }},
		{name: "signal unavailable hold", d: CircuitBreakerDecision{Action: CircuitBreakerHold, Reason: "dependency error signal unavailable"}},
		{name: "abort", d: CircuitBreakerDecision{Action: CircuitBreakerAbort, Reason: "insufficient request samples"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := clean
			if tc.mutate != nil {
				tc.mutate(&o)
			}
			if got := LowTrafficAdvanceAllowed(tc.d, o); got != tc.want {
				t.Fatalf("LowTrafficAdvanceAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}

// adr: 1446
// Synthetic probes join the 5xx sample while organic traffic is below the
// request floor: a failing quiet candidate aborts, and clean probes on both
// sides let the stage advance without waiting for organic traffic.
func TestProbeBackedLowTrafficEvidence(t *testing.T) {
	quiet := CircuitBreakerObservation{
		HasStable: true, StableDeploymentID: "stable-1", OOMSignalAvailable: true, LivenessSignalAvailable: true,
		CPURequestSignalAvailable: true, DependencySignalAvailable: true,
		Candidate: HealthWindow{Requests: 1}, Stable: HealthWindow{Requests: 2},
	}
	probes := func(cReq, cErr, sReq, sErr int64) func(*CircuitBreakerObservation) {
		return func(o *CircuitBreakerObservation) {
			o.Candidate.ProbeRequests, o.Candidate.ProbeServerErrors = cReq, cErr
			o.Stable.ProbeRequests, o.Stable.ProbeServerErrors = sReq, sErr
		}
	}
	tests := []struct {
		name        string
		mutate      func(*CircuitBreakerObservation)
		wantAction  CircuitBreakerAction
		wantAdvance bool
	}{
		{name: "failing probes abort", mutate: probes(20, 20, 20, 0), wantAction: CircuitBreakerAbort},
		{name: "failing probes abort before the floor", mutate: probes(6, 6, 20, 0), wantAction: CircuitBreakerAbort},
		{name: "clean probes advance", mutate: probes(20, 0, 20, 0), wantAction: CircuitBreakerHold, wantAdvance: true},
		{name: "a rare probe 5xx is no regression", mutate: probes(30, 1, 30, 0), wantAction: CircuitBreakerHold, wantAdvance: true},
		{name: "a shared failure is no regression", mutate: probes(20, 20, 20, 20), wantAction: CircuitBreakerHold, wantAdvance: true},
		{name: "too few probes keep holding", mutate: probes(10, 0, 10, 0), wantAction: CircuitBreakerHold},
		{name: "an unprobed predecessor keeps holding", mutate: probes(20, 0, 0, 0), wantAction: CircuitBreakerHold},
		{name: "a liveness restart keeps holding", mutate: func(o *CircuitBreakerObservation) {
			probes(20, 0, 20, 0)(o)
			o.CandidateLivenessRestarts = 1
		}, wantAction: CircuitBreakerHold},
		{name: "probes never replace organic samples", mutate: func(o *CircuitBreakerObservation) {
			o.Candidate.Requests, o.Stable.Requests = 100, 100
			o.Candidate.CPURequests, o.Candidate.CPUUsec = 100, 1_000_000
			o.Stable.CPURequests, o.Stable.CPUUsec = 100, 1_000_000
			probes(20, 20, 20, 0)(o)
		}, wantAction: CircuitBreakerAdvance},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := quiet
			tc.mutate(&o)
			d := EvaluateCircuitBreaker(o)
			if d.Action != tc.wantAction {
				t.Fatalf("EvaluateCircuitBreaker = %+v, want %s", d, tc.wantAction)
			}
			if d.Action == CircuitBreakerAbort && (!strings.HasPrefix(d.Reason, "5xx regression") || !strings.Contains(d.Reason, "synthetic probes included")) {
				t.Fatalf("abort reason = %q, want a probe-backed 5xx regression", d.Reason)
			}
			if got := ProbeEvidenceAdvanceAllowed(d, o); got != tc.wantAdvance {
				t.Fatalf("ProbeEvidenceAdvanceAllowed = %v, want %v (decision %+v)", got, tc.wantAdvance, d)
			}
		})
	}
}
