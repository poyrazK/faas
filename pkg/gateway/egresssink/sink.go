// Package egresssink implements the per-instance minute-bucketed byte
// ring buffer that gatewayd-internal populates with HTTP response body bytes
// and meterd drains to populate usage_minutes.tx_bytes.
//
// Why this lives in pkg/gateway/egresssink rather than cmd/gatewayd-internal/:
//
//	The meterd-side adapter (cmd/meterd/main.go) eventually dials a
//	unix-socket gRPC stream from gatewayd-internal and reads DrainBytes records
//	back. Keeping the primitive in a leaf package means the dialer
//	imports it without taking on cmd/gatewayd-internal/→ pkg/gateway →
//	cmd/gatewayd-internal/internal dependencies, and the gateway-side writer
//	(handler.go) imports it without dragging in any gRPC types.
//
// Why per-instance minute buckets rather than a single global counter:
//
//	gatewayd-internal proxies to N live instances at once. Each instance has
//	its own usage_minutes row (keyed on instance_id + minute).
//	Aggregate-then-split loses the attribution we need for billing
//	forensics — PR #266 (ADR-046) is the seam; the per-instance shape
//	is the billing-correct one. Bucketing by truncated minute gives
//	the meter sampler (which ticks once per minute) an exact drain
//	boundary; partial-minute accrual within a single bucket is fine
//	because the next minute's traffic lands in a different bucket.
//
// Why a per-instance mutex instead of a single global lock:
//
//	On every 2xx/3xx response the gateway handler records bytes into
//	the sink. Under the realistic "many instances, one global lock"
//	shape, concurrent writes serialize and the lock becomes the
//	bottleneck that scales linearly with fleet size. Per-instance
//	mutex means contention is bounded by per-instance request rate,
//	not fleet-wide — which matches the per-instance cc-stamped
//	sampler shape in pkg/gateway/topn.go.
//
// Why the sweeper:
//
//	An instance parks (state → PARKED). Park removes it from the
//	gateway pool but does NOT signal the sink — gatewayd-internal is happy
//	to forget about it on its own. Without eviction the map grows
//	indefinitely as tenants cycle through instances over weeks.
//	Sweeping on every Record keeps the hot path evict-clean and the
//	memory ceiling is bounded by the active fleet (same bound
//	gatewayd-internal already enforces via the LiveSet).
//
// Lifecycle:
//
//	One EgressSink instance per cmd/gatewayd-internal/daemon. Constructed in
//	cmd/gatewayd-internal/main.go after the Handler; Read by both the proxy
//	path (handler.go via RecordResponseBytes) and the gRPC egress
//	service (cmd/gatewayd-internal/via the sink's DrainRecords). Meterd
//	re-uses the same DrainRecords interface over gRPC — the wire
//	shape mirrors the in-memory one to avoid a translation step.
package egresssink

import (
	"sync"
	"time"
)

// BucketsToKeep is the number of past minute-buckets the sink
// retains per instance. Two minutes covers the meterd sampler
// cadence (1/min) plus one minute of lookback for clock skew; three
// gives buffer for a slow sampler without unbounded growth. PR-2
// keeps it deliberately small — the sampler reads at most the
// current bucket on every tick, and past buckets are useful only
// when a tick is delayed.
const BucketsToKeep = 3

// EgressSink is the per-instance, minute-bucketed byte ring buffer.
// Safe for concurrent use; one per gatewayd-internal process.
type EgressSink struct {
	// now is the clock injection point so tests can advance time
	// deterministically without sleeping.
	now func() time.Time

	// mu protects the instances map. The per-instance mutex (held
	// inside each *instanceBuckets) covers the per-instance minute
	// map. Lock order is s.mu before inst.mu — both RecordResponseBytes
	// and DrainRecords take s.mu first, then inst.mu. Per-instance
	// contention is bounded by per-instance request rate, not
	// fleet-wide; the s.mu serialisation is only on the map operation
	// (create-new-row / iterate / eviction) and is the natural
	// bottleneck of any map-backed concurrent structure.
	mu        sync.Mutex
	instances map[string]*instanceBuckets
}

