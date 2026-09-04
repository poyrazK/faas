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
//
// Security note (issue #555 review): OTEL_EXPORTER_OTLP_ENDPOINT is
// an operator-controlled trust input. Explicit https:// endpoints use
// the SDK's TLS transport; explicit http:// endpoints and legacy bare
// host:port values use plaintext. On a one-box deployment the
// collector is expected to bind 127.0.0.1; on a multi-box deployment
// the operator should use https:// with the collector's certificates or
// a private network. Never accept this value from a customer-facing
// source (request headers, manifest env) — that would let a tenant
// exfiltrate trace data to an attacker-controlled host.
package otelinit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
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
// in this PR — see sampler.go).
const envSamplingRate = "OTEL_TRACES_SAMPLER_ARG"

// defaultSamplingRate is the head sampler rate when OTEL_TRACES_SAMPLER_ARG
// is unset. The DeploymentAware wrapper (sampler.go) consults the
// per-deployment counter BEFORE this ratio applies, so the effective
// decision for the first 100 root spans of any deployment is
// RecordAndSample regardless of this default.
const defaultSamplingRate = 1.0

// batchTimeout is the maximum time the BatchSpanProcessor holds a
// span batch before exporting. Kept under the 5s SIGTERM grace window
// so the shutdown flush does not stall the daemon drain.
const batchTimeout = 2 * time.Second

// ShutdownFunc is the func returned in Handle.Shutdown. It is safe to
// call on a no-op provider (returns nil). The func is bounded by ctx;
// an unbounded shutdown can block forever if the OTLP collector is
// unresponsive.
type ShutdownFunc func(ctx context.Context) error

// Handle is the bundle of state Init returns to its caller. Most
// daemons only need Shutdown; schedd additionally reads
// DeploymentCounter so the DeploymentCounterWatcher (PR-6) can reset
// the per-deployment 100% sampling window on the "last live instance
// parked" transition (issue #555 acceptance #5).
//
// The DeploymentCounter is non-nil on BOTH the no-op path and the
// OTLP-wired path — counter state is pure in-memory and the watcher
// is independent of whether an exporter is configured.
type Handle struct {
	// Shutdown flushes the batch processor and shuts down the
	// exporter. No-op on the no-OTLP-endpoint path.
	Shutdown ShutdownFunc
	// DeploymentCounter is the per-deployment 100% sampling window
	// counter. Sampler and watcher consult / reset the same
	// instance (see sampler.go and pkg/sched/deployment_counter_watcher.go).
	DeploymentCounter *DeploymentCounter
}

// Config is the minimally-required set of fields to bootstrap a
// daemon's OTel pipeline. The OTel SDK is otherwise self-configuring
// from env vars and resource attributes.
type Config struct {
	// Name is the daemon name (e.g. "gatewayd-public", "schedd").
	// Used as the OTel service.name resource attribute.
	Name string
	// Version is the daemon version (typically wire.Version, ldflags-overridable).
	Version string
	// WindowSize overrides the per-deployment 100% sampling window
	// size. 0 (the default) maps to DefaultWindowSize (100, per
	// issue #555 acceptance #5). The override exists so tests can
	// run with a 3- or 5-span window.
	WindowSize int
	// MetricsRegisterer receives trace exporter health metrics. Leave nil
	// when the caller does not expose a daemon Prometheus registry.
	MetricsRegisterer prometheus.Registerer
	// MetricPrefix is the exact daemon metric prefix (for example,
	// gatewayd_public). When empty, Name is used.
	MetricPrefix string
}

