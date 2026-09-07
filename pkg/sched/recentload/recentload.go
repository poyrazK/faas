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

// RequestRateReader supplies the provider-independent app request rates
// derived from VMMD telemetry. It is the scale-down fallback when a split-box
// scheduler cannot scrape one local gateway metrics endpoint.
type RequestRateReader interface {
	RequestRatesPerSecondAt(now time.Time) map[string]float64
}

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

type rateWindow struct {
	buckets []rateBucket
}

type rateBucket struct {
	bucket int64
	rate   float64
}

// RecentLoad is the per-app RPS mirror.
type RecentLoad struct {
	mu         sync.Mutex
	windowSize int
	bucketSize time.Duration
	scraper    PromScraper
	rateReader RequestRateReader
	byApp      map[string]*appWindow
	rateByApp  map[string]*rateWindow
}

// New constructs the mirror. windowSize is the number of buckets
// retained (the RPS window); bucketSize is each bucket's duration.
// Production wires 5 × 1s = 5s — matches api.ScaleUpWindowSeconds so
// the reaper's notion of "recent" matches the scale-up trigger's.
// scraper may be nil when a RequestRateReader is attached. With neither
// source, Touch is a no-op and the reaper defers to ReapIdle.
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
		rateByApp:  map[string]*rateWindow{},
	}
}

// WithRateReader attaches the VMMD telemetry fallback. Production wires the
// scheduler's instance-stats reader even when a gateway scraper is available;
// fresh gateway samples remain preferred, and telemetry is used only when the
// cumulative gateway signal has no current observation.
func (r *RecentLoad) WithRateReader(reader RequestRateReader) *RecentLoad {
	if r != nil {
		r.rateReader = reader
	}
	return r
}

// Touch folds the available gateway counter scrape and VMMD rate snapshot
// into per-app rings. Nil-safe: a nil receiver returns immediately. A failed
// source leaves its previous ring intact; the other source can still update.
// The reaper's 1s ticker drives this; the reaper's decision cadence is 10s,
// so Touch runs ~10x more often than the decision reads the mirror.
//
// The deltas are computed against the cumulative value seen at the
// previous Touch. A regression (cumulative lower than lastSeen)
// means gatewayd-internal restarted and reset its counter — treat it as a
// fresh start: clear the per-app ring so the new window's first
// delta is measured from the new boot's perspective. This mirrors
// the scaleup.RingBuffer's restart handling.
func (r *RecentLoad) Touch(ctx context.Context, now time.Time) {
	if r == nil {
		return
	}
	if r.scraper != nil {
		counts, err := r.scraper.Scrape(ctx)
		if err == nil {
			r.touchCounts(now, counts)
		}
	}
	if r.rateReader != nil {
		r.touchRates(now, r.rateReader.RequestRatesPerSecondAt(now))
	}
}

func (r *RecentLoad) touchCounts(now time.Time, counts map[string]int64) {
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

func (r *RecentLoad) touchRates(now time.Time, rates map[string]float64) {
	if len(rates) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	currentBucket := now.UnixNano() / int64(r.bucketSize)
	cutoff := currentBucket - int64(r.windowSize) + 1
	for appID, rate := range rates {
		if appID == "" || rate < 0 {
			continue
		}
		window := r.rateByApp[appID]
		if window == nil {
			window = &rateWindow{}
			r.rateByApp[appID] = window
		}
		last := len(window.buckets) - 1
		if last >= 0 && window.buckets[last].bucket == currentBucket {
			window.buckets[last].rate = rate
		} else {
			window.buckets = append(window.buckets, rateBucket{bucket: currentBucket, rate: rate})
		}
		first := 0
		for first < len(window.buckets) && window.buckets[first].bucket < cutoff {
			first++
		}
		if first > 0 {
			window.buckets = window.buckets[first:]
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
	rate, _ := r.RecentRateWithSignal(appID, now)
	return rate
}

// RecentRateWithSignal returns the current rolling rate and whether a fresh
// observation exists. A real zero rate is therefore distinct from an absent
// signal: zero may safely drive scale-in, while absence must defer to the idle
// reaper. Fresh gateway counter deltas are preferred; VMMD rate samples are
// averaged over the same rolling window when the gateway signal is absent.
func (r *RecentLoad) RecentRateWithSignal(appID string, now time.Time) (float64, bool) {
	if r == nil || appID == "" {
		return 0, false
	}
	windowSeconds := float64(r.windowSize) * r.bucketSize.Seconds()
	if windowSeconds <= 0 {
		return 0, false
	}
	currentBucket := now.UnixNano() / int64(r.bucketSize)
	cutoff := currentBucket - int64(r.windowSize) + 1
	r.mu.Lock()
	defer r.mu.Unlock()
	if window := r.byApp[appID]; window != nil {
		var sum int64
		observed := false
		for _, b := range window.buckets {
			if b.bucket >= cutoff {
				sum += b.count
				observed = true
			}
		}
		if observed {
			return float64(sum) / windowSeconds, true
		}
	}
	if window := r.rateByApp[appID]; window != nil {
		var sum float64
		var samples int
		for _, b := range window.buckets {
			if b.bucket >= cutoff {
				sum += b.rate
				samples++
			}
		}
		if samples > 0 {
			return sum / float64(samples), true
		}
	}
	return 0, false
}

// RecentDesiredReplicas returns ceil(recent_rps / targetRPS) for appID.
// targetRPS == 0 returns 0 — the reaper treats that as "no target
// configured" and defers to the existing ReapIdle path. The reaper's
// ReapAggressive then ignores apps absent from the desiredByApp map.
//
// When the mirror has no observation for appID this also returns 0. Callers
// that make scale-down decisions must use RecentDesiredReplicasWithSignal so
// they can distinguish that absence from a measured zero traffic rate.
func (r *RecentLoad) RecentDesiredReplicas(appID string, now time.Time, targetRPS int) int {
	desired, _ := r.RecentDesiredReplicasWithSignal(appID, now, targetRPS)
	return desired
}

// RecentDesiredReplicasWithSignal preserves the observation bit used by the
// reaper. This prevents an unavailable gateway and unavailable node telemetry
// from being interpreted as a real traffic drop to zero.
func (r *RecentLoad) RecentDesiredReplicasWithSignal(appID string, now time.Time, targetRPS int) (int, bool) {
	if r == nil || targetRPS <= 0 {
		return 0, false
	}
	rps, observed := r.RecentRateWithSignal(appID, now)
	if !observed || rps <= 0 {
		return 0, observed
	}
	return int(math.Ceil(rps / float64(targetRPS))), true
}
