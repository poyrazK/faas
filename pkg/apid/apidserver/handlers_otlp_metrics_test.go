package apidserver

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

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
	for i := 0; i < api.MaxCustomMetricsPerApp; i++ {
		metrics = append(metrics, otlpGauge("extra_"+string(rune('a'+i)), 1))
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
	if got := resp.GetPartialSuccess().GetRejectedDataPoints(); got != 99 {
		t.Fatalf("rejected = %d, want the 99 new names beyond the one free slot", got)
	}
	rows, err := e.store.ListCustomMetrics(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	for _, row := range rows {
		values[row.Name] = row.Value
	}
	if len(values) != api.MaxCustomMetricsPerApp || values["known_a"] != 9 {
		t.Fatalf("stored = %v, want the known names (known_a updated) and one new name", values)
	}
	if _, ok := values["new_00"]; !ok {
		t.Fatalf("stored = %v, want the first new name in name order", values)
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
