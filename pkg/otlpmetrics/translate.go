// Package otlpmetrics translates OTLP metric exports into ADR-202/ADR-745
// custom metric writes. It holds no I/O so the mapping rules are testable on
// their own; apid does the authentication and storage.
package otlpmetrics

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Point is one metric value to store for a normalised name. A delta point's
// Value is added to the stored value (ADR-20261108160000); any other point
// replaces it. A point with Labels is one series of its name (slice C), and
// SeriesKey is the labels' canonical key ("" for the unlabelled series).
type Point struct {
	Name       string
	Kind       string
	Value      float64
	ObservedAt time.Time
	Delta      bool
	Labels     map[string]string
	SeriesKey  string
}

// Result is the translation outcome. Rejected counts data points that were
// not stored; Reason describes the first rejection for the OTLP partial
// success message.
type Result struct {
	Points   []Point
	Rejected int64
	Reason   string
}

// Histogram series suffixes (ADR-20261108160000): a histogram named h is
// stored as h_count and h_sum (counters) and h_p50, h_p95 and h_p99 (gauges
// estimated from the buckets).
const (
	HistogramCountSuffix = "_count"
	HistogramSumSuffix   = "_sum"
)

// HistogramQuantiles are the percentiles derived from a histogram, with the
// suffix each is stored under.
var HistogramQuantiles = []struct {
	Suffix string
	Q      float64
}{{"_p50", 0.50}, {"_p95", 0.95}, {"_p99", 0.99}}

// histogramMaxBaseBytes leaves room for the longest suffix (_count) within
// the custom metric name bound.
const histogramMaxBaseBytes = api.CustomMetricNameMaxBytes - len(HistogramCountSuffix)

// Translate applies the ADR-745 rules as amended by ADR-20261108160000:
//   - gauges and cumulative non-monotonic sums (up-down counters) are stored
//     as gauges, cumulative monotonic sums as counters;
//   - delta monotonic sums are added to a counter's running total, and delta
//     non-monotonic sums to a gauge (floored at zero by the store);
//   - explicit-bucket histograms become _count and _sum counters (set when
//     cumulative, added when delta) and _p50/_p95/_p99 gauges estimated from
//     the buckets of the newest data point;
//   - exponential histograms and summaries are rejected;
//   - data point attributes become labels (slice C): at most
//     api.CustomMetricMaxLabels scalar attributes per point, each distinct
//     label set a series of the name; other attribute shapes are rejected;
//   - negative or non-finite values are rejected;
//   - names are lowercased with '.', '-' and '/' mapped to '_' and must then
//     satisfy the custom metric name rules;
//   - the newest data point per series wins, except that delta points of one
//     series are summed.
//
// Points come back ordered by name, then series key.
//
// now stamps points that carry no timestamp.
func Translate(req *colmetricspb.ExportMetricsServiceRequest, now time.Time) Result {
	t := &translation{now: now, points: map[string]Point{}}
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				t.metric(m)
			}
		}
	}
	for _, p := range t.points {
		t.res.Points = append(t.res.Points, p)
	}
	sort.Slice(t.res.Points, func(i, j int) bool {
		a, b := t.res.Points[i], t.res.Points[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.SeriesKey < b.SeriesKey
	})
	return t.res
}

// series identifies one point slot: a name and a label set.
type series struct {
	name   string
	key    string
	labels map[string]string
}

func (s series) id() string { return s.name + "\x00" + s.key }

func (s series) suffixed(suffix string) series {
	return series{name: s.name + suffix, key: s.key, labels: s.labels}
}

type translation struct {
	now    time.Time
	points map[string]Point
	res    Result
}

func (t *translation) reject(n int, reason string) {
	t.res.Rejected += int64(n)
	if t.res.Reason == "" {
		t.res.Reason = reason
	}
}

// at is a data point's time: now when unset. A time out of range or further
// ahead than CustomMetricMaxFutureSkewSeconds is refused (ok false): it would
// keep a stopped pusher's value fresh until then (ADR-20261031144636).
func (t *translation) at(ts uint64) (time.Time, bool) {
	if ts == 0 {
		return t.now, true
	}
	if ts > math.MaxInt64 {
		return time.Time{}, false
	}
	at := time.Unix(0, int64(ts)).UTC()
	if at.After(t.now.Add(api.CustomMetricMaxFutureSkewSeconds * time.Second)) {
		return time.Time{}, false
	}
	if at.After(t.now) {
		at = t.now
	}
	return at, true
}

// pointAt is at, rejecting the point when its time is refused.
func (t *translation) pointAt(original string, ts uint64) (time.Time, bool) {
	at, ok := t.at(ts)
	if !ok {
		t.reject(1, fmt.Sprintf("metric %q: timestamps must not be more than %ds in the future", original, api.CustomMetricMaxFutureSkewSeconds))
	}
	return at, ok
}

