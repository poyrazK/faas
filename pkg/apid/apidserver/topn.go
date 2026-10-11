// Sampler goroutine for the <prefix>_top_tenant_rps gauge (issue #300).
//
// Layering:
//
//   observeWrap → ObserveTopTenantRPS(id)   — per-request bump of the
//                                              rolling-window count.
//   topNSampler.run                         — once-per-5s tick that
//                                              computes rps from the
//                                              diff and calls
//                                              EmitTopTenantRPS.
//   EmitTopTenantRPS                        — drives the gauge from
//                                              the topAccountSet
//                                              snapshot, bounded at
//                                              cap+1 series.
//
// Why a goroutine:
//
//   The top-N read is the single goroutine that should ever drive the
//   gauge. If the per-request path (observeWrap) called Set on the
//   gauge directly, the cardinality bound would depend on the
//   instantaneous top-N membership, which bounces under concurrent
//   sampling — the gauge would accumulate one series per id that ever
//   transiently held a top-N slot, blowing past cap+1. Pushing the
//   gauge write to a once-per-5s tick from this sampler keeps the
//   gauge series set bounded at cap+1 deterministically.
//
// Why 5s:
//
//   Matches the spec §12 dashboard's 5s poll rate and the 24h rolling
//   window's natural sample grid. Faster (1s) burns CPU on the sort
//   for no panel fidelity gain; slower (30s) lets a noisy customer
//   skip through the gauge between ticks.
//
// Why a 24h rolling reset (not lifetime, not daily-snapshot):
//
//   The acceptance contract (issue #300 #4) is "top-1000 by 24h
//   request count". A lifetime view would let a one-shot noisy
//   customer persist in the top-N forever; a daily-snapshot view
//   would let a quiet customer jump into the top-N at midnight
//   UTC regardless of activity. The 24h rolling reset strikes the
//   balance — the dashboard reads "today's noisiest customers".
//
// Lifecycle:
//
//   Started by cmd/apid/main.go's bgBefore hook after the server is
//   constructed; runs until ctx is cancelled. Stops cleanly because
//   the only mutable state is the topAccountSet (the gauge rows
//   persist across ticks — Prometheus gauges don't have rows to
//   delete; emitting 0 simply updates the existing series).

package apidserver

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// topNSamplerInterval is the gauge-emission cadence (issue #300 #1:
// "5s sampled"). Faster ticks don't add panel fidelity; slower ticks
// let a noisy customer slip between samples. Sized to match the §12
// dashboard's 5s poll rate.
const topNSamplerInterval = 5 * time.Second

// topNSampler drives the per-daemon top-tenant gauge from the
// rolling requestTotal counter. One instance per OpsMetrics;
// constructed once at server boot, runs as a background goroutine
// for the daemon's lifetime. No state outside the bound
// topAccountSet — the gauge is re-emitted from scratch every
// tick, so a transient error during the previous tick doesn't
// poison the gauge.
//
// Concurrency: one sampler goroutine per apid process. The sampler
// is the ONLY writer to the apid_top_tenant_rps gauge; per-request
// observation paths call ObserveTopTenantRPS on the underlying
// topAccountSet (the cheap path) and never Set the gauge directly.
type topNSampler struct {
	ops *wire.OpsMetrics
	log *slog.Logger

	// prevCounts stores the rolling-count snapshot from the last
	// tick per account id. The 5s rps for the current tick is
	// (currentCount - prevCount[id]) / interval. Holds a mutex
	// because observeWrap bumps the rolling count (via
	// ObserveTopTenantRPS) concurrently with the sampler reading
	// it (via topAccountSet.topNSnapshot, which copies under the
	// primitive's own mutex). The diff math itself is a plain
	// map read, so the lock is short-lived.
	mu         sync.Mutex
	prevCounts map[string]uint64
}

// newTopNSampler constructs a sampler; the caller owns the
// goroutine lifecycle (start it after construction, cancel the
// ctx to stop it).
func newTopNSampler(ops *wire.OpsMetrics, log *slog.Logger) *topNSampler {
	return &topNSampler{
		ops:        ops,
		log:        log,
		prevCounts: make(map[string]uint64),
	}
}

// run drives the sampler loop until ctx is cancelled. Returns nil
// on clean shutdown. Errors are logged but non-fatal — a transient
// Prometheus /metrics scrape failure is recoverable on the next
// tick; a broken topAccountSet would only happen if observeWrap
// races with the ticker, which the sync.Mutex on the primitive
// already prevents.
//
// On each tick:
//
//  1. Snapshot the current topAccountSet counts (under the
//     primitive's own mutex).
//  2. Compute the per-id 5s rps = (current - prev) / interval.
//  3. Call EmitTopTenantRPS with a closure returning the rps for
//     each id; the closure also falls back to 0 for ids that just
//     left the top-N (so the gauge row goes to 0, not stale).
//  4. Stash current counts as prev for the next tick.
//
// The closure pattern lets EmitTopTenantRPS own the per-id gauge
// Set calls, so the sampler never touches prometheus types
// directly — keeps this file free of /metrics coupling beyond the
// one wire.OpsMetrics access.
func (s *topNSampler) run(ctx context.Context) {
	if s.ops == nil {
		// Defensive: the sampler is started from bgBefore AFTER
		// srv.WithOpsMetrics, so this should never trigger.
		// The guard exists so unit tests can construct + Run
		// without first calling WithOpsMetrics.
		s.log.Warn("topNSampler started with nil ops; exiting")
		return
	}
	t := time.NewTicker(topNSamplerInterval)
	defer t.Stop()
	// Drive one emit immediately so the gauge isn't empty for
	// the first 5s after boot. Matches the pre-instantiated
	// ("other",) row pattern from pkg/wire/metrics.go: every
	// other GaugeVec emits from the moment the daemon boots, not
	// only after the first observation.
	s.tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick()
		}
	}
}

// tick drives one sampler iteration. Extracted so the boot-time
// immediate emit (run) and the recurring tick share the same path.
// The /metrics scrape happens on a different cadence (Prometheus
// polls every 15s by default); the sampler's role is to keep the
// gauge values current so the next scrape sees fresh data.
func (s *topNSampler) tick() {
	set := s.ops.TopAccountSet()
	if set == nil {
		return
	}
	// Roll the 24h window if the primitive signals it's time.
	// Cheap read; resetWindow is a no-op when the window hasn't
	// elapsed. Public surface lives on topAccountSet — wire.OpsMetrics
	// doesn't forward ShouldReset/ResetWindow because they're
	// sampler-private (the gauge never needs to know).
	if set.ShouldReset() {
		set.ResetWindow()
	}
	// Snapshot the current rolling counts (primitive's own mutex).
	current := set.SnapshotCounts()
	// Compute per-id 5s rps and drive the gauge.
	s.mu.Lock()
	prev := s.prevCounts
	s.prevCounts = current
	s.mu.Unlock()
	emitted := s.ops.EmitTopTenantRPS(func(id string) float64 {
		now := current[id]
		// Diff over the tick interval. On the very first tick
		// prev is empty so the rps is the full count / interval —
		// acceptable: the gauge surfaces a non-zero value as
		// soon as the daemon sees its first request, then
		// converges to a true 5s delta on subsequent ticks.
		delta := now
		if v, ok := prev[id]; ok && now >= v {
			delta = now - v
		}
		return float64(delta) / topNSamplerInterval.Seconds()
	})
	s.log.Debug("topNSampler tick", slog.Int("emitted_series", emitted))
}
