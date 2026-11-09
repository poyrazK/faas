package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/receivers"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261108160000 — slice B: in-guest metric exports are stored like the
// public endpoint's, only for the app's own account and with history on.
func TestGuestMetricsSinkStoresForTheApp(t *testing.T) {
	e := customHistoryEnv(t)
	app := createApp(t, e, "shop")
	sink := guestMetricsSink{s: e.s}
	ctx := context.Background()
	accepted, rejected, err := sink.StoreGuestMetrics(ctx, app.AccountID, app.ID, otlpExport(otlpGauge("orders.pending", 4), otlpGauge("bad", -1)))
	if err != nil || accepted != 1 || rejected != 1 {
		t.Fatalf("store = %d/%d, %v", accepted, rejected, err)
	}
	rows, err := e.store.ListCustomMetrics(ctx, app.ID)
	if err != nil || len(rows) != 1 || rows[0].Name != "orders_pending" || rows[0].Value != 4 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	other := "00000000-0000-0000-0000-000000000001"
	if _, _, err := sink.StoreGuestMetrics(ctx, other, app.ID, otlpExport(otlpGauge("x", 1))); !errors.Is(err, receivers.ErrGuestMetricsDisabled) {
		t.Fatalf("other account = %v, want disabled", err)
	}
	e.s.customMetricHistoryEnabled = false
	if _, _, err := sink.StoreGuestMetrics(ctx, app.AccountID, app.ID, otlpExport(otlpGauge("x", 1))); !errors.Is(err, receivers.ErrGuestMetricsDisabled) {
		t.Fatalf("history off = %v, want disabled", err)
	}
}

func otlpExport(metrics ...*metricspb.Metric) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: []*metricspb.ResourceMetrics{{
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
	}}}
}

func otlpGauge(name string, v float64) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{{
		TimeUnixNano: uint64(time.Now().UnixNano()), Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: v},
	}}}}}
}

func otlpCounter(name string, v int64) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
		IsMonotonic: true, AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
		DataPoints: []*metricspb.NumberDataPoint{{TimeUnixNano: uint64(time.Now().UnixNano()), Value: &metricspb.NumberDataPoint_AsInt{AsInt: v}}},
	}}}
}

