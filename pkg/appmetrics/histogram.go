package appmetrics

import (
	"math"
	"sort"
	"strconv"
)

// HistogramQuantile computes PromQL histogram_quantile() for a single
// q against an (app, le) → float64 bucket map produced by
// promql.Client.QueryBuckets. Empty / nil maps and all-zero bucket
// families return 0 (matches the per-app handler's appmetrics.SafeFloat
// coercion of NaN from PromQL).
//
// Semantics (matches PromQL histogram_quantile):
//   - skip the +Inf bucket (it would otherwise return +Inf for q<1)
//   - find the first bucket whose cumulative ≥ q·N
//   - interpolate linearly in (prevNonEmptyUpper, upper) by count ratio
//
// NaN / +Inf from the upstream vector query surface as 0 here too
// (PromQL emits the literal string "NaN" / "+Inf" in the value field,
// which strconv.ParseFloat parses; we trust appmetrics.SafeFloat on the outer
// side to clamp).
func HistogramQuantile(q float64, buckets map[string]float64) float64 {
	if len(buckets) == 0 {
		return 0
	}
	type entry struct {
		le  float64
		cum float64
	}
	parsed := make([]entry, 0, len(buckets))
	var total float64
	for leStr, v := range buckets {
		le, err := parseLE(leStr)
		if err != nil {
			continue
		}
		parsed = append(parsed, entry{le: le, cum: v})
	}
	if len(parsed) == 0 {
		return 0
	}
	sort.Slice(parsed, func(i, j int) bool { return parsed[i].le < parsed[j].le })
	for _, p := range parsed {
		if p.cum > total {
			total = p.cum
		}
	}
	if total <= 0 {
		return 0
	}
	target := q * total
	// Skip +Inf — cap at the last finite bucket (matches PromQL).
	lastFinite := -1
	for i, p := range parsed {
		if !math.IsInf(p.le, 1) {
			lastFinite = i
		}
	}
	if lastFinite < 0 {
		return 0
	}
	var prevUpper, prevCum float64
	for i := 0; i <= lastFinite; i++ {
		p := parsed[i]
		if p.cum >= target {
			countInBucket := p.cum - prevCum
			if countInBucket <= 0 {
				return p.le
			}
			frac := (target - prevCum) / countInBucket
			return prevUpper + frac*(p.le-prevUpper)
		}
		if p.cum > 0 {
			prevUpper = p.le
		}
		prevCum = p.cum
	}
	return prevUpper
}

// parseLE parses a Prometheus le="…" string value into a float64.
// Special-cases "+Inf" (PromQL's stringified infinity) and rejects
// "-Inf" / "NaN" / malformed input. Used only by HistogramQuantile
// to keep the bucket walk strict.
func parseLE(s string) (float64, error) {
	if s == "+Inf" {
		return math.Inf(1), nil
	}
	return strconv.ParseFloat(s, 64)
}
