package sched

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// workerScaleInStabilizer damps worker-pool scale-in. Demand is recomputed
// every tick from queue depth, so a queue that drains between two ticks and
// refills on the next would otherwise stop workers and immediately boot
// replacements. Each pool keeps its recommendations for one window, and a
// pool may only shrink to the highest of them. Scale-out passes through.
type workerScaleInStabilizer struct {
	window time.Duration

	mu        sync.Mutex
	pools     map[workerPoolKey]*workerDemandHistory
	lastSweep time.Time
}

// One generation of one environment's pool. A new deployment generation
// starts a fresh history rather than inheriting its predecessor's peak.
type workerPoolKey struct {
	appID        string
	scope        string
	deploymentID string
}

type workerDemandHistory struct {
	firstSeen time.Time
	samples   []workerDemandSample
}

type workerDemandSample struct {
	at      time.Time
	desired int
}

func newWorkerScaleInStabilizer(window time.Duration) *workerScaleInStabilizer {
	return &workerScaleInStabilizer{window: window, pools: map[workerPoolKey]*workerDemandHistory{}}
}

// stabilize records desired and returns the replica count the pool may
// converge to. current is the number of the target generation's workers
// that already count for concurrency.
func (s *workerScaleInStabilizer) stabilize(key workerPoolKey, current, desired int, now time.Time) int {
	if s == nil || s.window <= 0 {
		return desired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	history := s.pools[key]
	if history == nil {
		history = &workerDemandHistory{firstSeen: now}
		s.pools[key] = history
	}
	cutoff := now.Add(-s.window)
	kept := history.samples[:0]
	for _, sample := range history.samples {
		if !sample.at.Before(cutoff) {
			kept = append(kept, sample)
		}
	}
	history.samples = append(kept, workerDemandSample{at: now, desired: desired})
	if desired >= current {
		return desired
	}
	// One sample is not evidence that the backlog is gone. Until this schedd
	// has watched the pool for a whole window, keep what is running.
	if now.Sub(history.firstSeen) < s.window {
		return current
	}
	peak := desired
	for _, sample := range history.samples {
		if sample.desired > peak {
			peak = sample.desired
		}
	}
	if peak > current {
		peak = current
	}
	return peak
}

// sweepLocked forgets pools that stopped reporting (deleted apps, retired
// generations). It runs at most once per window so the per-tick cost stays
// independent of the number of pools.
func (s *workerScaleInStabilizer) sweepLocked(now time.Time) {
	if now.Sub(s.lastSweep) < s.window {
		return
	}
	s.lastSweep = now
	stale := now.Add(-2 * s.window)
	for key, history := range s.pools {
		if n := len(history.samples); n == 0 || history.samples[n-1].at.Before(stale) {
			delete(s.pools, key)
		}
	}
}

// workerBusyRank orders scale-in victims. Telemetry reports in-flight
// requests for push deliveries only; a worker with unknown telemetry may be
// processing, so observed-idle workers are stopped before it.
func (e *Engine) workerBusyRank(ins state.Instance, now time.Time) int {
	inflight, _, observed := e.telemetryCache.LookupInflightRequests(ins.ID, now)
	switch {
	case !observed:
		return 1
	case inflight > 0:
		return 0
	default:
		return 2
	}
}
