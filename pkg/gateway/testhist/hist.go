// Package testhist provides histogram-quantile helpers for in-test use.
//
// Quantile implements the same linear-interpolation rules as PromQL's
// histogram_quantile() function: it walks the bucket cumulative counts
// (which are inclusive of the upper bound per Prometheus `le=` semantics),
// finds the first bucket whose cumulative count ≥ q·N, and interpolates
// linearly within [prevUpper, upper] by the ratio of counts.
//
// The +Inf bucket is skipped: client_golang omits it from the dto unless
// exemplars are attached, but if it's present we treat it as
// rate-calculation-only and cap the walk at the largest finite `le` (matches
// PromQL itself — see
// https://github.com/prometheus/prometheus/blob/main/promql/quantile.go).
// This means a q < 1 always returns a finite time.Duration.
//
// This package is a sibling to pkg/gateway; the helpers are reused by
// pkg/gateway unit tests AND by cmd/e2e/deploy_wake_metal_test.go, which
// can't reach into pkg/gateway's unexported fields.
//
// The Quantile function reads the histogram in-process via the standard
// Prometheus dto. cmd/e2e tests that need to read a histogram living in
// a different process (gatewayd running as a subprocess) should scrape
// the /metrics exposition with SnapshotFromText instead.
package testhist

import (
	"bufio"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Snapshot reads the histogram's full dto in a single Write() call. Callers
// that want to walk the bucket cumulative counts directly (rather than
// reduce to a quantile) should use this.
//
// Empty histograms return a non-nil dto.Histogram with SampleCount == 0.
func Snapshot(t *testing.T, h prometheus.Histogram) *dto.Histogram {
	t.Helper()
	m := &dto.Metric{}
	if err := h.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("testhist.Snapshot: histogram write: %v", err)
	}
	if m.Histogram == nil {
		return &dto.Histogram{}
	}
	return m.Histogram
}

// Quantile returns the q-quantile (0 ≤ q ≤ 1) of h via PromQL
// histogram_quantile() semantics. Empty histograms return 0; non-monotonic
// cumulative counts are a t.Fatal because they indicate an emitter bug.
//
// Quantile matches the production dashboard's behavior (cmd/apid/status.go
// uses `histogram_quantile(0.95, sum(rate(..._bucket[5m])) by (le))`), so
// test assertions on Quantile(0.95) and dashboard p95 stay in lock-step.
func Quantile(t *testing.T, h prometheus.Histogram, q float64) time.Duration {
	t.Helper()
	if q < 0 || q > 1 {
		t.Fatalf("testhist.Quantile: q=%v, want in [0,1]", q)
	}
	dtoHist := Snapshot(t, h)
	if dtoHist.GetSampleCount() == 0 {
		return 0
	}
	les, cums := dtoBucketBounds(dtoHist)
	return quantileFromBuckets(t, float64(dtoHist.GetSampleCount()), q,
		les, cums, dtoHist.GetSampleSum())
}

// SnapshotFromText parses a Prometheus text exposition (the format
// /metrics serves) for the named histogram and returns its bucket
// cumulative counts, total count, and total sum. This is the scrape-time
// counterpart to Snapshot, intended for cmd/e2e tests that need to read
// a histogram living in a subprocess (gatewayd).
//
// metricName must match exactly (no regex). Returns an error if the
// histogram is not present or its bucket lines are missing/malformed.
type ScrapeHistogram struct {
	Name        string
	BucketLE    []float64
	Cumulative  []uint64
	SampleCount uint64
	SampleSum   float64
}

// SnapshotFromText scrapes a single histogram from a /metrics body. The
// histogram is identified by exact metric name; unlabelled histograms
// (the only kind this package currently supports) match lines with no
// label block.
func SnapshotFromText(text, metricName string) (*ScrapeHistogram, error) {
	sc := &ScrapeHistogram{Name: metricName}
	// Bucket lines: "<name>_bucket{le="<bound>"} <cumulative>"
	// Float (or +Inf) for the bound; integer cumulative count.
	bucketRe := regexp.MustCompile(`^` + regexp.QuoteMeta(metricName) + `_bucket\{le="([^"]+)"\}\s+(\d+)$`)
	countRe := regexp.MustCompile(`^` + regexp.QuoteMeta(metricName) + `_count\s+(\d+)$`)
	sumRe := regexp.MustCompile(`^` + regexp.QuoteMeta(metricName) + `_sum\s+([\d.eE+-]+)$`)

	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if m := bucketRe.FindStringSubmatch(line); m != nil {
			le, err := parseLE(m[1])
			if err != nil {
				return nil, fmt.Errorf("%s: parse le=%q: %w", metricName, m[1], err)
			}
			cum, err := strconv.ParseUint(m[2], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: parse cumulative %q: %w", metricName, m[2], err)
			}
			sc.BucketLE = append(sc.BucketLE, le)
			sc.Cumulative = append(sc.Cumulative, cum)
			continue
		}
		if m := countRe.FindStringSubmatch(line); m != nil {
			c, err := strconv.ParseUint(m[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: parse _count %q: %w", metricName, m[1], err)
			}
			sc.SampleCount = c
			continue
		}
		if m := sumRe.FindStringSubmatch(line); m != nil {
			s, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: parse _sum %q: %w", metricName, m[1], err)
			}
			sc.SampleSum = s
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: scan: %w", metricName, err)
	}
	if sc.SampleCount == 0 && len(sc.BucketLE) == 0 {
		return nil, fmt.Errorf("%s: not found in exposition", metricName)
	}
	return sc, nil
}

