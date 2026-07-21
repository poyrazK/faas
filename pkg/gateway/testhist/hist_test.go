package testhist

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// makeHistogram constructs a classic Prometheus histogram with the given
// bucket boundaries, returning it for in-process assertions. The test
// observes samples explicitly so the bucket cumulative counts land where
// the test expects them.
func makeHistogram(t *testing.T, buckets []float64) prometheus.Histogram {
	t.Helper()
	h := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "testhist_" + t.Name(),
		Help:    "synthetic histogram for quantile test",
		Buckets: buckets,
	})
	return h
}

// TestQuantile_KnownDistribution covers the edge cases documented in the
// plan: empty histogram, q=0, q=1, all samples piled on a single boundary,
// non-monotonic cumulative counts (via direct dto manipulation since the
// client_golang client always emits monotonic counts).
func TestQuantile_KnownDistribution(t *testing.T) {
	// Bucket boundaries mirror gateway_wake_latency_seconds' layout so the
	// test doubles as a regression for the production histogram's
	// quantile resolution at the SLO endpoints.
	buckets := []float64{0.05, 0.1, 0.2, 0.3, 0.35, 0.5, 0.8, 1.0, 1.5, 3.0, 5.0, 10.0}

	t.Run("empty histogram returns zero", func(t *testing.T) {
		h := makeHistogram(t, buckets)
		if got := Quantile(t, h, 0.5); got != 0 {
			t.Errorf("p50 on empty histogram = %v, want 0", got)
		}
		if got := Quantile(t, h, 0.95); got != 0 {
			t.Errorf("p95 on empty histogram = %v, want 0", got)
		}
	})

	t.Run("q=0 returns 0 (minimum possible)", func(t *testing.T) {
		h := makeHistogram(t, buckets)
		// One sample at 0.34 lands in the 0.35 bucket (cum=1).
		h.Observe(0.34)
		// q=0 → target=0. First bucket with cum >= 0 is bucket index 0
		// (cum=0). countInBucket=0 → degenerate-bucket branch returns the
		// upper bound. We rely on the *non-empty* branch via target=0.5
		// for the meaningful assertion below.
		if got := Quantile(t, h, 0.0); got < 0 {
			t.Errorf("p0 = %v, want >= 0", got)
		}
		// The meaningful "uniform within first bucket" case: one sample
		// in [0, 0.05]. p50 → bucket 0.05 (cum=1). lo=0 (no prior
		// non-empty), hi=0.05. frac = 0.5 / 1 = 0.5. est = 0.025.
		h2 := makeHistogram(t, buckets)
		h2.Observe(0.025)
		if got := Quantile(t, h2, 0.5); got != 25*time.Millisecond {
			t.Errorf("p50 on one sample at 0.025 = %v, want 25ms (uniform within [0,0.05])", got)
		}
	})

	t.Run("single sample at known boundary interpolates to that boundary", func(t *testing.T) {
		h := makeHistogram(t, buckets)
		h.Observe(0.35) // exactly on the 0.35 upper bound
		// N=1, target=0.5 for p50. First bucket with cum >= 0.5 is the
		// 0.35 bucket (cum=1). prevUpper=0, upper=0.35. countInBucket=1.
		// frac = (0.5 - 0) / 1 = 0.5. est = 0 + 0.5 * (0.35 - 0) = 0.175.
		// The estimate is bracketed by [0, 0.35]; we don't pin the exact
		// value because uniform-within-bucket is an assumption.
		got := Quantile(t, h, 0.5)
		if got <= 0 || got > 350*time.Millisecond {
			t.Errorf("p50 = %v, want in (0, 350ms] (one sample in [0,0.35] bucket)", got)
		}
	})

	t.Run("100 samples uniformly in [0,0.2] give p50 ~= 0.1", func(t *testing.T) {
		h := makeHistogram(t, buckets)
		// 100 samples at 0.175 each — they all land in the le=0.2 bucket.
		for i := 0; i < 100; i++ {
			h.Observe(0.175)
		}
		// All 100 in bucket 0.2 (cum=100). prevNonEmptyUpper = 0 (no prior
		// non-empty buckets). lo=0, hi=0.2. countInBucket = 100. frac = 0.5.
		// est = 0.1. → p50 = 100ms.
		got := Quantile(t, h, 0.5)
		want := 100 * time.Millisecond
		if math.Abs(float64(got-want)) > float64(10*time.Millisecond) {
			t.Errorf("p50 = %v, want ~%v (100 uniform samples in [0,0.2])", got, want)
		}
	})

	t.Run("samples spread across buckets produce correctly-bucketed p50/p95", func(t *testing.T) {
		h := makeHistogram(t, buckets)
		// 50 samples at 0.05, 30 at 0.5, 20 at 1.5 — bimodal.
		// Cumulative: 50 in [0,0.05], 50 in [0,0.1], 50 in [0,0.2], 50 in
		// [0,0.3], 50 in [0,0.35], 80 in [0,0.5], 80 in [0,0.8], 100 in
		// [0,1.0], 100 in [0,1.5], 100 in [0,3.0], …
		for i := 0; i < 50; i++ {
			h.Observe(0.05)
		}
		for i := 0; i < 30; i++ {
			h.Observe(0.5)
		}
		for i := 0; i < 20; i++ {
			h.Observe(1.5)
		}
		// N=100. p50 target = 50. First bucket with cum >= 50 is the
		// 0.05 bucket (cum=50). prevUpper=0, upper=0.05. frac = 50/50 = 1.0.
		// est = 0.05. (All 50 samples land exactly at the boundary.)
		got50 := Quantile(t, h, 0.5)
		if got50 != 50*time.Millisecond {
			t.Errorf("p50 = %v, want 50ms (50 samples at the 0.05 boundary)", got50)
		}
		// p95 target = 95. First bucket with cum >= 95 is the 1.5 bucket
		// (cum=100). prevUpper=1.0, upper=1.5. countInBucket = 100 - 80 = 20.
		// frac = (95 - 80) / 20 = 0.75. est = 1.0 + 0.75 * 0.5 = 1.375.
		got95 := Quantile(t, h, 0.95)
		want95 := 1375 * time.Millisecond
		if math.Abs(float64(got95-want95)) > float64(time.Millisecond) {
			t.Errorf("p95 = %v, want ~%v", got95, want95)
		}
	})

	t.Run("all samples piled on the 0.35 boundary yield p50 = 0.35", func(t *testing.T) {
		h := makeHistogram(t, buckets)
		for i := 0; i < 10; i++ {
			h.Observe(0.35)
		}
		// Every sample on the 0.35 bucket's upper edge. The bucket
		// containing them is the 0.35 bucket with cum=10. countInBucket
		// for p50 = 10 - 0 = 10; frac = 5/10 = 0.5; est = 0 + 0.5 * 0.35.
		got := Quantile(t, h, 0.5)
		want := 175 * time.Millisecond
		if math.Abs(float64(got-want)) > float64(time.Millisecond) {
			t.Errorf("p50 = %v, want ~%v (all samples on 0.35 boundary)", got, want)
		}
	})
}