// instanceBuckets is the per-instance state. bytes is keyed on the
// truncated-minute Unix timestamp; zero-value is the
// "no-observation" state, eliminating any explicit nil checks on
// the hot path. mu covers ONLY the (map + lastTouch) pair — never
// held across I/O. Always taken after s.mu, never before.
type instanceBuckets struct {
	mu        sync.Mutex
	usage     map[int64]usageBucket
	lastTouch time.Time
}

type usageBucket struct {
	bytes     uint64
	requests  uint64
	coldBoots uint64
}

// NewEgressSink returns a sink with the real time clock. Use
// NewEgressSinkWithClock in tests for determinism.
func NewEgressSink() *EgressSink {
	return NewEgressSinkWithClock(time.Now)
}

// NewEgressSinkWithClock is the clock-injectable constructor used
// by tests. now must be non-nil; passing nil panics.
func NewEgressSinkWithClock(now func() time.Time) *EgressSink {
	if now == nil {
		panic("egresssink: now func must not be nil")
	}
	return &EgressSink{
		now:       now,
		instances: make(map[string]*instanceBuckets),
	}
}

// RecordResponseBytes adds n bytes to the (instanceID, currentMinute)
// bucket. Negative or zero values are no-ops; the HTTP path can
// spuriously record 0 for streaming responses whose body is gated
// by Content-Length mismatches, and we should never crash the proxy.
//
// Memory growth: each call updates lastTouch; the EgressSink-level
// eviction sweep runs alongside (see maybeSweep) so a long-idle
// instance row eventually disappears without an explicit
// "instance parked" signal from vmmd or schedd.
//
// Contract: n is int64 but the bucket storage is uint64. The
// n <= 0 guard above is the load-bearing piece — a negative n
// would silently wrap on the uint64 conversion inside this
// function and corrupt the running total. Callers MUST keep that
// guard upstream (or call this function with n >= 0 only).
func (s *EgressSink) RecordResponseBytes(instanceID string, n int64) {
	if instanceID == "" || n <= 0 {
		return
	}
	minute := s.now().UTC().Truncate(time.Minute).Unix()
	// Lock order: s.mu before inst.mu. The drainer (DrainRecords)
	// holds s.mu while iterating and acquires inst.mu per row, so any
	// other path that needs both locks MUST take s.mu first to keep
	// the lock graph acyclic. The previous getOrCreate/then-Lock
	// sequence released s.mu before inst.mu and raced a concurrent
	// drainer's eviction, producing ~4.5% loss in CI's
	// TestRecordResponseBytes_Concurrent under -race.
	s.mu.Lock()
	inst, ok := s.instances[instanceID]
	if !ok {
		inst = &instanceBuckets{usage: make(map[int64]usageBucket, BucketsToKeep)}
		s.instances[instanceID] = inst
	}
	inst.mu.Lock()
	s.mu.Unlock()
	bucket := inst.usage[minute]
	bucket.bytes += uint64(n)
	inst.usage[minute] = bucket
	inst.lastTouch = s.now()
	inst.mu.Unlock()
}

// RecordRequest records one request that reached a selected instance. A cold
// boot is counted only when that request's authoritative wake outcome was a
// cold boot; restores and already-running requests leave coldBoots unchanged.
func (s *EgressSink) RecordRequest(instanceID string, coldBoot bool) {
	if instanceID == "" {
		return
	}
	minute := s.now().UTC().Truncate(time.Minute).Unix()
	s.mu.Lock()
	inst, ok := s.instances[instanceID]
	if !ok {
		inst = &instanceBuckets{usage: make(map[int64]usageBucket, BucketsToKeep)}
		s.instances[instanceID] = inst
	}
	inst.mu.Lock()
	s.mu.Unlock()
	bucket := inst.usage[minute]
	bucket.requests++
	if coldBoot {
		bucket.coldBoots++
	}
	inst.usage[minute] = bucket
	inst.lastTouch = s.now()
	inst.mu.Unlock()
}

