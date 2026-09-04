package scaleup

import (
	"sync"
	"time"
)

// RingBuffer is a per-app sliding-window counter. Each app owns a
// slice of `(bucketIdx, count)` tuples; on every Touch the buffer
// reads the latest scrape (app_id → cumulative count) and rolls each
// app's bucket forward by the per-app delta. The window is
// `windowSize` buckets of `bucketSize` each — the canonical config
// is 5 buckets × 1s = 5s window, matching api.ScaleUpWindowSeconds.
//
// The buffer is bounded: at most one entry per app per bucket, so
// the memory cost is O(numApps × windowSize). Cleanup of idle app
// entries is intentionally NOT implemented — apps come and go but
// the count is small (the platform caps at hundreds of apps per
// account, and only apps with autoscale configured feed the buffer
// in steady state).
//
// Concurrency: the buffer is safe for one reader (AppRPS) and one
// writer (Touch) running concurrently. Multiple writers are NOT
// supported — Touch is called from schedd's loop goroutine only.
type RingBuffer struct {
	windowSize int
	bucketSize time.Duration
	// tickInterval is the cadence at which Touch is called. The
	// ring's bucket index is `floor(now / bucketSize) mod windowSize`.
	// The tickInterval is exposed so the trigger can detect a missed
	// tick (e.g. when the loop paused) and force a bucket rotation.
	tickInterval time.Duration

	mu sync.Mutex
	// byApp maps app_id → buffer. Each buffer is a fixed-size slice
	// of (bucketIdx, count) pairs; bucketIdx is the absolute bucket
	// number (for eviction by age). count is the cumulative value
	// seen at that bucket.
	byApp map[string]*appBuffer
}

// appBuffer is the per-app ring buffer. Held by value inside the
// map; the enclosing mutex serialises access.
type appBuffer struct {
	// buckets is the ring. buckets[i].bucket is the absolute bucket
	// number (monotonically increasing). buckets[i].count is the
	// count captured at that bucket's end.
	buckets []bucket
	// lastSeen is the cumulative count from the most recent Touch.
	// Used to compute per-app deltas between ticks.
	lastSeen int64
}

type bucket struct {
	bucket int64 // absolute bucket number (floor(now / bucketSize))
	count  int64 // cumulative count at end of bucket
}

// NewRingBuffer constructs a RingBuffer. windowSize is the number of
// buckets retained (the RPS window); bucketSize is the bucket's
// duration. tickInterval is the cadence at which Touch will be
// called (used defensively to force bucket rotation when the loop
// pauses).
func NewRingBuffer(windowSize int, bucketSize, tickInterval time.Duration) *RingBuffer {
	if windowSize <= 0 {
		windowSize = 1
	}
	if bucketSize <= 0 {
		bucketSize = time.Second
	}
	if tickInterval <= 0 {
		tickInterval = time.Second
	}
	return &RingBuffer{
		windowSize:   windowSize,
		bucketSize:   bucketSize,
		tickInterval: tickInterval,
		byApp:        map[string]*appBuffer{},
	}
}

// Touch folds a fresh scrape into the per-app ring. The cumulative
// count per app is delta'd against the previous Touch; the delta is
// added to the current bucket. Buckets older than `windowSize` are
// evicted.
//
// Touch is safe for one concurrent caller (the loop's tick goroutine).
// Multiple writers are not synchronised — the trigger is the only
// owner.
func (r *RingBuffer) Touch(now time.Time, counts map[string]int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	currentBucket := now.UnixNano() / int64(r.bucketSize)
	for appID, cumulative := range counts {
		buf, ok := r.byApp[appID]
		if !ok {
			// First observation: seed the bucket with delta=0
			// and lastSeen=cumulative. Seeding with the raw
			// cumulative value would inflate the first window
			// (the bucket's count is what AppRPS sums) — if
			// gatewayd-internal has already served N requests when the
			// trigger is enabled mid-flight, AppRPS would report
			// N/conc for the first 5 ticks. The next Touch
			// computes a real delta against this seeded
			// lastSeen, so the first delta tick is correct
			// (just one tick delayed).
			r.byApp[appID] = &appBuffer{
				buckets:  []bucket{{bucket: currentBucket, count: 0}},
				lastSeen: cumulative,
			}
			continue
		}
		// Compute per-tick delta. Negative deltas gatewayd-internal
		// restart) signal a fresh boot — the cumulative
		// counter is per-process and resets to 0 on restart.
		// Treat the regression as a fresh start: clear the
		// per-app ring (otherwise the old bucket counts
		// would inflate the trigger's notion of "current
		// traffic" for the next windowSize seconds) and
		// seed the new boot with lastSeen=cumulative but a
		// delta=0 bucket — same cold-boot guard as the
		// first-observation branch. The first delta tick
		// after the restart will be measured from the new
		// boot's perspective.
		if cumulative < buf.lastSeen {
			buf.buckets = []bucket{{bucket: currentBucket, count: 0}}
			buf.lastSeen = cumulative
			continue
		}
		delta := cumulative - buf.lastSeen
		buf.lastSeen = cumulative
		// Append to the current bucket or roll forward.
		last := len(buf.buckets) - 1
		if last >= 0 && buf.buckets[last].bucket == currentBucket {
			buf.buckets[last].count += delta
		} else {
			buf.buckets = append(buf.buckets, bucket{
				bucket: currentBucket,
				count:  delta,
			})
		}
		// Evict buckets older than the window. The window is
		// `windowSize` buckets inclusive of the current one
		// (mirrors AppRPS's `>= cutoff`): buckets strictly
		// less than `currentBucket - windowSize + 1` are stale.
		cutoff := currentBucket - int64(r.windowSize) + 1
		first := 0
		for first < len(buf.buckets) && buf.buckets[first].bucket < cutoff {
			first++
		}
		if first > 0 {
			buf.buckets = buf.buckets[first:]
		}
	}
}

// AppRPS returns the per-app request-count sum of all buckets that fall
// inside the window. It is retained as a count accessor for diagnostics and
// tests; callers that need a true rate should use AppRate. Returns 0 when the
// buffer has no observation for appID.
func (r *RingBuffer) AppRPS(appID string, now time.Time) float64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	buf, ok := r.byApp[appID]
	if !ok || len(buf.buckets) == 0 {
		return 0
	}
	currentBucket := now.UnixNano() / int64(r.bucketSize)
	// Window is windowSize buckets inclusive of current. Keep
	// buckets where `bucket >= currentBucket - windowSize + 1`.
	cutoff := currentBucket - int64(r.windowSize) + 1
	var sum int64
	for _, b := range buf.buckets {
		if b.bucket >= cutoff {
			sum += b.count
		}
	}
	return float64(sum)
}

// AppRate returns the per-app requests/second over the configured sliding
// window. Keeping the normalization here makes all scale-up callers use the
// same units regardless of whether the source is Prometheus or VMMD stats.
func (r *RingBuffer) AppRate(appID string, now time.Time) float64 {
	if r == nil {
		return 0
	}
	windowSeconds := float64(r.windowSize) * r.bucketSize.Seconds()
	if windowSeconds <= 0 {
		return 0
	}
	return r.AppRPS(appID, now) / windowSeconds
}

// HasObservation reports whether Touch has ever accepted a cumulative
// counter for appID. The trigger uses this to distinguish a real zero-rate
// observation from an unavailable Prometheus signal and can then fall back
// to the VMMD stats reader.
func (r *RingBuffer) HasObservation(appID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	buf, ok := r.byApp[appID]
	return ok && buf != nil && len(buf.buckets) > 0
}