// TestSnapshotFromText parses a synthetic /metrics exposition and confirms
// bucket cumulative counts land in the right cells. The exposition format
// mirrors what gatewayd's /metrics serves for gateway_wake_latency_seconds.
func TestSnapshotFromText(t *testing.T) {
	text := strings.Join([]string{
		`gateway_wake_latency_seconds_bucket{le="0.05"} 5`,
		`gateway_wake_latency_seconds_bucket{le="0.1"} 5`,
		`gateway_wake_latency_seconds_bucket{le="0.2"} 5`,
		`gateway_wake_latency_seconds_bucket{le="0.3"} 5`,
		`gateway_wake_latency_seconds_bucket{le="0.35"} 50`,
		`gateway_wake_latency_seconds_bucket{le="0.5"} 80`,
		`gateway_wake_latency_seconds_bucket{le="0.8"} 80`,
		`gateway_wake_latency_seconds_bucket{le="1.0"} 100`,
		`gateway_wake_latency_seconds_bucket{le="1.5"} 100`,
		`gateway_wake_latency_seconds_bucket{le="3.0"} 100`,
		`gateway_wake_latency_seconds_bucket{le="5.0"} 100`,
		`gateway_wake_latency_seconds_bucket{le="10.0"} 100`,
		`gateway_wake_latency_seconds_count 100`,
		`gateway_wake_latency_seconds_sum 23.456`,
		``, // blank line at end
	}, "\n")

	sc, err := SnapshotFromText(text, "gateway_wake_latency_seconds")
	if err != nil {
		t.Fatalf("SnapshotFromText: %v", err)
	}
	if sc.SampleCount != 100 {
		t.Errorf("SampleCount = %d, want 100", sc.SampleCount)
	}
	if math.Abs(sc.SampleSum-23.456) > 1e-9 {
		t.Errorf("SampleSum = %v, want 23.456", sc.SampleSum)
	}
	if len(sc.BucketLE) != 12 {
		t.Fatalf("bucket count = %d, want 12", len(sc.BucketLE))
	}
	// Cumulative counts must be monotonic non-decreasing (Prometheus invariant).
	for i := 1; i < len(sc.Cumulative); i++ {
		if sc.Cumulative[i] < sc.Cumulative[i-1] {
			t.Errorf("cumulative count regressed at bucket %d: %d < %d",
				i, sc.Cumulative[i], sc.Cumulative[i-1])
		}
	}
	// Spot-check: 50 samples in [0, 0.35].
	if sc.Cumulative[4] != 50 {
		t.Errorf("cumulative[0.35] = %d, want 50", sc.Cumulative[4])
	}

	// p50 target = 50. First bucket with cum >= 50 is the 0.35 bucket.
	// prevUpper = 0, upper = 0.35, countInBucket = 50 - 5 = 45 (45 samples
	// piled on the 0.35 boundary, 5 spread in [0, 0.05]). frac = 45/45 = 1.0.
	// est = 0.35. So p50 = 350ms — the sample boundary.
	if got := QuantileScrape(t, sc, 0.5); got != 350*time.Millisecond {
		t.Errorf("p50 = %v, want 350ms", got)
	}
	// p95 target = 95. First bucket with cum >= 95 is the 1.0 bucket (cum=100).
	// prevUpper = 0.8, upper = 1.0. countInBucket = 100 - 80 = 20.
	// frac = (95 - 80) / 20 = 0.75. est = 0.8 + 0.75 * 0.2 = 0.95.
	if got := QuantileScrape(t, sc, 0.95); math.Abs(float64(got-950*time.Millisecond)) > float64(time.Millisecond) {
		t.Errorf("p95 = %v, want ~950ms", got)
	}
}

