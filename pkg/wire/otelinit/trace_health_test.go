package otelinit

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type fakeSpanExporter struct {
	err         error
	shutdownErr error
}

func (f *fakeSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	return f.err
}

func (f *fakeSpanExporter) Shutdown(context.Context) error {
	return f.shutdownErr
}

func TestTraceExporterHealthTracksBatchSuccessAndFailure(t *testing.T) {
	reg := newTestRegistry()
	health, err := NewTraceExporterHealth(reg, "schedd")
	if err != nil {
		t.Fatalf("NewTraceExporterHealth: %v", err)
	}
	health.SetEnabled(true)
	exporter := &fakeSpanExporter{}
	wrapped := health.Wrap(exporter)

	if err := wrapped.ExportSpans(context.Background(), make([]sdktrace.ReadOnlySpan, 3)); err != nil {
		t.Fatalf("successful ExportSpans: %v", err)
	}
	if got := testutil.ToFloat64(health.up); got != 1 {
		t.Errorf("up after success = %v, want 1", got)
	}
	if got := testutil.ToFloat64(health.spansExported); got != 3 {
		t.Errorf("spans exported = %v, want 3", got)
	}
	if got := testutil.ToFloat64(health.exports.WithLabelValues("batch", "success")); got != 1 {
		t.Errorf("successful batch exports = %v, want 1", got)
	}
	if got := testutil.ToFloat64(health.lastSuccess); got <= 0 {
		t.Errorf("last success timestamp = %v, want positive", got)
	}

	exporter.err = errors.New("collector unavailable")
	if err := wrapped.ExportSpans(context.Background(), make([]sdktrace.ReadOnlySpan, 2)); err == nil {
		t.Fatal("failed ExportSpans returned nil")
	}
	if got := testutil.ToFloat64(health.up); got != 0 {
		t.Errorf("up after failure = %v, want 0", got)
	}
	if got := testutil.ToFloat64(health.spansDropped); got != 2 {
		t.Errorf("spans dropped = %v, want 2", got)
	}
	if got := testutil.ToFloat64(health.exports.WithLabelValues("batch", "error")); got != 1 {
		t.Errorf("failed batch exports = %v, want 1", got)
	}
	if got := testutil.ToFloat64(health.lastAttempt); got <= 0 {
		t.Errorf("last attempt timestamp = %v, want positive", got)
	}
}

func TestTraceExporterHealthTracksShutdownFailure(t *testing.T) {
	reg := newTestRegistry()
	health, err := NewTraceExporterHealth(reg, "schedd")
	if err != nil {
		t.Fatalf("NewTraceExporterHealth: %v", err)
	}
	health.SetEnabled(true)
	exporter := &fakeSpanExporter{shutdownErr: errors.New("shutdown failed")}

	if err := health.Wrap(exporter).Shutdown(context.Background()); err == nil {
		t.Fatal("Shutdown returned nil")
	}
	if got := testutil.ToFloat64(health.up); got != 0 {
		t.Errorf("up after shutdown failure = %v, want 0", got)
	}
	if got := testutil.ToFloat64(health.exports.WithLabelValues("shutdown", "error")); got != 1 {
		t.Errorf("failed shutdown exports = %v, want 1", got)
	}
}

func newTestRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}