// set records a value that replaces the stored one; the newest wins.
func (t *translation) set(s series, kind string, value float64, at time.Time) {
	if prev, ok := t.points[s.id()]; ok && !prev.Delta && !at.After(prev.ObservedAt) {
		return
	}
	t.points[s.id()] = Point{Name: s.name, Kind: kind, Value: value, ObservedAt: at, Labels: s.labels, SeriesKey: s.key}
}

// add records a value added to the stored one; deltas of a series are summed.
func (t *translation) add(s series, kind string, value float64, at time.Time) {
	prev, ok := t.points[s.id()]
	if ok && prev.Delta {
		prev.Value += value
		if at.After(prev.ObservedAt) {
			prev.ObservedAt = at
		}
		t.points[s.id()] = prev
		return
	}
	t.points[s.id()] = Point{Name: s.name, Kind: kind, Value: value, ObservedAt: at, Delta: true, Labels: s.labels, SeriesKey: s.key}
}

// record sets or adds by temporality. One export cannot mix delta and
// cumulative points for a series: the result would depend on their order.
func (t *translation) record(s series, kind string, value float64, at time.Time, delta bool) {
	if prev, ok := t.points[s.id()]; ok && prev.Delta != delta {
		t.reject(1, fmt.Sprintf("metric %q: one export cannot mix delta and cumulative points", s.name))
		return
	}
	if delta {
		t.add(s, kind, value, at)
	} else {
		t.set(s, kind, value, at)
	}
}

// seriesOf resolves a data point's labels, rejecting the point on bad ones.
func (t *translation) seriesOf(original, name string, attrs []*commonpb.KeyValue) (series, bool) {
	labels, key, err := Labels(attrs)
	if err != nil {
		t.reject(1, fmt.Sprintf("metric %q: %v", original, err))
		return series{}, false
	}
	return series{name: name, key: key, labels: labels}, true
}

func (t *translation) metric(m *metricspb.Metric) {
	name := NormalizeName(m.GetName())
	switch data := m.GetData().(type) {
	case *metricspb.Metric_Gauge:
		t.numbers(m.GetName(), name, data.Gauge.GetDataPoints(), state.CustomMetricKindGauge, false)
	case *metricspb.Metric_Sum:
		kind := state.CustomMetricKindCounter
		if !data.Sum.GetIsMonotonic() {
			kind = state.CustomMetricKindGauge
		}
		delta, ok := temporality(data.Sum.GetAggregationTemporality())
		if !ok {
			t.reject(len(data.Sum.GetDataPoints()), fmt.Sprintf("metric %q: sums need delta or cumulative temporality", m.GetName()))
			return
		}
		t.numbers(m.GetName(), name, data.Sum.GetDataPoints(), kind, delta)
	case *metricspb.Metric_Histogram:
		delta, ok := temporality(data.Histogram.GetAggregationTemporality())
		if !ok {
			t.reject(len(data.Histogram.GetDataPoints()), fmt.Sprintf("metric %q: histograms need delta or cumulative temporality", m.GetName()))
			return
		}
		t.histogram(m.GetName(), name, data.Histogram.GetDataPoints(), delta)
	default:
		t.reject(dataPointCount(m), fmt.Sprintf("metric %q: exponential histograms and summaries are not supported", m.GetName()))
	}
}

func (t *translation) numbers(original, name string, points []*metricspb.NumberDataPoint, kind string, delta bool) {
	if api.ValidateCustomMetricName(name) != nil {
		t.reject(len(points), fmt.Sprintf("metric %q: name must normalise to [a-z][a-z0-9_]{0,62}", original))
		return
	}
	for _, dp := range points {
		s, ok := t.seriesOf(original, name, dp.GetAttributes())
		if !ok {
			continue
		}
		value := dp.GetAsDouble()
		if v, ok := dp.GetValue().(*metricspb.NumberDataPoint_AsInt); ok {
			value = float64(v.AsInt)
		}
		// A delta up-down counter legitimately goes down; only the stored
		// total must stay non-negative, which the store enforces.
		negativeOK := delta && kind == state.CustomMetricKindGauge
		if math.IsNaN(value) || math.IsInf(value, 0) || (value < 0 && !negativeOK) || (kind == state.CustomMetricKindGauge && api.ValidateCustomMetricValue(math.Abs(value)) != nil) {
			t.reject(1, fmt.Sprintf("metric %q: values must be finite and non-negative", original))
			continue
		}
		at, ok := t.pointAt(original, dp.GetTimeUnixNano())
		if !ok {
			continue
		}
		t.record(s, kind, value, at, delta)
	}
}

