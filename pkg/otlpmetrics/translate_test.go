package otlpmetrics

import (
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

var now = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func ts(d time.Duration) uint64 { return uint64(now.Add(d).UnixNano()) }

func dbl(v float64, at time.Duration) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{TimeUnixNano: ts(at), Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: v}}
}

func request(metrics ...*metricspb.Metric) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
	}}}
}

func gauge(name string, points ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}}}
}

func sum(name string, monotonic bool, temporality metricspb.AggregationTemporality, points ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
		IsMonotonic: monotonic, AggregationTemporality: temporality, DataPoints: points,
	}}}
}

func TestTranslate_GaugesAndCountersKeepTheNewestPoint(t *testing.T) {
	intPoint := &metricspb.NumberDataPoint{TimeUnixNano: ts(-time.Minute), Value: &metricspb.NumberDataPoint_AsInt{AsInt: 7}}
	res := Translate(request(
		gauge("orders.pending", dbl(5, -2*time.Minute), dbl(9, -time.Second), dbl(3, -time.Minute)),
		sum("payments-failed", true, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, intPoint),
	), now)
	if res.Rejected != 0 {
		t.Fatalf("rejected %d (%s), want 0", res.Rejected, res.Reason)
	}
	sort.Slice(res.Points, func(i, j int) bool { return res.Points[i].Name < res.Points[j].Name })
	if len(res.Points) != 2 {
		t.Fatalf("points = %+v", res.Points)
	}
	if p := res.Points[0]; p.Name != "orders_pending" || p.Kind != "gauge" || p.Value != 9 || !p.ObservedAt.Equal(now.Add(-time.Second)) {
		t.Fatalf("gauge point = %+v, want the newest value 9", p)
	}
	if p := res.Points[1]; p.Name != "payments_failed" || p.Kind != "counter" || p.Value != 7 {
		t.Fatalf("counter point = %+v", p)
	}
}

func TestTranslate_RejectsWhatCannotBeStored(t *testing.T) {
	withAttrs := dbl(1, 0)
	withAttrs.Attributes = []*commonpb.KeyValue{{Key: "region", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "eu"}}}}
	exponential := &metricspb.Metric{Name: "latency", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
		DataPoints: []*metricspb.ExponentialHistogramDataPoint{{}, {}},
	}}}
	summary := &metricspb.Metric{Name: "latency", Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{
		DataPoints: []*metricspb.SummaryDataPoint{{}},
	}}}
	for _, tc := range []struct {
		name   string
		metric *metricspb.Metric
		want   int64
	}{
		{"exponential histogram", exponential, 2},
		{"summary", summary, 1},
		{"negative cumulative up-down", sum("depth", false, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, dbl(-1, 0)), 1},
		{"negative delta counter", sum("reqs", true, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, dbl(-1, 0)), 1},
		{"inconsistent histogram", histogramMetric("latency", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			histPoint(0, 5, []float64{1, 2}, []uint64{1, 1})), 1},
		{"histogram with negative bounds", histogramMetric("latency", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			histPoint(0, 1, []float64{-1}, []uint64{1, 0})), 1},
		{"histogram name too long", histogramMetric(strings.Repeat("h", 58), metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			histPoint(0, 1, []float64{1}, []uint64{1, 0})), 1},
		{"attributes", gauge("orders", withAttrs), 1},
		{"negative", gauge("orders", dbl(-1, 0)), 1},
		{"non-finite", gauge("orders", dbl(math.Inf(1), 0)), 1},
		{"invalid name", gauge("9lives", dbl(1, 0)), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Translate(request(tc.metric), now)
			if res.Rejected != tc.want || len(res.Points) != 0 || res.Reason == "" {
				t.Fatalf("rejected=%d points=%d reason=%q, want %d rejected and none stored", res.Rejected, len(res.Points), res.Reason, tc.want)
			}
		})
	}
}

func histPoint(at time.Duration, sumValue float64, bounds []float64, counts []uint64) *metricspb.HistogramDataPoint {
	var total uint64
	for _, c := range counts {
		total += c
	}
	return &metricspb.HistogramDataPoint{TimeUnixNano: ts(at), Count: total, Sum: &sumValue, ExplicitBounds: bounds, BucketCounts: counts}
}

func histogramMetric(name string, temporality metricspb.AggregationTemporality, points ...*metricspb.HistogramDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
		AggregationTemporality: temporality, DataPoints: points,
	}}}
}

func byName(points []Point) map[string]Point {
	out := make(map[string]Point, len(points))
	for _, p := range points {
		out[p.Name] = p
	}
	return out
}

// adr: 20261108160000 — delta sums accumulate: points of one name in an
// export are summed into one delta, counters stay counters.
func TestTranslate_DeltaSumsAreSummedIntoOneDelta(t *testing.T) {
	res := Translate(request(
		sum("http.requests", true, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, dbl(3, -2*time.Second), dbl(4, -time.Second)),
	), now)
	if res.Rejected != 0 || len(res.Points) != 1 {
		t.Fatalf("res = %+v", res)
	}
	p := res.Points[0]
	if p.Name != "http_requests" || p.Kind != "counter" || !p.Delta || p.Value != 7 || !p.ObservedAt.Equal(now.Add(-time.Second)) {
		t.Fatalf("point = %+v, want one counter delta of 7 at the newest time", p)
	}
}

