// Package cpustats wraps cgroupstats.Reader with a per-instance
// previous-sample cache. The cache turns the raw cumulative
// usage_usec counter into a rate (cpu_pct) and a cumulative seconds
// reading (cpu_seconds) that vmmd's Stats handler can serve on the
// wire without doing cgroup I/O on the hot path.
//
// # Design
//
// cgroupstats.Reader.Sample returns a raw cumulative counter
// (usage_usec since the cgroup was created). Converting that to
// "percent of one vCPU consumed over the last interval" requires
// the previous reading. This package holds that previous reading
// per instance and is the single source of truth for vmmd's
// CPU-on-the-wire output.
//
// # Regression contract
//
// A cgroup recreation (jailer restart, manual rmdir) resets
// usage_usec to a smaller value. The cache detects this by
// comparing the new reading to the last: a non-monotonic step
// drops the previous baseline and starts a new one, returning
// ok=false from Observe until two post-regression samples have
// been seen. This matches the documented contract at
// pkg/sched/instancestats/poller.go ("cumulative-counter
// regression detection lives in PR-B's Stats handler"). Schedd
// consumes this contract by stamping Unknown on the first
// post-regression row and re-baselining on the next.
//
// # Concurrency
//
// The cache is safe for one writer (the vmmd sample loop) and many
// readers (concurrent Stats gRPC handlers). The mu protects the
// map; reads under the mutex are O(1) and short. A future change
// to allow multi-writer pollers would sharded by instance id; the
// current vmmd has exactly one sample loop so a single mutex is
// the cheapest correct thing.
package cpustats

import (
	"sync"
	"time"
)

// Observation is the cache's input: an instance id, a cumulative
// CPU-µs reading, the cumulative cgroup throttled-µs reading
// (issue #301, ADR-044), and the wall-clock moment the reading
// was taken. ThrottledUsec is read from the same cpu.stat file as
// CPUUsageUsec so a single per-tick cgroup read populates both.
type Observation struct {
	InstanceID    string
	CPUUsageUsec  uint64
	ThrottledUsec uint64
	At            time.Time
}

// Reading is the cache's output: the rate (CPU percent of one
// vCPU) and the cumulative seconds since the cache was last
// reset for this instance. CPUSeconds is a monotonic
// counter-style value; CPUPct is the instantaneous rate between
// the last two observations.
//
// ThrottledSeconds (issue #301, ADR-044) is the cumulative
// CPU time this scope spent throttled, in seconds, since the
// cache was last reset for this instance. Counter-shape value
// (Σ(throttled_usec delta) / 1e6), used to label the
// vmmd_cpu_throttle_seconds_total{account_id, app_id} counter
// via schedd's rollup into the topAppSet admission (cap 100).
// Resets on regression under the same contract as CPUSeconds.
//
// ThrottledRatio (issue #301, ADR-044) is the per-tick
// throttle-to-total ratio:
//
//	ratio = (throttle_delta) / (throttle_delta + usage_delta)
//
// computed across the last two observations.
// Valid follows the same contract as the other fields — false on
// baseline / regression / clock-skew.
type Reading struct {
	CPUPct           float64
	CPUSeconds       float64
	ThrottledSeconds float64
	ThrottledRatio   float64
	// Valid is false when the cache has only one observation
	// (baseline not yet established) or the previous observation
	// was dropped on regression. Callers MUST treat Valid=false
	// as "no data"; never default to 0.
	Valid bool
}

type lastSample struct {
	usage     uint64
	throttled uint64
	at        time.Time
	// accumSeconds is the cumulative CPUSeconds reading for this
	// instance, preserved across the rate calculation. On
	// regression it resets to 0 — same shape as the
	// schedd-side seam, which sums Σ(usage_delta) over the
	// instance's lifetime.
	accumSeconds float64
	// accumThrottledSeconds (issue #301, ADR-044) is the
	// cumulative CPU-throttled-seconds reading for this
	// instance, preserved across the rate calculation. On
	// regression it resets to 0 under the same contract as
	// accumSeconds. Counter-shape value drives the
	// vmmd_cpu_throttle_seconds_total{account_id, app_id}
	// wire field (SchedStats's ThrottledSeconds column) and
	// the pkg/wire/topn_app.go topAppSet rollup.
	accumThrottledSeconds float64
	// lastRate is the most recent CPU% reading computed by
	// Observe. Stored on the cache so the Stats gRPC handler
	// can serve a fresh rate without doing any cgroup I/O or
	// advancing the baseline. Reset to 0 on regression.
	lastRate float64
	// lastThrottledRatio is the most recent throttle ratio
	// computed by Observe. Same caching rationale as lastRate.
	lastThrottledRatio float64
}