// Init wires up the OTel SDK per the config. Returns a Handle whose
// Shutdown func flushes the batch processor and shuts down the
// exporter. The shutdown is a no-op when OTEL_EXPORTER_OTLP_ENDPOINT
// is unset (the noop provider path). Init is safe to call at most
// once per daemon; subsequent calls panic via SetTracerProvider.
//
// log is the daemon's correlation logger. It is used for one-time
// boot diagnostics (which exporter was selected, endpoint status); it
// is NOT attached to the OTel pipeline — slog stays separate from
// the OTel SDK per CLAUDE.md conventions (slog is the log canonical,
// OTel is the trace canonical).
func Init(ctx context.Context, cfg Config, log *slog.Logger) (*Handle, error) {
	if cfg.Name == "" {
		return nil, errors.New("otelinit: Name is required")
	}
	metricPrefix := cfg.MetricPrefix
	if metricPrefix == "" {
		metricPrefix = cfg.Name
	}
	health, err := NewTraceExporterHealth(cfg.MetricsRegisterer, metricPrefix)
	if err != nil {
		return nil, fmt.Errorf("otelinit: register trace exporter health: %w", err)
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

	// The per-deployment counter is constructed on every Init — the
	// watcher (PR-6) resets it on the "last live instance parked"
	// transition. The counter is independent of the exporter
	// configuration: a daemon without an OTLP endpoint still
	// maintains the counter so the window semantics hold once the
	// operator toggles the endpoint on.
	counter := NewDeploymentCounter(cfg.WindowSize)

	endpoint := os.Getenv(envEndpoint)
	if endpoint == "" {
		// No exporter configured. Install the SDK noop provider so
		// call sites do not have to nil-check. The shutdown returned
		// here is a no-op. The counter is still constructed so the
		// watcher can wire against it.
		otel.SetTracerProvider(noop.NewTracerProvider())
		log.Info("otelinit: no OTEL_EXPORTER_OTLP_ENDPOINT set; spans are no-op")
		return &Handle{
			Shutdown:          func(context.Context) error { return nil },
			DeploymentCounter: counter,
		}, nil
	}

	// OTLP/HTTP supports both the legacy host:port form and a full URL.
	// Full HTTPS URLs retain TLS; bare host:port and explicit HTTP remain
	// plaintext for backwards compatibility.
	exporterOptions := []otlptracehttp.Option{otlptracehttp.WithTimeout(5 * time.Second)}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		exporterOptions = append(exporterOptions, otlptracehttp.WithEndpointURL(endpoint))
		if strings.HasPrefix(endpoint, "http://") {
			exporterOptions = append(exporterOptions, otlptracehttp.WithInsecure())
		}
	} else {
		exporterOptions = append(exporterOptions,
			otlptracehttp.WithEndpoint(endpoint),
			otlptracehttp.WithInsecure(),
		)
	}
	client, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		health.SetUnavailable()
		return nil, fmt.Errorf("otelinit: build OTLP/HTTP client: %w", err)
	}
	var exporter sdktrace.SpanExporter = client
	if health != nil {
		health.SetEnabled(true)
		exporter = health.Wrap(exporter)
	}

	rate := defaultSamplingRate
	if v := os.Getenv(envSamplingRate); v != "" {
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			// Issue #555 review: the previous fmt.Sscanf accepted
			// trailing junk ("1.0xyz" parsed as 1.0); ParseFloat
			// rejects it. Falls back to defaultSamplingRate.
			log.Warn("otelinit: invalid OTEL_TRACES_SAMPLER_ARG, falling back", "value", v, "err", err)
		} else {
			rate = parsed
		}
	}
	// Sampler chain: ParentBased(DeploymentAware(TraceIDRatioBased(rate))).
	// ParentBased is the outer wrapper so child spans inherit the
	// parent's SampledFlag (W3C parent-trace invariant). The
	// DeploymentAware wrapper only sees root spans and either forces
	// RecordAndSample inside the per-deployment window or delegates
	// to TraceIDRatioBased (issue #555 acceptance #5).
	root := sdktrace.TraceIDRatioBased(rate)
	sampler := sdktrace.ParentBased(NewDeploymentAware(root, WithCounter(counter)))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(batchTimeout),
			sdktrace.WithMaxExportBatchSize(512),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)
	log.Info("otelinit: wired OTLP/HTTP exporter",
		"endpoint", endpoint, "sampler", "parent_based_deployment_aware_trace_id_ratio",
		"sampler_arg", rate, "window_size", counter.WindowSize())

	return &Handle{
		Shutdown: func(shutdownCtx context.Context) error {
			// Outer timeout is the higher of (ctx deadline, 5s) so a
			// long-running batch does not stall the daemon drain.
			flushCtx, cancel := context.WithTimeout(shutdownCtx, 5*time.Second)
			defer cancel()
			return tp.Shutdown(flushCtx)
		},
		DeploymentCounter: counter,
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