func (t *translation) histogram(original, base string, points []*metricspb.HistogramDataPoint, delta bool) {
	if api.ValidateCustomMetricName(base) != nil || len(base) > histogramMaxBaseBytes {
		t.reject(len(points), fmt.Sprintf("metric %q: a histogram name must normalise to [a-z][a-z0-9_]{0,%d}", original, histogramMaxBaseBytes-1))
		return
	}
	// The percentiles come from each series' newest data point.
	type newestPoint struct {
		s  series
		dp *metricspb.HistogramDataPoint
	}
	newest := map[string]newestPoint{}
	var order []string
	for _, dp := range points {
		s, ok := t.seriesOf(original, base, dp.GetAttributes())
		if !ok {
			continue
		}
		if !validHistogramPoint(dp) {
			t.reject(1, fmt.Sprintf("metric %q: histogram buckets, bounds and sum must be consistent, finite and non-negative", original))
			continue
		}
		at, ok := t.pointAt(original, dp.GetTimeUnixNano())
		if !ok {
			continue
		}
		t.record(s.suffixed(HistogramCountSuffix), state.CustomMetricKindCounter, float64(dp.GetCount()), at, delta)
		if dp.Sum != nil {
			t.record(s.suffixed(HistogramSumSuffix), state.CustomMetricKindCounter, dp.GetSum(), at, delta)
		}
		prev, seen := newest[s.key]
		if !seen {
			order = append(order, s.key)
		}
		if !seen || dp.GetTimeUnixNano() >= prev.dp.GetTimeUnixNano() {
			newest[s.key] = newestPoint{s: s, dp: dp}
		}
	}
	for _, key := range order {
		n := newest[key]
		if n.dp.GetCount() == 0 {
			continue
		}
		at, _ := t.at(n.dp.GetTimeUnixNano()) // already accepted above
		for _, q := range HistogramQuantiles {
			t.set(n.s.suffixed(q.Suffix), state.CustomMetricKindGauge, HistogramQuantile(q.Q, n.dp), at)
		}
	}
}

// validHistogramPoint checks the shape a percentile estimate relies on:
// one more bucket than bounds, bounds increasing, finite and non-negative
// (custom metric values are non-negative), bucket counts adding up to the
// count, and a non-negative finite sum.
func validHistogramPoint(dp *metricspb.HistogramDataPoint) bool {
	bounds, counts := dp.GetExplicitBounds(), dp.GetBucketCounts()
	if len(counts) == 0 {
		return dp.GetCount() == 0 || len(bounds) == 0
	}
	if len(counts) != len(bounds)+1 {
		return false
	}
	for i, b := range bounds {
		if b < 0 || math.IsNaN(b) || math.IsInf(b, 0) || (i > 0 && b <= bounds[i-1]) {
			return false
		}
	}
	var total uint64
	for _, c := range counts {
		total += c
	}
	if total != dp.GetCount() {
		return false
	}
	if dp.Sum != nil {
		if s := dp.GetSum(); s < 0 || math.IsNaN(s) || math.IsInf(s, 0) {
			return false
		}
	}
	return true
}

// HistogramQuantile estimates quantile q of an explicit-bucket histogram
// point by linear interpolation inside the bucket holding the target rank,
// as Prometheus' histogram_quantile does. The first bucket starts at the
// point's min (or 0); the overflow bucket ends at its max (or, without one,
// the last bound). With no buckets it answers the mean.
func HistogramQuantile(q float64, dp *metricspb.HistogramDataPoint) float64 {
	bounds, counts := dp.GetExplicitBounds(), dp.GetBucketCounts()
	if len(counts) == 0 || len(bounds) == 0 {
		if dp.GetCount() == 0 {
			return 0
		}
		return dp.GetSum() / float64(dp.GetCount())
	}
	rank := q * float64(dp.GetCount())
	var seen float64
	for i, c := range counts {
		if c == 0 {
			continue
		}
		if seen+float64(c) < rank {
			seen += float64(c)
			continue
		}
		lower, upper := 0.0, 0.0
		if i > 0 {
			lower = bounds[i-1]
		} else if dp.Min != nil && dp.GetMin() >= 0 {
			lower = dp.GetMin()
		}
		switch {
		case i < len(bounds):
			upper = bounds[i]
		case dp.Max != nil && dp.GetMax() > lower:
			upper = dp.GetMax()
		default:
			return lower
		}
		if upper < lower {
			upper = lower
		}
		return lower + (upper-lower)*(rank-seen)/float64(c)
	}
	return bounds[len(bounds)-1]
}

// temporality reports whether points are deltas; ok is false when the
// temporality is unspecified, which says neither.
func temporality(t metricspb.AggregationTemporality) (delta, ok bool) {
	switch t {
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA:
		return true, true
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE:
		return false, true
	}
	return false, false
}

// NormalizeName maps an OTLP metric name (conventionally dotted, e.g.
// "orders.pending") onto the custom metric name space.
func NormalizeName(name string) string {
	return strings.NewReplacer(".", "_", "-", "_", "/", "_").Replace(strings.ToLower(strings.TrimSpace(name)))
}

func dataPointCount(m *metricspb.Metric) int {
	switch data := m.GetData().(type) {
	case *metricspb.Metric_Histogram:
		return len(data.Histogram.GetDataPoints())
	case *metricspb.Metric_ExponentialHistogram:
		return len(data.ExponentialHistogram.GetDataPoints())
	case *metricspb.Metric_Summary:
		return len(data.Summary.GetDataPoints())
	default:
		return 1
	}
}