// Cache is a per-instance rate-and-accumulator over
// cgroupstats.Sample values. Construct with New (testable) or
// NewWithDefaults (production vmmd wiring).
type Cache struct {
	mu   sync.Mutex
	last map[string]lastSample
	now  func() time.Time
}

// New returns a Cache. now is consulted on every Observe; pass
// nil to use time.Now.
func New(now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	return &Cache{last: make(map[string]lastSample), now: now}
}

// NewWithDefaults returns a Cache that uses time.Now. Intended
// for cmd/vmmd wiring.
func NewWithDefaults() *Cache { return New(nil) }

// Observe records one cgroupstats.Sample for the given instance
// and returns the resulting Reading. ok is false on the first
// observation for a given instance (baseline) and on a
// usage_usec regression; in either case the previous baseline is
// reset so the next Observe returns a real reading.
//
// now is the wall-clock moment of the reading. Callers that batch
// readings from a single sample loop should pass a single now
// across the batch so the per-instance deltas are computed
// against the same instant — otherwise the rate drifts with the
// loop's iteration order.
func (c *Cache) Observe(o Observation) (Reading, bool) {
	if o.InstanceID == "" {
		return Reading{Valid: false}, false
	}
	now := o.At
	if now.IsZero() {
		now = c.now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, hadPrev := c.last[o.InstanceID]
	// First observation: just record the baseline; no rate yet.
	if !hadPrev {
		c.last[o.InstanceID] = lastSample{usage: o.CPUUsageUsec, throttled: o.ThrottledUsec, at: now}
		return Reading{Valid: false}, false
	}
	// Regression: drop baseline, start fresh. The next Observe
	// will return ok=true with the post-regression rate.
	// Either counter regressing (usage OR throttle) trips the
	// reset — they're written by the same kernel struct under
	// the same cgroup, so a regression in either one means a
	// recreation that nuked both baselines.
	if o.CPUUsageUsec < prev.usage || o.ThrottledUsec < prev.throttled {
		c.last[o.InstanceID] = lastSample{usage: o.CPUUsageUsec, throttled: o.ThrottledUsec, at: now}
		return Reading{Valid: false}, false
	}
	// No wall-clock progression: degenerate (clock didn't move,
	// or two samples at the same instant). Keep the previous
	// accumulator and return Valid=false so callers don't
	// divide by zero.
	if !now.After(prev.at) {
		return Reading{CPUSeconds: prev.accumSeconds, ThrottledSeconds: prev.accumThrottledSeconds, ThrottledRatio: prev.lastThrottledRatio}, false
	}
	deltaUsec := o.CPUUsageUsec - prev.usage
	deltaThrottledUsec := o.ThrottledUsec - prev.throttled
	deltaDur := now.Sub(prev.at)
	deltaSeconds := deltaDur.Seconds()
	// cpu_pct = 100 * (delta_usec / 1e6) / delta_seconds
	//   = 100 * (cpu_seconds_delta) / delta_seconds
	// deltaSeconds is bounded below by ~250ms (the vmmd sample
	// cadence) so this never underflows in practice; we still
	// guard for the clock-skew case.
	if deltaSeconds <= 0 {
		return Reading{CPUSeconds: prev.accumSeconds, ThrottledSeconds: prev.accumThrottledSeconds, ThrottledRatio: prev.lastThrottledRatio, Valid: false}, false
	}
	pct := 100.0 * (float64(deltaUsec) / 1e6) / deltaSeconds
	// Clamp to a sane upper bound. A vCPU can consume at most
	// 100% * vcpuCount; the schedd admission cap is 160 vCPU
	// across the fleet, but a single instance is bounded by
	// plan.vcpus (Free/Hobby/Pro = 2, Scale = 4). 400% is a
	// generous clip that catches runaway counters (e.g. a
	// kernel bug) without losing realistic burst readings.
	const maxPct = 400.0
	if pct > maxPct {
		pct = maxPct
	}
	if pct < 0 {
		pct = 0
	}
	// throttle_ratio = throttle_delta / (throttle_delta + usage_delta)
	// (issue #301, ADR-044). When both deltas are zero the
	// VM didn't run at all in this window — ratio is 0 by
	// convention (a true "no throttle" reading, distinct from
	// "no data"). Denominator guarded against the
	// (vanishingly-unlikely) case where both deltas are exactly
	// zero via an explicit check.
	totalDeltaUsec := deltaUsec + deltaThrottledUsec
	var throttleRatio float64
	if totalDeltaUsec > 0 {
		throttleRatio = float64(deltaThrottledUsec) / float64(totalDeltaUsec)
	}
	accum := prev.accumSeconds + float64(deltaUsec)/1e6
	accumThrottled := prev.accumThrottledSeconds + float64(deltaThrottledUsec)/1e6
	c.last[o.InstanceID] = lastSample{
		usage:                 o.CPUUsageUsec,
		throttled:             o.ThrottledUsec,
		at:                    now,
		accumSeconds:          accum,
		accumThrottledSeconds: accumThrottled,
		lastRate:              pct,
		lastThrottledRatio:    throttleRatio,
	}
	return Reading{
		CPUPct:           pct,
		CPUSeconds:       accum,
		ThrottledSeconds: accumThrottled,
		ThrottledRatio:   throttleRatio,
		Valid:            true,
	}, true
}

// Forget drops the cached baseline for an instance. Call this
// when vmmd tears down an instance so the map does not grow
// unbounded across the vmmd process lifetime.
func (c *Cache) Forget(instanceID string) {
	c.mu.Lock()
	delete(c.last, instanceID)
	c.mu.Unlock()
}

// Reset wipes all baselines. Used by tests and on vmmd restart
// (the in-memory state is gone anyway, but Reset is the
// documented seam for any future "drop everything" admin
// operation).
func (c *Cache) Reset() {
	c.mu.Lock()
	c.last = make(map[string]lastSample)
	c.mu.Unlock()
}

// Snapshot returns the current accumSeconds reading for an
// instance without advancing the baseline. Used by the Stats
// gRPC handler to populate the wire without forcing a fresh
// cgroup read on every gRPC call. ok=false means the cache has
// no baseline for the instance.
func (c *Cache) Snapshot(instanceID string) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, ok := c.last[instanceID]
	if !ok {
		return 0, false
	}
	return prev.accumSeconds, true
}

// Lookup returns the most recent Reading for an instance without
// advancing the baseline. ok=false means the cache has no
// baseline for the instance (first sample, regression, or
// forgotten). The returned Reading's Valid field mirrors ok
// semantically; callers should check both.
//
// The Reading.CPUPct is the rate computed at the most recent
// Observe (i.e. the rate for the most recent ~250 ms window of
// cgroup usage). It is not recomputed at Lookup time — that
// would require a fresh cgroup read and would put I/O back on
// the hot path the cache was designed to avoid. schedd's 200 ms
// poller will pull a fresh value at most 200 ms after the next
// sample loop tick.
func (c *Cache) Lookup(instanceID string) (Reading, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, ok := c.last[instanceID]
	if !ok {
		return Reading{Valid: false}, false
	}
	return Reading{
		CPUPct:           prev.lastRate,
		CPUSeconds:       prev.accumSeconds,
		ThrottledSeconds: prev.accumThrottledSeconds,
		ThrottledRatio:   prev.lastThrottledRatio,
		Valid:            true,
	}, true
}

// Size returns the number of instances currently tracked. For
// diagnostics and tests; not used on the hot path.
func (c *Cache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.last)
}
