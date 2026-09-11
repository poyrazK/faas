package wire_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	collector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestStartOTLPMetricsHealthSuccess(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)

	registry := prometheus.NewRegistry()
	shutdown, err := wire.StartOTLPMetrics(context.Background(), registry, collector.URL, "gatewayd-public", nil)
	if err != nil {
		t.Fatalf("StartOTLPMetrics: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if got := gatheredValue(t, registry, "gatewayd_public_otel_metrics_exporter_enabled", nil); got != 1 {
		t.Errorf("exporter_enabled = %v, want 1", got)
	}
	if got := gatheredValue(t, registry, "gatewayd_public_otel_metrics_exporter_up", nil); got != 1 {
		t.Errorf("exporter_up = %v, want 1", got)
	}
	if got := gatheredValue(t, registry, "gatewayd_public_otel_metrics_export_total", map[string]string{"trigger": "shutdown", "outcome": "success"}); got != 1 {
		t.Errorf("successful shutdown exports = %v, want 1", got)
	}
	if got := gatheredValue(t, registry, "gatewayd_public_otel_metrics_export_total", map[string]string{"trigger": "shutdown", "outcome": "error"}); got != 0 {
		t.Errorf("failed shutdown exports = %v, want 0", got)
	}
	if got := gatheredValue(t, registry, "gatewayd_public_otel_metrics_last_success_timestamp_seconds", nil); got <= 0 {
		t.Errorf("last_success_timestamp_seconds = %v, want positive", got)
	}
}

func TestStartOTLPMetricsHealthFailure(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "collector unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(collector.Close)

	registry := prometheus.NewRegistry()
	shutdown, err := wire.StartOTLPMetrics(context.Background(), registry, collector.URL, "apid", nil)
	if err != nil {
		t.Fatalf("StartOTLPMetrics: %v", err)
	}
	if err := shutdown(context.Background()); err == nil {
		t.Fatal("shutdown returned nil after collector failure")
	}

	if got := gatheredValue(t, registry, "apid_otel_metrics_exporter_enabled", nil); got != 1 {
		t.Errorf("exporter_enabled = %v, want 1", got)
	}
	if got := gatheredValue(t, registry, "apid_otel_metrics_exporter_up", nil); got != 0 {
		t.Errorf("exporter_up = %v, want 0", got)
	}
	if got := gatheredValue(t, registry, "apid_otel_metrics_export_total", map[string]string{"trigger": "shutdown", "outcome": "error"}); got != 1 {
		t.Errorf("failed shutdown exports = %v, want 1", got)
	}
}

func TestStartOTLPMetricsHealthDisabled(t *testing.T) {
	registry := prometheus.NewRegistry()
	shutdown, err := wire.StartOTLPMetrics(context.Background(), registry, "", "gatewayd-internal", nil)
	if err != nil {
		t.Fatalf("StartOTLPMetrics: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if got := gatheredValue(t, registry, "gatewayd_internal_otel_metrics_exporter_enabled", nil); got != 0 {
		t.Errorf("exporter_enabled = %v, want 0", got)
	}
	if got := gatheredValue(t, registry, "gatewayd_internal_otel_metrics_exporter_up", nil); got != 0 {
		t.Errorf("exporter_up = %v, want 0", got)
	}
}

func TestStartOTLPMetricsUsesStandardHeadersAndResourceAttributes(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=Bearer%20test-token")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=production,faas.node.name=fsn-1")

	var gotAuthorization string
	var gotBody []byte
	collectorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collectorServer.Close)

	registry := prometheus.NewRegistry()
	shutdown, err := wire.StartOTLPMetrics(context.Background(), registry, collectorServer.URL, "gatewayd-public", nil)
	if err != nil {
		t.Fatalf("StartOTLPMetrics: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if gotAuthorization != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want decoded bearer header", gotAuthorization)
	}
	var exported collector.ExportMetricsServiceRequest
	if err := proto.Unmarshal(gotBody, &exported); err != nil {
		t.Fatalf("decode OTLP metrics: %v", err)
	}
	if len(exported.GetResourceMetrics()) != 1 {
		t.Fatalf("resource metrics = %d, want 1", len(exported.GetResourceMetrics()))
	}
	attrs := exported.GetResourceMetrics()[0].GetResource().GetAttributes()
	for _, want := range []string{"deployment.environment", "faas.node.name", "service.name", "service.version"} {
		found := false
		for _, attr := range attrs {
			if attr.GetKey() == want {
				found = true
				break
			}
		}
		if !found && (want != "service.version" || strings.TrimSpace(wire.Version) != "") {
			t.Errorf("resource attribute %q missing from OTLP metrics export", want)
		}
	}
}

func gatheredValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !metricLabelsMatch(metric, labels) {
				continue
			}
			switch family.GetType() {
			case dto.MetricType_COUNTER:
				return metric.GetCounter().GetValue()
			case dto.MetricType_GAUGE:
				return metric.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %q with labels %v not found", name, labels)
	return 0
}

func metricLabelsMatch(metric *dto.Metric, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	for _, label := range metric.GetLabel() {
		if value, ok := want[label.GetName()]; ok && value != label.GetValue() {
			return false
		}
	}
	for name := range want {
		found := false
		for _, label := range metric.GetLabel() {
			if label.GetName() == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
