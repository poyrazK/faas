// Package otelinit bootstraps the OpenTelemetry SDK per daemon
// (issue #555). One Init call per cmd/<daemon>/main.go; the returned
// shutdown func flushes and stops the batch span processor on SIGHUP
// / SIGTERM.
//
// Why per-daemon TracerProvider instead of otel.GetTracerProvider():
// the platform contract is that one daemon = one OTel exporter = one
// shutdown. Sharing a global across cmd/* would force every daemon
// to coordinate the lifecycle and would let pkg/wire tests leak
// background goroutines.
//
// Why a no-op fallback when OTEL_EXPORTER_OTLP_ENDPOINT is unset:
// the issue #555 acceptance tests must work without an otel-collector
// running. The SDK noop provider lets every site call tracer.Start
// without a nil-check; the shutdown func is a no-op when the
// provider is noop.
package otelinit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/onebox-faas/faas/pkg/wire"
)

// envEndpoint is the OTel-standard env var for the OTLP/HTTP endpoint.
// Issue #555 acceptance #4 toggles export via this single setting.
const envEndpoint = "OTEL_EXPORTER_OTLP_ENDPOINT"

// envSamplingRate is the OTel-standard env var for the head sampler.
// Issue #555 acceptance #5: 1 req/s default; first 100 requests of a
// new deployment are 100% sampled (the per-deployment counter lives
// in a sibling PR, not here — this PR lands the bare sampler).
const envSamplingRate = "OTEL_TRACES_SAMPLER_ARG"

// defaultSamplingRate is the head sampler rate when OTEL_TRACES_SAMPLER_ARG
// is unset. Issue #555 says 1 req/s by default — we model that as 1.0
// ratio baseline; the per-deployment 100% sampling window is layered
// on top in a follow-up PR.
const defaultSamplingRate = 1.0

// batchTimeout is the maximum time the BatchSpanProcessor holds a
// span batch before exporting. Kept under the 5s SIGTERM grace window
// so the shutdown flush does not stall the daemon drain.
const batchTimeout = 2 * time.Second

// ShutdownFunc is returned by Init. It is safe to call on a no-op
// provider (returns nil). The func is bounded by ctx; an unbounded
// shutdown can block forever if the OTLP collector is unresponsive.
type ShutdownFunc func(ctx context.Context) error

// Config is the minimally-required set of fields to bootstrap a
// daemon's OTel pipeline. The OTel SDK is otherwise self-configuring
// from env vars and resource attributes.
type Config struct {
	// Name is the daemon name (e.g. "gatewayd-public", "schedd").
	// Used as the OTel service.name resource attribute.
	Name string
	// Version is the daemon version (typically wire.Version, ldflags-overridable).
	Version string
}