// adr: 20261108160000 — up-down counters are gauges: cumulative ones replace
// the value, delta ones (which may be negative) move it.
func TestTranslate_UpDownCountersAreGauges(t *testing.T) {
	res := Translate(request(
		sum("queue.depth", false, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, dbl(5, -time.Minute), dbl(8, -time.Second)),
		sum("connections", false, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, dbl(3, -time.Minute), dbl(-5, -time.Second)),
	), now)
	if res.Rejected != 0 {
		t.Fatalf("rejected %d: %s", res.Rejected, res.Reason)
	}
	points := byName(res.Points)
	if p := points["queue_depth"]; p.Kind != "gauge" || p.Delta || p.Value != 8 {
		t.Fatalf("cumulative up-down = %+v", p)
	}
	if p := points["connections"]; p.Kind != "gauge" || !p.Delta || p.Value != -2 {
		t.Fatalf("delta up-down = %+v, want a gauge delta of -2", p)
	}
}

// adr: 20261108160000 — a histogram becomes _count and _sum counters and
// _p50/_p95/_p99 gauges interpolated from its buckets.
func TestTranslate_HistogramBecomesDerivedSeries(t *testing.T) {
	// 100 observations: 50 in [0,10], 40 in (10,100], 10 in (100,1000].
	cumulative := histogramMetric("http.server.duration", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
		histPoint(-time.Minute, 1, []float64{10, 100}, []uint64{1, 0, 0}),
		histPoint(-time.Second, 9000, []float64{10, 100, 1000}, []uint64{50, 40, 10, 0}))
	res := Translate(request(cumulative), now)
	if res.Rejected != 0 {
		t.Fatalf("rejected %d: %s", res.Rejected, res.Reason)
	}
	points := byName(res.Points)
	want := map[string]struct {
		kind  string
		value float64
	}{
		"http_server_duration_count": {"counter", 100},
		"http_server_duration_sum":   {"counter", 9000},
		"http_server_duration_p50":   {"gauge", 10},
		"http_server_duration_p95":   {"gauge", 550},
		"http_server_duration_p99":   {"gauge", 910},
	}
	if len(points) != len(want) {
		t.Fatalf("points = %+v", res.Points)
	}
	for name, w := range want {
		p := points[name]
		if p.Kind != w.kind || p.Delta || math.Abs(p.Value-w.value) > 1e-9 || !p.ObservedAt.Equal(now.Add(-time.Second)) {
			t.Errorf("%s = %+v, want %s %v from the newest point", name, p, w.kind, w.value)
		}
	}

	delta := histogramMetric("jobs", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
		histPoint(-2*time.Second, 3, []float64{1}, []uint64{2, 0}),
		histPoint(-time.Second, 5, []float64{1}, []uint64{1, 2}))
	points = byName(Translate(request(delta), now).Points)
	if p := points["jobs_count"]; !p.Delta || p.Value != 5 || p.Kind != "counter" {
		t.Fatalf("delta histogram count = %+v, want a counter delta of 5", p)
	}
	if p := points["jobs_sum"]; !p.Delta || p.Value != 8 {
		t.Fatalf("delta histogram sum = %+v, want a delta of 8", p)
	}
	if p := points["jobs_p50"]; p.Delta || p.Kind != "gauge" {
		t.Fatalf("delta histogram p50 = %+v, want a gauge set from the newest point", p)
	}
}

func TestHistogramQuantileEdges(t *testing.T) {
	maxV, minV := 400.0, 2.0
	overflow := histPoint(0, 0, []float64{10, 100}, []uint64{0, 0, 4})
	if got := HistogramQuantile(0.5, overflow); got != 100 {
		t.Fatalf("overflow without max = %v, want the last bound", got)
	}
	overflow.Max = &maxV
	if got := HistogramQuantile(0.5, overflow); got != 250 {
		t.Fatalf("overflow with max = %v, want 250", got)
	}
	first := histPoint(0, 0, []float64{10}, []uint64{2, 0})
	first.Min = &minV
	if got := HistogramQuantile(0.5, first); got != 6 {
		t.Fatalf("first bucket from min = %v, want 6", got)
	}
	noBuckets := &metricspb.HistogramDataPoint{Count: 4, Sum: &maxV}
	if got := HistogramQuantile(0.99, noBuckets); got != 100 {
		t.Fatalf("no buckets = %v, want the mean", got)
	}
	if res := Translate(request(histogramMetric("empty", metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
		histPoint(0, 0, nil, nil))), now); len(byName(res.Points)) != 2 {
		t.Fatalf("an empty histogram = %+v, want only _count and _sum", res.Points)
	}
}

func TestTranslate_UntimedPointUsesNow(t *testing.T) {
	res := Translate(request(gauge("queue", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 4}})), now)
	if len(res.Points) != 1 || !res.Points[0].ObservedAt.Equal(now) {
		t.Fatalf("points = %+v, want one point stamped now", res.Points)
	}
}
