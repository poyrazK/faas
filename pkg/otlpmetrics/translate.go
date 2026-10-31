// Package otlpmetrics translates OTLP metric exports into ADR-202/ADR-745
// custom metric writes. It holds no I/O so the mapping rules are testable on
// their own; apid does the authentication and storage.
package otlpmetrics

import (
	"fmt"
	"math"
	"strings"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Point is one metric value to store: the latest accepted data point for a
// normalised name, or, when Delta is set, the sum of an export's delta points
// for it, which the store adds to the counter's total (ADR-20261031144636).
type Point struct {
	Name       string
	Kind       string
	Value      float64
	ObservedAt time.Time
	Delta      bool
}

// Result is the translation outcome. Rejected counts data points that were
// not stored; Reason describes the first rejection for the OTLP partial
// success message.
type Result struct {
	Points   []Point
	Rejected int64
	Reason   string
}

// Translate applies the ADR-745 rules:
//   - gauges are stored as gauges; cumulative monotonic sums as counters;
//   - delta or non-monotonic sums, histograms, and other types are rejected;
//   - data points with attributes are rejected (attributes would multiply the
//     per-app series and defeat the name cap);
//   - negative or non-finite values are rejected;
//   - names are lowercased with '.', '-' and '/' mapped to '_' and must then
//     satisfy the custom metric name rules;
//   - the newest data point per name wins.
//
// now stamps points that carry no timestamp.
func Translate(req *colmetricspb.ExportMetricsServiceRequest, now time.Time) Result {
	var res Result
	latest := map[string]Point{}
	reject := func(n int, reason string) {
		res.Rejected += int64(n)
		if res.Reason == "" {
			res.Reason = reason
		}
	}
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				translateMetric(m, now, latest, reject)
			}
		}
	}
	for _, p := range latest {
		res.Points = append(res.Points, p)
	}
	return res
}

func translateMetric(m *metricspb.Metric, now time.Time, latest map[string]Point, reject func(int, string)) {
	var points []*metricspb.NumberDataPoint
	kind, delta := state.CustomMetricKindGauge, false
	switch data := m.GetData().(type) {
	case *metricspb.Metric_Gauge:
		points = data.Gauge.GetDataPoints()
	case *metricspb.Metric_Sum:
		points = data.Sum.GetDataPoints()
		temporality := data.Sum.GetAggregationTemporality()
		if !data.Sum.GetIsMonotonic() || temporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE &&
			temporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA {
			reject(len(points), fmt.Sprintf("metric %q: only monotonic sums with delta or cumulative temporality are supported", m.GetName()))
			return
		}
		kind = state.CustomMetricKindCounter
		delta = temporality == metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	default:
		reject(dataPointCount(m), fmt.Sprintf("metric %q: only gauges and cumulative monotonic sums are supported", m.GetName()))
		return
	}
	name := NormalizeName(m.GetName())
	if api.ValidateCustomMetricName(name) != nil {
		reject(len(points), fmt.Sprintf("metric %q: name must normalise to [a-z][a-z0-9_]{0,62}", m.GetName()))
		return
	}
	for _, dp := range points {
		if len(dp.GetAttributes()) > 0 {
			reject(1, fmt.Sprintf("metric %q: data point attributes are not supported", m.GetName()))
			continue
		}
		value := dp.GetAsDouble()
		if v, ok := dp.GetValue().(*metricspb.NumberDataPoint_AsInt); ok {
			value = float64(v.AsInt)
		}
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			reject(1, fmt.Sprintf("metric %q: values must be finite and non-negative", m.GetName()))
			continue
		}
		at, ok := pointTime(dp.GetTimeUnixNano(), now)
		if !ok {
			reject(1, fmt.Sprintf("metric %q: timestamps must not be more than %ds in the future", m.GetName(), api.CustomMetricMaxFutureSkewSeconds))
			continue
		}
		prev, seen := latest[name]
		switch {
		case seen && prev.Delta != delta:
			reject(1, fmt.Sprintf("metric %q: one export cannot mix delta and cumulative points", m.GetName()))
			continue
		case delta && seen:
			// Every delta counts: they are increments from different
			// intervals or instances, summed into the counter's total.
			prev.Value += value
			if at.After(prev.ObservedAt) {
				prev.ObservedAt = at
			}
			latest[name] = prev
			continue
		case seen && !at.After(prev.ObservedAt):
			continue
		}
		latest[name] = Point{Name: name, Kind: kind, Value: value, ObservedAt: at, Delta: delta}
	}
}

// pointTime is a data point's time: now when unset, and refused when it is
// out of range or further in the future than CustomMetricMaxFutureSkewSeconds,
// which would keep a stopped pusher's value fresh until then.
func pointTime(ts uint64, now time.Time) (time.Time, bool) {
	if ts == 0 {
		return now, true
	}
	if ts > math.MaxInt64 {
		return time.Time{}, false
	}
	at := time.Unix(0, int64(ts)).UTC()
	if at.After(now.Add(api.CustomMetricMaxFutureSkewSeconds * time.Second)) {
		return time.Time{}, false
	}
	return at, true
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
