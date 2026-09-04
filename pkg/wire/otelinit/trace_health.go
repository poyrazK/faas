package otelinit

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TraceExporterHealth exposes the health of an OTLP trace exporter in the
// daemon's existing Prometheus registry. It deliberately reports exporter
// attempts rather than SDK queue depth: a successful ExportSpans call is the
// last point at which this process can know that the collector accepted a
// batch.
type TraceExporterHealth struct {
	enabled        prometheus.Gauge
	up             prometheus.Gauge
	exports        *prometheus.CounterVec
	spansExported  prometheus.Counter
	spansDropped   prometheus.Counter
	exportDuration prometheus.Histogram
	lastAttempt    prometheus.Gauge
	lastSuccess    prometheus.Gauge
}

// NewTraceExporterHealth registers trace exporter health collectors on reg.
// A nil registerer disables the instrumentation and returns a nil health
// handle, which keeps package-level and test-only SDK users lightweight.
func NewTraceExporterHealth(reg prometheus.Registerer, prefix string) (*TraceExporterHealth, error) {
	if reg == nil {
		return nil, nil
	}
	if prefix == "" {
		prefix = "otel"
	}
	h := &TraceExporterHealth{
		enabled: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_otel_trace_exporter_enabled",
			Help: "Whether an OTLP trace exporter is configured for this daemon (1) or not (0).",
		}),
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_otel_trace_exporter_up",
			Help: "Whether the OTLP trace exporter has not observed a failed export since startup or its last successful export.",
		}),
		exports: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_otel_trace_export_total",
			Help: "OTLP trace export attempts, labelled by SDK trigger and outcome.",
		}, []string{"trigger", "outcome"}),
		spansExported: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_otel_trace_spans_exported_total",
			Help: "Spans in OTLP trace batches exported successfully.",
		}),
		spansDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_otel_trace_spans_dropped_total",
			Help: "Spans in OTLP trace batches whose export failed and may have been dropped by the SDK.",
		}),
		exportDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    prefix + "_otel_trace_export_duration_seconds",
			Help:    "Duration of OTLP trace export attempts in seconds.",
			Buckets: prometheus.DefBuckets,
		}),
		lastAttempt: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_otel_trace_last_attempt_timestamp_seconds",
			Help: "Unix timestamp of the most recent OTLP trace export attempt.",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_otel_trace_last_success_timestamp_seconds",
			Help: "Unix timestamp of the most recent successful OTLP trace export.",
		}),
	}
	collectors := []prometheus.Collector{
		h.enabled,
		h.up,
		h.exports,
		h.spansExported,
		h.spansDropped,
		h.exportDuration,
		h.lastAttempt,
		h.lastSuccess,
	}
	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			return nil, fmt.Errorf("register %T: %w", collector, err)
		}
	}
	for _, trigger := range []string{"batch", "shutdown"} {
		for _, outcome := range []string{"success", "error"} {
			h.exports.WithLabelValues(trigger, outcome)
		}
	}
	return h, nil
}

// SetEnabled records whether the daemon configured an OTLP trace exporter.
func (h *TraceExporterHealth) SetEnabled(enabled bool) {
	if h == nil {
		return
	}
	if enabled {
		h.enabled.Set(1)
		// There has not been a failed attempt yet. This avoids a false alarm
		// during the normal interval before the first batch is flushed.
		h.up.Set(1)
		return
	}
	h.enabled.Set(0)
	h.up.Set(0)
}

// SetUnavailable marks a configured exporter that could not be constructed
// or is otherwise known to be unusable. It intentionally keeps enabled at 1
// so configuration failures are visible to the exporter-down alert.
func (h *TraceExporterHealth) SetUnavailable() {
	if h == nil {
		return
	}
	h.enabled.Set(1)
	h.up.Set(0)
}

// Wrap instruments an SDK span exporter. The returned exporter is safe for
// concurrent calls, matching the SDK SpanExporter contract.
func (h *TraceExporterHealth) Wrap(exporter sdktrace.SpanExporter) sdktrace.SpanExporter {
	if h == nil || exporter == nil {
		return exporter
	}
	return traceHealthExporter{health: h, exporter: exporter}
}

type traceHealthExporter struct {
	health   *TraceExporterHealth
	exporter sdktrace.SpanExporter
}

func (e traceHealthExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	started := time.Now()
	e.health.lastAttempt.Set(float64(started.UnixNano()) / float64(time.Second))
	err := e.exporter.ExportSpans(ctx, spans)
	e.health.exportDuration.Observe(time.Since(started).Seconds())
	if err != nil {
		e.health.exports.WithLabelValues("batch", "error").Inc()
		e.health.spansDropped.Add(float64(len(spans)))
		e.health.up.Set(0)
		return err
	}
	e.health.exports.WithLabelValues("batch", "success").Inc()
	e.health.spansExported.Add(float64(len(spans)))
	e.health.lastSuccess.Set(float64(time.Now().UnixNano()) / float64(time.Second))
	e.health.up.Set(1)
	return nil
}

func (e traceHealthExporter) Shutdown(ctx context.Context) error {
	err := e.exporter.Shutdown(ctx)
	if err != nil {
		e.health.exports.WithLabelValues("shutdown", "error").Inc()
		e.health.up.Set(0)
		return err
	}
	e.health.exports.WithLabelValues("shutdown", "success").Inc()
	return nil
}
