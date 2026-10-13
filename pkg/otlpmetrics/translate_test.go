package otlpmetrics

import (
	"math"
	"sort"
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
	histogram := &metricspb.Metric{Name: "latency", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
		DataPoints: []*metricspb.HistogramDataPoint{{}, {}},
	}}}
	for _, tc := range []struct {
		name   string
		metric *metricspb.Metric
		want   int64
	}{
		{"delta sum", sum("reqs", true, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA, dbl(1, 0)), 1},
		{"non-monotonic sum", sum("depth", false, metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE, dbl(1, 0), dbl(2, 0)), 2},
		{"histogram", histogram, 2},
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

func TestTranslate_UntimedPointUsesNow(t *testing.T) {
	res := Translate(request(gauge("queue", &metricspb.NumberDataPoint{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 4}})), now)
	if len(res.Points) != 1 || !res.Points[0].ObservedAt.Equal(now) {
		t.Fatalf("points = %+v, want one point stamped now", res.Points)
	}
}
