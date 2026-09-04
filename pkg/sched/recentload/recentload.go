// Package recentload holds the per-app rolling-window RPS signal that
// drives the aggressive reaper scale-down path (issue #171). The
// signal is intentionally independent from pkg/sched/scaleup.RingBuffer:
// the scale-up trigger has its own private ring, and the reaper must
// not couple to a future scaleup refactor. The mirror here consumes the
// same scaleup.PromScraper interface so we don't duplicate scraping.
//
// Concurrency: Touch is called from one goroutine (schedd's loop on a
// 1s ticker). RecentRPS / RecentDesiredReplicas are called from the
// reaper tick (also one goroutine). The mutex is one r/w pair and is
// uncontended in steady state.
package recentload

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/sched/scaleup"
)

// PromScraper is the same interface the scale-up trigger uses. Aliased
// here so both packages share a single contract (a future "single
// connection" optimization to gateway /metrics is easier when the
// scraper surface is one type).
type PromScraper = scaleup.PromScraper

// PromScraperFunc is the closure adapter (mirrors scaleup.PromScraperFunc).
type PromScraperFunc = scaleup.PromScraperFunc

// appWindow is the per-app rolling counter. One appWindow holds a
// bounded slice of (bucketIdx, count) tuples; the reaper reads the
// sum across the trailing windowSize buckets at evaluation time.
type appWindow struct {
	buckets  []bucket
	lastSeen int64
}

// bucket is one second's worth of per-app request deltas. bucketIdx
// is the absolute bucket number so eviction by age is exact even
// across long pauses in the loop.
type bucket struct {
	bucket int64
	count  int64
}

// RecentLoad is the per-app RPS mirror.
type RecentLoad struct {
	mu         sync.Mutex
	windowSize int
	bucketSize time.Duration
	scraper    PromScraper
	byApp      map[string]*appWindow
}

// New constructs the mirror. windowSize is the number of buckets
// retained (the RPS window); bucketSize is each bucket's duration.
// Production wires 5 × 1s = 5s — matches api.ScaleUpWindowSeconds so
// the reaper's notion of "recent" matches the scale-up trigger's.
// scraper may be nil; Touch becomes a no-op (no signal means the
// reaper sees desired=0 everywhere and defers to ReapIdle).
func New(scraper PromScraper, windowSize int, bucketSize time.Duration) *RecentLoad {
	if windowSize <= 0 {
		windowSize = 1
	}
	if bucketSize <= 0 {
		bucketSize = time.Second
	}
	return &RecentLoad{
		windowSize: windowSize,
		bucketSize: bucketSize,
		scraper:    scraper,
		byApp:      map[string]*appWindow{},
	}
}

// Touch folds one scrape into the per-app rings. Nil-safe: a nil
// receiver or a nil scraper returns immediately, leaving the previous
// ring intact. The reaper's 1s ticker drives this; the reaper's
// decision cadence is 10s, so Touch runs ~10x more often than the
// decision reads the mirror.
//
// The deltas are computed against the cumulative value seen at the
// previous Touch. A regression (cumulative lower than lastSeen)
// means gatewayd-internal restarted and reset its counter — treat it as a
// fresh start: clear the per-app ring so the new window's first
// delta is measured from the new boot's perspective. This mirrors
// the scaleup.RingBuffer's restart handling.
func (r *RecentLoad) Touch(ctx context.Context, now time.Time) {
	if r == nil || r.scraper == nil {
		return
	}
	counts, err := r.scraper.Scrape(ctx)
	if err != nil {
		// Degraded mode: keep the previous ring. The reaper sees
		// stale-but-non-zero values rather than zero, which is the
		// safer direction (an aggressive park on a scrape error
		// would be wrong). Same shape as scaleup.Trigger.Tick.
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	currentBucket := now.UnixNano() / int64(r.bucketSize)
	for appID, cumulative := range counts {
		buf, ok := r.byApp[appID]
		if !ok {
			r.byApp[appID] = &appWindow{
				buckets:  []bucket{{bucket: currentBucket, count: 0}},
				lastSeen: cumulative,
			}
			continue
		}
		if cumulative < buf.lastSeen {
			buf.buckets = []bucket{{bucket: currentBucket, count: 0}}
			buf.lastSeen = cumulative
			continue
		}
		delta := cumulative - buf.lastSeen
		buf.lastSeen = cumulative
		last := len(buf.buckets) - 1
		if last >= 0 && buf.buckets[last].bucket == currentBucket {
			buf.buckets[last].count += delta
		} else {
			buf.buckets = append(buf.buckets, bucket{
				bucket: currentBucket,
				count:  delta,
			})
		}
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

// RecentRPS returns the windowed sum for appID. Returns 0 when the
// mirror has no observation for appID or has never been Touched.
// Reaper compares the sum against the per-app target; per-instance
// RPS is the reaper's responsibility (it knows the running count).
func (r *RecentLoad) RecentRPS(appID string, now time.Time) float64 {
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
	cutoff := currentBucket - int64(r.windowSize) + 1
	var sum int64
	for _, b := range buf.buckets {
		if b.bucket >= cutoff {
			sum += b.count
		}
	}
	return float64(sum)
}

// RecentRate returns requests/second over the configured sliding window.
// RecentRPS intentionally remains the raw window-count accessor for the
// existing diagnostics surface; autoscale replica math uses this normalized
// method so its target is genuinely expressed in requests/second.
func (r *RecentLoad) RecentRate(appID string, now time.Time) float64 {
	if r == nil {
		return 0
	}
	windowSeconds := float64(r.windowSize) * r.bucketSize.Seconds()
	if windowSeconds <= 0 {
		return 0
	}
	return r.RecentRPS(appID, now) / windowSeconds
}

// RecentDesiredReplicas returns ceil(recent_rps / targetRPS) for appID.
// targetRPS == 0 returns 0 — the reaper treats that as "no target
// configured" and defers to the existing ReapIdle path. The reaper's
// ReapAggressive then ignores apps absent from the desiredByApp map.
//
// When the mirror has no observation for appID yet (cold path, no
// scrape has run since schedd started) this also returns 0 — the
// reaper must not aggressively park an app that simply hasn't been
// scraped yet. The existing ReapIdle timeout handles that case.
func (r *RecentLoad) RecentDesiredReplicas(appID string, now time.Time, targetRPS int) int {
	if r == nil || targetRPS <= 0 {
		return 0
	}
	rps := r.RecentRate(appID, now)
	if rps <= 0 {
		return 0
	}
	return int(math.Ceil(rps / float64(targetRPS)))
}
