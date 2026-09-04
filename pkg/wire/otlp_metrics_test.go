package wire_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
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