func postOTLP(t *testing.T, e testEnv, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/apps/shop/otlp/v1/metrics", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+e.key)
	r.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func TestPostOTLPMetrics_StoresGaugesAndCountersFromProtobuf(t *testing.T) {
	e := customHistoryEnv(t)
	app := createApp(t, e, "shop")
	body, err := proto.Marshal(otlpExport(otlpGauge("orders.pending", 12), otlpCounter("payments.failed", 3)))
	if err != nil {
		t.Fatal(err)
	}
	rec := postOTLP(t, e, "application/x-protobuf", body)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Fatalf("status %d content-type %q: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	var resp colmetricspb.ExportMetricsServiceResponse
	if err := proto.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.GetPartialSuccess().GetRejectedDataPoints() != 0 {
		t.Fatalf("response = %v (%v), want full success", &resp, err)
	}
	rows, err := e.store.ListCustomMetrics(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, row := range rows {
		kinds[row.Name] = row.Kind
	}
	if kinds["orders_pending"] != state.CustomMetricKindGauge || kinds["payments_failed"] != state.CustomMetricKindCounter {
		t.Fatalf("stored kinds = %v", kinds)
	}
}

func TestPostOTLPMetrics_ReportsPartialSuccessInJSON(t *testing.T) {
	e := customHistoryEnv(t)
	createApp(t, e, "shop")
	metrics := []*metricspb.Metric{otlpGauge("ok_metric", 1), otlpGauge("negative", -5)}
	for i := 0; i < api.PlanPro.CustomMetricsPerApp(); i++ {
		metrics = append(metrics, otlpGauge(fmt.Sprintf("extra_%03d", i), 1))
	}
	body, err := protojson.Marshal(otlpExport(metrics...))
	if err != nil {
		t.Fatal(err)
	}
	rec := postOTLP(t, e, "application/json", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var resp colmetricspb.ExportMetricsServiceResponse
	if err := protojson.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// One negative value plus every name beyond the per-app cap.
	if got := resp.GetPartialSuccess().GetRejectedDataPoints(); got != 2 {
		t.Fatalf("rejected = %d (%q), want 2", got, resp.GetPartialSuccess().GetErrorMessage())
	}
}

// adr: 20261028101615 — an export naming many new metrics writes the app's
// known names and admits new ones, in name order, only up to its limit.
func TestPostOTLPMetrics_AdmitsNewNamesOnlyWithinTheLimit(t *testing.T) {
	e := customHistoryEnv(t)
	app := createApp(t, e, "shop")
	known := []string{"known_a", "known_b", "known_c", "known_d"}
	var metrics []*metricspb.Metric
	for _, name := range known {
		metrics = append(metrics, otlpGauge(name, 1))
	}
	body, err := proto.Marshal(otlpExport(metrics...))
	if err != nil {
		t.Fatal(err)
	}
	if rec := postOTLP(t, e, "application/x-protobuf", body); rec.Code != http.StatusOK {
		t.Fatalf("seed status %d: %s", rec.Code, rec.Body)
	}

	metrics = []*metricspb.Metric{otlpGauge("known_a", 9)}
	for i := 99; i >= 0; i-- {
		metrics = append(metrics, otlpGauge(fmt.Sprintf("new_%02d", i), 1))
	}
	body, err = proto.Marshal(otlpExport(metrics...))
	if err != nil {
		t.Fatal(err)
	}
	rec := postOTLP(t, e, "application/x-protobuf", body)
	var resp colmetricspb.ExportMetricsServiceResponse
	if rec.Code != http.StatusOK || proto.Unmarshal(rec.Body.Bytes(), &resp) != nil {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	limit := api.PlanPro.CustomMetricsPerApp()
	free := limit - len(known)
	if got := resp.GetPartialSuccess().GetRejectedDataPoints(); got != int64(100-free) {
		t.Fatalf("rejected = %d, want the %d new names beyond the %d free slots", got, 100-free, free)
	}
	rows, err := e.store.ListCustomMetrics(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, row := range rows {
		values[row.Name] = row.Value
	}
	if len(values) != limit || values["known_a"] != 9 {
		t.Fatalf("stored = %v, want the known names (known_a updated) and new names up to the plan's %d", values, limit)
	}
	if _, ok := values["new_00"]; !ok {
		t.Fatalf("stored = %v, want the first new name in name order", values)
	}
}

// adr: 20261108160000 — delta counters accumulate across exports and a
// histogram is stored as its five derived series.
func TestPostOTLPMetrics_DeltasAccumulateAndHistogramsDerive(t *testing.T) {
	e := customHistoryEnv(t)
	app := createApp(t, e, "shop")
	delta := func(v int64) []byte {
		body, err := proto.Marshal(otlpExport(&metricspb.Metric{Name: "jobs.done", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
			IsMonotonic: true, AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
			DataPoints: []*metricspb.NumberDataPoint{{TimeUnixNano: uint64(time.Now().UnixNano()), Value: &metricspb.NumberDataPoint_AsInt{AsInt: v}}},
		}}}))
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	for _, v := range []int64{3, 4} {
		if rec := postOTLP(t, e, "application/x-protobuf", delta(v)); rec.Code != http.StatusOK {
			t.Fatalf("delta export: %d %s", rec.Code, rec.Body)
		}
	}
	sumValue := 30.0
	histogram, err := proto.Marshal(otlpExport(&metricspb.Metric{Name: "latency", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
		AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
		DataPoints: []*metricspb.HistogramDataPoint{{TimeUnixNano: uint64(time.Now().UnixNano()), Count: 4, Sum: &sumValue,
			ExplicitBounds: []float64{10}, BucketCounts: []uint64{4, 0}}},
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	if rec := postOTLP(t, e, "application/x-protobuf", histogram); rec.Code != http.StatusOK {
		t.Fatalf("histogram export: %d %s", rec.Code, rec.Body)
	}
	rows, err := e.store.ListCustomMetrics(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]state.CustomMetric{}
	for _, row := range rows {
		got[row.Name] = row
	}
	if r := got["jobs_done"]; r.Value != 7 || r.Kind != state.CustomMetricKindCounter {
		t.Fatalf("jobs_done = %+v, want counter 7 accumulated from deltas 3 and 4", r)
	}
	for _, name := range []string{"latency_count", "latency_sum", "latency_p50", "latency_p95", "latency_p99"} {
		if _, ok := got[name]; !ok {
			t.Fatalf("histogram series %s missing; stored %v", name, got)
		}
	}
	if got["latency_count"].Value != 4 || got["latency_sum"].Value != 30 || got["latency_p50"].Value != 5 {
		t.Fatalf("histogram series = %+v", got)
	}
}

// adr: 20261108160000 — slice C: labelled points are stored as series, the
// metric row carries their aggregate, a later unlabelled point joins as the
// '' series, and the list shows the series.
func TestPostOTLPMetrics_LabelledSeriesAggregate(t *testing.T) {
	e := customHistoryEnv(t)
	app := createApp(t, e, "shop")
	point := func(v float64, queue string) *metricspb.NumberDataPoint {
		dp := &metricspb.NumberDataPoint{TimeUnixNano: uint64(time.Now().UnixNano()), Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: v}}
		if queue != "" {
			dp.Attributes = []*commonpb.KeyValue{{Key: "queue", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: queue}}}}
		}
		return dp
	}
	send := func(points ...*metricspb.NumberDataPoint) {
		t.Helper()
		body, err := proto.Marshal(otlpExport(&metricspb.Metric{Name: "queue.depth", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}}}))
		if err != nil {
			t.Fatal(err)
		}
		rec := postOTLP(t, e, "application/x-protobuf", body)
		var resp colmetricspb.ExportMetricsServiceResponse
		if rec.Code != http.StatusOK || proto.Unmarshal(rec.Body.Bytes(), &resp) != nil || resp.GetPartialSuccess().GetRejectedDataPoints() != 0 {
			t.Fatalf("export: %d %s %v", rec.Code, rec.Body, &resp)
		}
	}
	send(point(4, "emails"), point(9, "billing"))
	send(point(2, ""))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/shop/custom-metrics", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	e.h.ServeHTTP(rec, req)
	var list api.CustomMetricListResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if len(list.Metrics) != 1 || list.Metrics[0].Value != 9 || len(list.Metrics[0].Series) != 3 || list.MaxSeries != api.PlanPro.CustomMetricSeriesPerApp() {
		t.Fatalf("list = %+v", list)
	}
	if s := list.Metrics[0].Series[1]; s.Key != "queue=billing" || s.Labels["queue"] != "billing" || s.Value != 9 {
		t.Fatalf("series[1] = %+v", s)
	}
	rows, err := e.store.ListCustomMetrics(context.Background(), app.ID)
	if err != nil || len(rows) != 1 || !rows[0].Labelled {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
}

func TestPostOTLPMetrics_GatesAndValidation(t *testing.T) {
	e := setup(t, api.PlanPro)
	createApp(t, e, "shop")
	if rec := postOTLP(t, e, "application/x-protobuf", nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("flag off = %d, want 503", rec.Code)
	}
	e.s.customMetricHistoryEnabled = true
	if rec := postOTLP(t, e, "text/plain", []byte("x")); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain = %d, want 415", rec.Code)
	}
	if rec := postOTLP(t, e, "application/x-protobuf", []byte{0xff, 0xff}); rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage body = %d, want 400", rec.Code)
	}
}
