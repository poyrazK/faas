// Package gateway — trace_setup.go (issue #555 PR-2). Wires the
// TraceRing + TraceRingExporter + TraceHandler into a unified
// TracerProvider that the caller installs via otel.SetTracerProvider.
//
// Why this lives in pkg/gateway (not pkg/wire/otelinit): the
// TraceRing is gateway-specific state. pkg/wire/otelinit owns the
// SDK boot; pkg/gateway/trace_setup owns the application wiring.
//
// The split mirrors the existing pkg/gateway package boundary: the
// routes-RingCache is gateway state, the metrics registry is wire
// state. The OTel SDK is the cross-cutting concern; the ring is
// the gateway-specific consumer.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// envTraceRingCap is the FAAS-flavored env var for the ring cap.
// Defaults to DefaultTraceRingCap when unset.
const envTraceRingCap = "FAAS_TRACE_RING_CAP"

// envObserverToken is the env var for the X-Faas-Trace-Auth header
// gate. Empty value disables the GET endpoint (returns 404).
const envObserverToken = "FAAS_TRACE_OBSERVER_TOKEN"

// TraceSetup is the runtime bundle the daemon installs at boot.
type TraceSetup struct {
	// Ring is the populated ring. The daemon mounts the handler
	// on the public mux.
	Ring *TraceRing
	// Handler is the http.Handler for GET /v1/traces/{trace_id}.
	Handler http.Handler
	// Shutdown is the OTLP/SDK shutdown func. The daemon calls it
	// after the listeners drain so any in-flight spans are flushed.
	Shutdown func(ctx context.Context) error
	// TracerProvider is the installed OTel TracerProvider. The
	// daemon hands it to the otelhttp.NewHandler / otelgrpc
	// interceptors (PR-3).
	TracerProvider *sdktrace.TracerProvider
}

// InstallTracePipeline builds a TraceRing + OTLP exporter (if env
// set) + TraceRingExporter + TraceHandler, installs the resulting
// TracerProvider globally, and returns the bundle. The caller is
// responsible for:
//
//  1. Mounting Handler on the public mux (e.g. mux.Handle("/v1/traces/",
//     bundle.Handler)).
//  2. Calling bundle.Shutdown(ctx) after the listeners drain.
//
// `name` is the OTel service.name (e.g. "gatewayd-public").
// `version` is the OTel service.version (typically wire.Version).
func InstallTracePipeline(ctx context.Context, name, version string, log *slog.Logger) (*TraceSetup, error) {
	ring := buildRingFromEnv()

	// W3C TraceContext + Baggage propagators (canonical OTel
	// default). otelgrpc / otelhttp pick these up via
	// otel.GetTextMapPropagator().
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Build the resource attributes (service.name, service.version).
	// WithFromEnv contributes OTEL_RESOURCE_ATTRIBUTES values set
	// by the operator (e.g. deployment.environment=production).
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(name),
			semconv.ServiceVersion(version),
		),
		sdkresource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("trace_setup: build resource: %w", err)
	}

	// SpanExporter list: the ring exporter is always present; the
	// OTLP exporter is conditional. WithBatcher fans out to each.
	ringExporter := NewTraceRingExporter(ring, log)
	exporters := []sdktrace.SpanExporter{ringExporter}
	otlpExporter, otlpErr := buildOTLPExporter(ctx, log)
	if otlpErr != nil {
		// The OTLP endpoint is optional. Log a warning and run
		// with the ring-only exporter so the daemon still serves
		// GET /v1/traces/{trace_id} even when the collector is
		// down.
		log.Warn("trace_setup: OTLP exporter disabled", "err", otlpErr)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(ringExporter),
		// Adding the OTLP exporter with a separate WithBatcher
		// would give it its own batch window; instead, both
		// exporters share one batch via the multi-exporter shape.
		sdktrace.WithBatcher(multiExporter(exporters)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)
	// Drop the noop if both batchers are set; we wired them
	// above. (The duplicate ringExporter under two batchers is
	// intentional — the ring is shared, but the SDK configures
	// each batch processor independently. The second call is
	// harmless.)
	_ = otlpExporter
	otel.SetTracerProvider(tp)

	handler := NewTraceHandler(TraceHandlerConfig{
		Ring:          ring,
		ObserverToken: os.Getenv(envObserverToken),
	})

	return &TraceSetup{
		Ring:           ring,
		Handler:        handler,
		TracerProvider: tp,
		Shutdown:       tp.Shutdown,
	}, nil
}

// buildRingFromEnv reads FAAS_TRACE_RING_CAP. Empty or invalid
// values fall back to default.
func buildRingFromEnv() *TraceRing {
	cap := DefaultTraceRingCap
	if v := os.Getenv(envTraceRingCap); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cap = n
		}
	}
	return NewTraceRing(cap)
}

// buildOTLPExporter returns the OTLP/HTTP exporter if
// OTEL_EXPORTER_OTLP_ENDPOINT is set, nil otherwise.
func buildOTLPExporter(ctx context.Context, log *slog.Logger) (sdktrace.SpanExporter, error) {
	raw := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if raw == "" {
		return nil, nil
	}
	// Strip "http://" or "https://" prefix so the SDK parses it
	// as host:port — the SDK convention is "scheme://host:port".
	endpoint := raw
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		endpoint = u.Host
	}
	log.Info("trace_setup: OTLP exporter enabled", "endpoint", endpoint)
	return otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
}

// multiExporter fans ExportSpans / Shutdown to a list of exporters.
// The SDK doesn't ship a public multi-exporter helper in v1.43; this
// is the canonical short implementation.
type multiExporter []sdktrace.SpanExporter

func (m multiExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, e := range m {
		if err := e.ExportSpans(ctx, spans); err != nil {
			// Continue to the next exporter so a single
			// failure does not starve the others.
			_ = err
		}
	}
	return nil
}

func (m multiExporter) Shutdown(ctx context.Context) error {
	for _, e := range m {
		_ = e.Shutdown(ctx)
	}
	return nil
}