// QuantileScrape returns the q-quantile of a previously-scraped histogram.
// Same semantics as Quantile; this variant operates on ScrapeHistogram so
// callers can scrape once and ask for multiple quantiles (p50 + p95) without
// re-scraping.
//
// Returns 0 for an empty histogram (SampleCount == 0).
func QuantileScrape(t *testing.T, sc *ScrapeHistogram, q float64) time.Duration {
	t.Helper()
	if q < 0 || q > 1 {
		t.Fatalf("testhist.QuantileScrape: q=%v, want in [0,1]", q)
	}
	if sc == nil || sc.SampleCount == 0 {
		return 0
	}
	return quantileFromBuckets(t, float64(sc.SampleCount), q,
		sc.BucketLE, sc.Cumulative, sc.SampleSum)
}

// quantileFromBuckets is the shared core for Quantile and QuantileScrape:
// walk cumulative counts in increasing upper-bound order, find the first
// bucket whose cum ≥ q·N, interpolate linearly within [prevNonEmptyUpper,
// upper] by the count ratio. Returns 0 for empty input.
//
// `prevNonEmptyUpper` (not the naive prevUpper) is critical: carrying the
// previous bucket's upper bound forward through empty buckets would inflate
// lo and skew quantiles inside the first non-empty bucket. E.g. for samples
// piled in the le=0.2 bucket, naive prevUpper would land at 0.1 instead of
// 0, yielding p50 = 150ms instead of 100ms.
//
// callerIdent is a short label baked into t.Fatalf messages so a regression
// in either call site points at the right function in the test log.
func quantileFromBuckets(t *testing.T, n float64, q float64,
	les []float64, cums []uint64, sampleSum float64) time.Duration {
	t.Helper()
	if n <= 0 || len(les) == 0 {
		// Defensive: a histogram with samples but no buckets is exotic
		// (only possible with a custom prometheus.Histogram implementation).
		// Return the mean as a fallback.
		return time.Duration(sampleSum / n * float64(time.Second))
	}
	target := q * n

	// Find the last *finite* bucket's index. The +Inf bucket, if present,
	// is the final cumulative count and would otherwise be the "winning"
	// bucket for any q < 1, returning math.Inf(1) — meaningless as a
	// time.Duration. PromQL itself uses the largest finite le as the cap
	// (https://github.com/prometheus/prometheus/blob/main/promql/quantile.go).
	// We mirror that by stopping the walk at the last finite bucket and
	// using its upper bound as the cap if no earlier bucket matched.
	lastFinite := -1
	for i, upper := range les {
		if !math.IsInf(upper, 1) {
			lastFinite = i
		}
	}

	var prevNonEmptyUpper float64
	var prevCount float64
	for i, upper := range les {
		if i > lastFinite {
			break
		}
		cum := float64(cums[i])
		if cum < prevCount {
			t.Fatalf("testhist: non-monotonic cumulative count at bucket %d (%.0f < %.0f)",
				i, cum, prevCount)
		}
		if cum >= target {
			lo := prevNonEmptyUpper
			hi := upper
			countInBucket := cum - prevCount
			if countInBucket <= 0 {
				// All samples piled on the boundary — return the upper
				// bound. Matches PromQL behavior on degenerate buckets.
				return time.Duration(upper * float64(time.Second))
			}
			frac := (target - prevCount) / countInBucket
			est := lo + frac*(hi-lo)
			return time.Duration(est * float64(time.Second))
		}
		// Don't advance prevNonEmptyUpper through empty buckets — only
		// update it once we've seen at least one sample (cum > 0).
		if cum > 0 {
			prevNonEmptyUpper = upper
		}
		prevCount = cum
	}
	// q*N exceeds the largest finite bucket's cumulative count — the quantile
	// lies above the highest finite upper bound. Return that upper bound.
	// (PromQL returns +Inf here, but our caller wants a number; capping at
	// the last finite bucket matches "p95 of a bounded distribution".)
	return time.Duration(prevNonEmptyUpper * float64(time.Second))
}

// dtoBucketBounds extracts (le, cumulative) slices from a histogram dto in
// increasing upper-bound order. Used by Quantile to adapt the dto shape to
// the bucket-walker in quantileFromBuckets.
func dtoBucketBounds(h *dto.Histogram) ([]float64, []uint64) {
	buckets := h.GetBucket()
	les := make([]float64, len(buckets))
	cums := make([]uint64, len(buckets))
	for i, b := range buckets {
		les[i] = b.GetUpperBound()
		cums[i] = b.GetCumulativeCount()
	}
	return les, cums
}

// parseLE parses a Prometheus le="…" value. Special-cases "+Inf" to
// math.Inf(1); everything else parses as a float.
func parseLE(s string) (float64, error) {
	if s == "+Inf" {
		return math.Inf(1), nil
	}
	return strconv.ParseFloat(s, 64)
}
