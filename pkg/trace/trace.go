// Package trace is the OpenTelemetry facade for faas (issue #268,
// #758, cluster E commit 15 of the platform-observability
// mega-PR). One Init per cmd/<daemon>/main.go; StartSpan +
// SpanFromContext are the only two functions handlers + libraries
// need to call.
//
// Why a separate facade on top of pkg/wire/otelinit:
//
//   - Callers shouldn't have to import pkg/wire from cmd/<daemon>
//     just to call otelinit.Init — that's a layering violation
//     (pkg/wire imports most other packages, never the other way).
//   - StartSpan + SpanFromContext give handlers a one-line API
//     that doesn't require propagating the oteltrace package
//     import through every function signature.
//
// The facade delegates to pkg/wire/otelinit for the actual
// TracerProvider lifecycle; the Init + StartSpan + Shutdown
// triple here is a thin sugar so the call sites stay readable.
package trace

import (
	"context"
	"io"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/wire/otelinit"
)

// InitTracer (cluster E commit 15) initializes the per-daemon
// TracerProvider via pkg/wire/otelinit and returns a shutdown
// func the daemon's main.go defers. serviceName is the
// `service.name` resource attribute (e.g. "vmmd", "apid"); version
// is the `service.version` (typically wire.Version); log is the
// daemon's correlation logger used for one-time boot diagnostics.
// When log is nil the facade substitutes a discard logger so the
// otelinit path stays nil-safe. The OTEL_EXPORTER_OTLP_ENDPOINT
// env var is the trust input — when unset, the SDK falls back
// to the noop provider and shutdown is a no-op (see
// pkg/wire/otelinit security note).
func InitTracer(ctx context.Context, serviceName, version string, log *slog.Logger) (shutdown func(context.Context) error, err error) {
	return InitTracerWithRegistry(ctx, serviceName, version, log, nil, "")
}

// InitTracerWithRegistry initializes tracing and attaches trace exporter
// health metrics to reg. metricPrefix should match the daemon's OpsMetrics
// prefix; it may be empty when reg is nil.
func InitTracerWithRegistry(ctx context.Context, serviceName, version string, log *slog.Logger, reg prometheus.Registerer, metricPrefix string) (shutdown func(context.Context) error, err error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	h, err := otelinit.Init(ctx, otelinit.Config{
		Name:              serviceName,
		Version:           version,
		MetricsRegisterer: reg,
		MetricPrefix:      metricPrefix,
	}, log)
	if err != nil {
		return nil, err
	}
	if h == nil {
		// Defensive: otelinit.Init returns a non-nil handle even
		// on the noop path. Future-proof against a refactor.
		return func(context.Context) error { return nil }, nil
	}
	return h.Shutdown, nil
}

// StartSpan (cluster E commit 15) starts a span on the current
// TracerProvider (no-op when noop) and returns the new ctx +
// span. The caller MUST defer span.End(). attrs are the
// span's initial attribute set.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	tr := otelinit.Tracer("trace")
	return tr.Start(ctx, name, oteltrace.WithAttributes(attrs...))
}

// SpanFromContext (cluster E commit 15) returns the span
// attached to ctx, or oteltrace.SpanFromContext's noop fallback
// when no span is attached. Callers use this to stamp
// trace_id + span_id into the slog envelope (cmd/<daemon>/main.go
// already calls NewCorrelationLogger with these fields wired in
// via PR-A).
func SpanFromContext(ctx context.Context) oteltrace.Span {
	return oteltrace.SpanFromContext(ctx)
}