// Init wires up the OTel SDK per the config. Returns a shutdown that
// flushes the batch processor and shuts down the exporter. The
// shutdown is a no-op when OTEL_EXPORTER_OTLP_ENDPOINT is unset (the
// noop provider path). Init is safe to call at most once per
// daemon; subsequent calls panic via SetTracerProvider.
//
// log is the daemon's correlation logger. It is used for one-time
// boot diagnostics (which exporter was selected, endpoint status); it
// is NOT attached to the OTel pipeline — slog stays separate from
// the OTel SDK per CLAUDE.md conventions (slog is the log canonical,
// OTel is the trace canonical).
func Init(ctx context.Context, cfg Config, log *slog.Logger) (ShutdownFunc, error) {
	if cfg.Name == "" {
		return nil, errors.New("otelinit: Name is required")
	}
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.Name),
			semconv.ServiceVersion(cfg.Version),
		),
		sdkresource.WithFromEnv(),
		sdkresource.WithProcess(),
	)
	if err != nil {
		return nil, fmt.Errorf("otelinit: build resource: %w", err)
	}

	// W3C TraceContext + Baggage propagators are the canonical OTel
	// default. otelgrpc (PR-3) and otelhttp (PR-2) pick these up
	// through otel.GetTextMapPropagator().
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	endpoint := os.Getenv(envEndpoint)
	if endpoint == "" {
		// No exporter configured. Install the SDK noop provider so
		// call sites do not have to nil-check. The shutdown returned
		// here is a no-op.
		otel.SetTracerProvider(noop.NewTracerProvider())
		log.Info("otelinit: no OTEL_EXPORTER_OTLP_ENDPOINT set; spans are no-op")
		return func(context.Context) error { return nil }, nil
	}

	// OTLP/HTTP exporter. otlptracehttp.WithEndpoint expects
	// "host:port" (no scheme); the SDK appends /v1/traces.
	client, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("otelinit: build OTLP/HTTP client: %w", err)
	}

	rate := defaultSamplingRate
	if v := os.Getenv(envSamplingRate); v != "" {
		var parsed float64
		if _, err := fmt.Sscanf(v, "%f", &parsed); err != nil {
			log.Warn("otelinit: invalid OTEL_TRACES_SAMPLER_ARG, falling back", "value", v)
		} else {
			rate = parsed
		}
	}
	sampler := sdktrace.ParentBased(sdktrace.TraceIDRatioBased(rate))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(client,
			sdktrace.WithBatchTimeout(batchTimeout),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)
	log.Info("otelinit: wired OTLP/HTTP exporter",
		"endpoint", endpoint, "sampler", "parent_based_trace_id_ratio",
		"sampler_arg", rate)

	return func(shutdownCtx context.Context) error {
		// Outer timeout is the higher of (ctx deadline, 5s) so a
		// long-running batch does not stall the daemon drain.
		flushCtx, cancel := context.WithTimeout(shutdownCtx, 5*time.Second)
		defer cancel()
		return tp.Shutdown(flushCtx)
	}, nil
}

// Tracer returns the OTel tracer for the named instrumentation scope.
// Equivalent to otel.Tracer(name) but centralised so any future
// scope-wide configuration (e.g. per-scope attributes) has a single
// hook.
//
// The name is conventionally the daemon name (e.g. "schedd",
// "vmmd") so spans attribute their emit site correctly.
func Tracer(name string) oteltrace.Tracer {
	return otel.Tracer(name)
}

// LiftSpanContext returns the trace_id and span_id from the active
// span on ctx, suitable for stamping onto the wire.CorrelationFields
// envelope. Returns empty strings when no span is active. The values
// are formatted as 32-hex / 16-hex strings (the W3C trace_id and
// span_id conventions); the slog JSON shape stays canonical.
//
// Use this at the canonical lift point (e.g. before forwarding a
// correlation struct across gRPC or HTTP) so downstream daemons can
// re-attach span context without re-parsing the inbound traceparent.
func LiftSpanContext(ctx context.Context) (traceID, spanID string) {
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return "", ""
	}
	return sc.TraceID().String(), sc.SpanID().String()
}

// LiftFromMD reads the x-faas-* trace context from the inbound gRPC
// metadata on ctx. The OTel SDK's otelgrpc server interceptor (PR-3)
// already extracts the W3C traceparent and stashes it on ctx; this
// helper reads the x-faas-* backup carrier so the engine has the
// trace_id/span_id strings for the slog envelope without having to
// re-parse the SpanContext.
//
// Returns ("", "") when nothing is set. The caller can safely call
// tracer.Start(ctx, ...) regardless — the OTel SDK treats absent
// span context as a fresh root span.
func LiftFromMD(ctx context.Context) (CorrelationIDs, bool) {
	cor, ok := wire.CorrelationFromIncoming(ctx)
	if !ok {
		return CorrelationIDs{}, false
	}
	return CorrelationIDs{
		TraceID: cor.TraceID,
		SpanID:  cor.SpanID,
	}, true
}

// CorrelationIDs is the simplest representation of the trace context
// the slog envelope needs (issue #555 layer 1). Exposed as a struct
// so future fields (tracestate, baggage) can be added without
// changing the call signature.
type CorrelationIDs struct {
	TraceID string
	SpanID  string
}