// DrainRecords returns a snapshot of every (instanceID, minute)
// bucket currently held by the sink and zeroes them in the same
// critical section. The snapshot is a copy — the caller can hand
// it to a gRPC stream writer (which is itself non-blocking) without
// racing later Records.
//
// Each record carries a YYYY-MM-DDTHH:MM:00Z timestamp equivalent
// (EmittedAt) so the receiver can route the row to the right
// usage_minutes partition even if its own clock has drifted.
//
// A drained instance whose buckets are all zero is dropped from the
// instances map once DrainRecords returns, keeping memory bounded
// even when the caller drains at a sub-minute cadence.
func (s *EgressSink) DrainRecords() []Record {
	now := s.now()
	out := make([]Record, 0)
	sweepCutoff := now.Add(-time.Duration(BucketsToKeep) * time.Minute)
	s.mu.Lock()
	for id, inst := range s.instances {
		inst.mu.Lock()
		// Drop buckets older than the lookback window; without this
		// the per-instance map grows by one entry per minute for
		// the daemon's lifetime, regardless of activity.
		for minute := range inst.usage {
			if minute < sweepCutoff.Unix() {
				delete(inst.usage, minute)
			}
		}
		// Snapshot remaining buckets, then delete them in the same
		// critical section. Deleting (rather than zeroing in place)
		// closes the "concurrent Record between snapshot and zero"
		// race: a Record after snapshot creates a fresh entry on a
		// future minute key, while a Record on the same minute key
		// that races the drain is serialised by the per-instance
		// mutex and lands on a re-inserted entry only after we
		// release. The "missed" Record is bounded by the time
		// meterd takes to drain (one sampler tick ≈ 1 min) and the
		// next drain re-attaches it; the per-minute attribution is
		// preserved because minute keys are immutable.
		var drained uint64
		for minute, bucket := range inst.usage {
			if bucket.bytes == 0 && bucket.requests == 0 && bucket.coldBoots == 0 {
				continue
			}
			out = append(out, Record{
				InstanceID: id,
				Minute:     time.Unix(minute, 0).UTC(),
				Bytes:      bucket.bytes,
				Requests:   bucket.requests,
				ColdBoots:  bucket.coldBoots,
			})
			drained += bucket.bytes + bucket.requests + bucket.coldBoots
		}
		// Wipe the bucket map: a future Record on a different
		// minute-key populates a fresh entry on a different key, so
		// "delete all" is safe. Keeping stale zero entries would
		// defeat the per-instance eviction check below (len > 0
		// even though everything is drained), so the row would
		// leak forever.
		clear(inst.usage)
		empty := drained == 0
		// Stale-instance eviction: a row whose drain returned
		// nothing is cold (per-instance mu is held, so no Record
		// can sneak in). A Record after eviction recreates the row
		// from scratch; the next drain picks it up.
		if empty {
			delete(s.instances, id)
		}
		inst.lastTouch = now
		inst.mu.Unlock()
	}
	s.mu.Unlock()
	return out
}

// Snapshot counts every tracked (instanceID, minute) bucket without
// draining. Used by tests + the integration smoke harness; never on
// the hot path (the meterd consumer drains, it doesn't snapshot).
func (s *EgressSink) Snapshot() []Record {
	s.mu.Lock()
	out := make([]Record, 0, len(s.instances)*BucketsToKeep)
	for id, inst := range s.instances {
		inst.mu.Lock()
		for minute, bucket := range inst.usage {
			if bucket.bytes == 0 && bucket.requests == 0 && bucket.coldBoots == 0 {
				continue
			}
			out = append(out, Record{
				InstanceID: id,
				Minute:     time.Unix(minute, 0).UTC(),
				Bytes:      bucket.bytes,
				Requests:   bucket.requests,
				ColdBoots:  bucket.coldBoots,
			})
		}
		inst.mu.Unlock()
	}
	s.mu.Unlock()
	return out
}

// Tracked returns the number of distinct instance rows the sink
// currently holds. Test seam; not on the hot path.
func (s *EgressSink) Tracked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.instances)
}

// Record is the wire-serialisable tuple the gRPC stream emits. One
// record per (instanceID, minute) bucket — the per-instance grouping
// the consumer (meterd) needs to populate usage_minutes.tx_bytes.
type Record struct {
	InstanceID string    `json:"instance_id"`
	Minute     time.Time `json:"minute"` // truncated to the minute
	Bytes      uint64    `json:"bytes"`  // cumulative within the bucket
	Requests   uint64    `json:"requests"`
	ColdBoots  uint64    `json:"cold_boots"`
}