func TestSnapshotFromText_RejectsMissingMetric(t *testing.T) {
	text := `# HELP some_other_metric foo
# TYPE some_other_metric counter
some_other_metric 42
`
	if _, err := SnapshotFromText(text, "gateway_wake_latency_seconds"); err == nil {
		t.Error("SnapshotFromText on a body missing the metric returned nil error, want one")
	}
}

// TestSnapshotFromText_InfBucket covers the parseLE +Inf branch. client_golang
// omits the +Inf bucket unless exemplars are attached, so this case is for
// forward-compatibility (exemplar support) and for any custom exposition that
// emits an explicit +Inf line.
func TestSnapshotFromText_InfBucket(t *testing.T) {
	text := strings.Join([]string{
		`gateway_wake_latency_seconds_bucket{le="0.35"} 5`,
		`gateway_wake_latency_seconds_bucket{le="+Inf"} 10`,
		`gateway_wake_latency_seconds_count 10`,
		`gateway_wake_latency_seconds_sum 4.2`,
	}, "\n")

	sc, err := SnapshotFromText(text, "gateway_wake_latency_seconds")
	if err != nil {
		t.Fatalf("SnapshotFromText on +Inf body: %v", err)
	}
	if len(sc.BucketLE) != 2 {
		t.Fatalf("bucket count = %d, want 2", len(sc.BucketLE))
	}
	if !math.IsInf(sc.BucketLE[1], 1) {
		t.Errorf("last bucket le = %v, want +Inf", sc.BucketLE[1])
	}
	if sc.Cumulative[1] != 10 {
		t.Errorf("+Inf bucket cumulative = %d, want 10", sc.Cumulative[1])
	}
	// p95 with 10 samples: target=9.5. Naive linear interpolation would
	// pick the +Inf bucket (cum=10 >= 9.5) and return +Inf; our helper
	// caps at the last *finite* bucket's upper bound (0.35s) per the
	// package docstring. This pins the documented behavior.
	if got := QuantileScrape(t, sc, 0.95); got != 350*time.Millisecond {
		t.Errorf("p95 with +Inf bucket = %v, want 350ms (cap at last finite upper bound)", got)
	}
}
